package engine

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func addrs(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestRouteCommandsFreshGateway(t *testing.T) {
	// 监督进程清空主路由表之后的首次设置；首跳在宿主之外，要经网桥地址转发。
	net := netip.MustParsePrefix("172.20.0.0/16")
	cmds, err := routeCommands("", "eth0", net, netip.MustParseAddr("172.20.0.1"), addrs("203.0.113.10"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ip route replace 203.0.113.10 via 172.20.0.1 dev eth0 onlink"}
	if strings.Join(cmds, ";") != strings.Join(want, ";") {
		t.Fatalf("得到 %v，期望 %v", cmds, want)
	}
}

func TestRouteCommandsOnLinkHop(t *testing.T) {
	// 首跳与网关同网段（端到端测试的情形，或首跳是宿主在出口网络上的地址）。
	net := netip.MustParsePrefix("203.0.113.0/28")
	cmds, err := routeCommands("", "eth0", net, netip.Addr{}, addrs("203.0.113.3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0] != "ip route replace 203.0.113.3 dev eth0 scope link" {
		t.Fatalf("同网段首跳应直接走出口网卡: %v", cmds)
	}
}

func TestRouteCommandsRemovesEverythingElseOnEgress(t *testing.T) {
	current := strings.Join([]string{
		"default via 172.20.0.1 dev eth0",
		"172.20.0.0/16 dev eth0 proto kernel scope link src 172.20.0.2",
		"198.51.100.9 via 172.20.0.1 dev eth0 onlink",                      // 不再使用的旧首跳
		"203.0.113.10 via 172.20.0.1 dev eth0 onlink",                      // 仍在使用
		"10.213.250.8/30 dev cs2 proto kernel scope link src 10.213.250.9", // 槽位回程路由，不能动
	}, "\n")
	cmds, err := routeCommands(current, "eth0", netip.MustParsePrefix("172.20.0.0/16"), netip.MustParseAddr("172.20.0.1"), addrs("203.0.113.10"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"ip route del default dev eth0", "ip route del 172.20.0.0/16 dev eth0", "ip route del 198.51.100.9 dev eth0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少 %q：%v", want, cmds)
		}
	}
	if strings.Contains(joined, "cs2") || strings.Contains(joined, "10.213.250") {
		t.Errorf("不能动槽位 veth 上的路由: %v", cmds)
	}
	if strings.Contains(joined, "203.0.113.10") {
		t.Errorf("已就位的首跳路由不应被重写（会打断既有连接）: %v", cmds)
	}
}

func TestRouteCommandsAlreadyAligned(t *testing.T) {
	current := "203.0.113.3 dev eth0 scope link\n10.213.250.0/30 dev cs0 proto kernel scope link src 10.213.250.1\n"
	cmds, err := routeCommands(current, "eth0", netip.MustParsePrefix("203.0.113.0/28"), netip.Addr{}, addrs("203.0.113.3"))
	if err != nil || len(cmds) != 0 {
		t.Fatalf("已对齐时不应有任何命令: %v %v", cmds, err)
	}
}

func TestRouteCommandsFixesWrongNexthop(t *testing.T) {
	current := "203.0.113.10 via 172.20.0.9 dev eth0 onlink\n"
	cmds, _ := routeCommands(current, "eth0", netip.MustParsePrefix("172.20.0.0/16"), netip.MustParseAddr("172.20.0.1"), addrs("203.0.113.10"))
	if len(cmds) != 1 || !strings.Contains(cmds[0], "replace 203.0.113.10 via 172.20.0.1") {
		t.Fatalf("下一跳不对的路由应被改写: %v", cmds)
	}
}

func TestRouteCommandsNoHopsMeansNoEgress(t *testing.T) {
	current := "default via 172.20.0.1 dev eth0\n172.20.0.0/16 dev eth0 proto kernel scope link src 172.20.0.2\n"
	cmds, _ := routeCommands(current, "eth0", netip.MustParsePrefix("172.20.0.0/16"), netip.MustParseAddr("172.20.0.1"), nil)
	if len(cmds) != 2 {
		t.Fatalf("没有首跳时出口网卡上不应留任何路由: %v", cmds)
	}
	for _, c := range cmds {
		if !strings.HasPrefix(c, "ip route del ") {
			t.Fatalf("只应有删除命令: %v", cmds)
		}
	}
}

func TestRouteCommandsErrors(t *testing.T) {
	// internal 网络没有网关地址，够不着网段之外的首跳。
	if _, err := routeCommands("", "eth0", netip.MustParsePrefix("203.0.113.0/28"), netip.Addr{}, addrs("198.51.100.3")); err == nil {
		t.Error("够不着的首跳应报错")
	}
	if _, err := routeCommands("", "eth0", netip.MustParsePrefix("203.0.113.0/28"), netip.Addr{}, addrs("2001:db8::1")); err == nil {
		t.Error("IPv6 首跳应报错")
	}
}

func TestIfaceWithAddr(t *testing.T) {
	out := "1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever\n" +
		"7: eth0    inet 203.0.113.2/28 brd 203.0.113.15 scope global eth0\\       valid_lft forever preferred_lft forever\n" +
		"9: cs0    inet 10.213.250.1/30 scope global cs0\\       valid_lft forever preferred_lft forever\n"
	if got := ifaceWithAddr(out, netip.MustParseAddr("203.0.113.2")); got != "eth0" {
		t.Fatalf("应找到 eth0，得到 %q", got)
	}
	if got := ifaceWithAddr(out, netip.MustParseAddr("203.0.113.9")); got != "" {
		t.Fatalf("不存在的地址应返回空，得到 %q", got)
	}
}

func TestParseExitIP(t *testing.T) {
	cases := map[string]string{
		"198.51.100.3\n":           "198.51.100.3",
		"SEEN 198.51.100.4\n":      "198.51.100.4",
		`{"ip":"203.0.113.77"}`:    "203.0.113.77",
		"999.1.1.1 then 192.0.2.5": "192.0.2.5",
		"<html>no address</html>":  "",
		"":                         "",
	}
	for body, want := range cases {
		got, ok := parseExitIP(body)
		if got != want || ok != (want != "") {
			t.Errorf("parseExitIP(%q) = %q, %v；期望 %q", body, got, ok, want)
		}
	}
}

func testSlot() (model.Slot, model.Identity) {
	sl := model.Slot{
		ID: "a", Gateway: "main", Landing: "home-a", Subnet: "10.213.250.8/30", UID: 1002, GID: 1002,
		Mounts: []model.Mount{{Source: "/data/proj", Target: "/work"}, {Source: "/data/ref", Target: "/ref", ReadOnly: true}},
		Env:    map[string]string{"EDITOR": "vim", "HTTPS_PROXY": "http://should-never-appear"},
		TZ:     "Asia/Shanghai", MemoryBytes: 8 << 30, CPUs: 2.5,
	}
	sl.Normalize()
	return sl, model.Identity{MachineID: strings.Repeat("ab", 16), Hostname: "dev-k3x9q1", MAC: "02:11:22:33:44:55"}
}

func TestWorkerSpecIsolation(t *testing.T) {
	sl, ident := testSlot()
	files := workerFiles{Home: "/d/slots/a/home", MachineID: "/d/slots/a/etc/machine-id", Passwd: "/d/slots/a/etc/passwd", Group: "/d/slots/a/etc/group", ResolvConf: "/d/slots/a/etc/resolv.conf"}
	spec := workerSpec("img:1", sl, ident, files, map[string]string{"k": "v"})
	hc := spec.HostConfig

	if hc.NetworkMode != "none" {
		t.Fatalf("worker 必须以无网络启动: %q", hc.NetworkMode)
	}
	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || len(hc.CapAdd) != 0 {
		t.Fatalf("worker 不应有任何能力: drop=%v add=%v", hc.CapDrop, hc.CapAdd)
	}
	if !hc.ReadonlyRootfs || len(hc.SecurityOpt) != 1 || hc.SecurityOpt[0] != "no-new-privileges" {
		t.Fatal("worker 必须只读 rootfs 且禁止提权")
	}
	if len(hc.Devices) != 0 || hc.PidMode != "" {
		t.Fatal("worker 不应有设备或共享 PID 命名空间")
	}
	if spec.User != "1002:1002" || spec.Hostname != "dev-k3x9q1" {
		t.Fatalf("用户与 hostname 不对: %s %s", spec.User, spec.Hostname)
	}
	if hc.Sysctls["net.ipv6.conf.all.disable_ipv6"] != "1" {
		t.Fatal("worker 必须关闭 IPv6")
	}
	if len(hc.DNS) != 1 || hc.DNS[0] != workerDNS {
		t.Fatalf("resolv.conf 应只有一个普通的解析器地址: %v", hc.DNS)
	}
	if hc.Memory != 8<<30 || hc.NanoCPUs != 2_500_000_000 || hc.PidsLimit != model.DefaultPidsLimit {
		t.Fatalf("资源限额不对: %d %d %d", hc.Memory, hc.NanoCPUs, hc.PidsLimit)
	}

	env := strings.Join(spec.Env, "\n")
	if strings.Contains(strings.ToLower(env), "proxy") {
		t.Fatalf("worker 环境里不允许出现任何代理变量:\n%s", env)
	}
	for _, want := range []string{"HOME=/home/dev", "EDITOR=vim", "TZ=Asia/Shanghai", "LANG=C.UTF-8"} {
		if !strings.Contains(env, want) {
			t.Errorf("环境变量缺少 %s", want)
		}
	}

	binds := strings.Join(hc.Binds, "\n")
	for _, want := range []string{
		"/d/slots/a/home:/home/dev", "/d/slots/a/etc/machine-id:/etc/machine-id:ro",
		"/d/slots/a/etc/passwd:/etc/passwd:ro", "/data/proj:/work", "/data/ref:/ref:ro",
	} {
		if !strings.Contains(binds, want) {
			t.Errorf("挂载缺少 %s", want)
		}
	}
	if strings.Contains(binds, "docker.sock") {
		t.Fatal("worker 不能挂 Docker socket")
	}

	masked := strings.Join(hc.MaskedPaths, "\n")
	if !strings.Contains(masked, "/sys/devices/virtual/dmi/id") {
		t.Fatal("必须遮蔽 DMI 信息")
	}
	if !strings.Contains(masked, "/proc/kcore") || !strings.Contains(masked, "/sys/firmware") {
		t.Fatal("给出 MaskedPaths 会替换 Docker 的默认列表，默认项必须一并带上")
	}
}

func TestWorkerSpecDigestTracksChanges(t *testing.T) {
	sl, ident := testSlot()
	files := workerFiles{Home: "/h", MachineID: "/m", Passwd: "/p", Group: "/g"}
	a := specDigest(workerSpec("img:1", sl, ident, files, map[string]string{}))
	if a != specDigest(workerSpec("img:1", sl, ident, files, map[string]string{})) {
		t.Fatal("相同输入应得到相同摘要")
	}
	sl.Mounts = sl.Mounts[:1]
	if a == specDigest(workerSpec("img:1", sl, ident, files, map[string]string{})) {
		t.Fatal("挂载变化必须改变摘要")
	}
}

func TestPasswdAndGroup(t *testing.T) {
	sl, _ := testSlot()
	pw := string(passwdFile(sl))
	if !strings.Contains(pw, "dev:x:1002:1002:dev:/home/dev:/bin/bash\n") || !strings.HasPrefix(pw, "root:x:0:0:") {
		t.Fatalf("passwd 内容不对:\n%s", pw)
	}
	if gr := string(groupFile(sl)); !strings.Contains(gr, "dev:x:1002:\n") {
		t.Fatalf("group 内容不对:\n%s", gr)
	}
}
