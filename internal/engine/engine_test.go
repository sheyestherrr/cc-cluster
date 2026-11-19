package engine

import (
	"net/netip"
	"strconv"
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

func TestParseExit(t *testing.T) {
	info, ok := parseExit(`{"ip": "203.0.113.77", "country": "US", "org": "AS64500 Example", "timezone": "America/Los_Angeles"}`)
	if !ok || info.IP != "203.0.113.77" || info.TZ != "America/Los_Angeles" || info.Country != "US" || info.Org != "AS64500 Example" {
		t.Errorf("JSON 应答应给出 IP、时区、国家和运营商: %+v %v", info, ok)
	}
	// 时区会写进容器的环境变量和启动脚本，写法不对的不采用，但 IP 仍然有效。
	info, ok = parseExit(`{"ip": "203.0.113.77", "timezone": "x; rm -rf /"}`)
	if !ok || info.IP != "203.0.113.77" || info.TZ != "" {
		t.Errorf("不合法的时区应被丢弃: %+v %v", info, ok)
	}
	if info, ok = parseExit("198.51.100.3\n"); !ok || info.IP != "198.51.100.3" || info.TZ != "" {
		t.Errorf("纯文本应答只有 IP: %+v %v", info, ok)
	}
	for _, body := range []string{`{"error": "rate limited"}`, `{"ip": "not-an-ip"}`, `{broken`, ""} {
		if info, ok = parseExit(body); ok {
			t.Errorf("parseExit(%q) 不应成功: %+v", body, info)
		}
	}
	if got := (UpResult{ExitIP: "203.0.113.77", Exit: exitInfo{Country: "US", Org: "AS64500 Example"}}).ExitSummary(); got != "203.0.113.77（US，AS64500 Example）" {
		t.Errorf("出口描述: %q", got)
	}
	if got := (UpResult{ExitIP: "203.0.113.77"}).ExitSummary(); got != "203.0.113.77" {
		t.Errorf("没有归属信息时只显示 IP: %q", got)
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
	return sl, model.Identity{
		MachineID: strings.Repeat("ab", 16), Hostname: "kevin-OptiPlex-7090", MAC: "3c:2c:30:33:44:55", User: "kevin",
		DMI: map[string]string{"sys_vendor": "Dell Inc.", "product_name": "OptiPlex 7090", "bios_date": "07/12/2023", "chassis_type": "3"},
	}
}

func testParams() workerParams {
	sl, ident := testSlot()
	return workerParams{Image: "img:1", Slot: sl, Ident: ident, TZ: sl.TZ, Lang: model.DefaultLang, HomeDir: "/d/slots/a/home", Labels: map[string]string{"k": "v"}}
}

func TestWorkerSpecIsolation(t *testing.T) {
	spec := workerSpec(testParams())
	hc := spec.HostConfig

	if hc.NetworkMode != "none" {
		t.Fatalf("worker 必须以无网络启动: %q", hc.NetworkMode)
	}
	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || len(hc.CapAdd) != 0 {
		t.Fatalf("worker 不应有任何能力: drop=%v add=%v", hc.CapDrop, hc.CapAdd)
	}
	if len(hc.SecurityOpt) != 1 || hc.SecurityOpt[0] != "no-new-privileges" {
		t.Fatal("worker 必须禁止提权")
	}
	if hc.CgroupnsMode != "private" {
		t.Fatal("worker 必须使用私有的 cgroup 命名空间")
	}
	if len(hc.Devices) != 0 || hc.PidMode != "" {
		t.Fatal("worker 不应有设备或共享 PID 命名空间")
	}
	if spec.User != "1002:1002" || spec.Hostname != "kevin-OptiPlex-7090" || spec.WorkingDir != "/home/kevin" {
		t.Fatalf("用户、hostname 或工作目录不对: %s %s %s", spec.User, spec.Hostname, spec.WorkingDir)
	}
	if len(hc.ExtraHosts) != 1 || hc.ExtraHosts[0] != "kevin-OptiPlex-7090:127.0.1.1" {
		t.Fatalf("主机名应解析到 127.0.1.1: %v", hc.ExtraHosts)
	}
	if hc.Sysctls["net.ipv6.conf.all.disable_ipv6"] != "1" {
		t.Fatal("worker 必须关闭 IPv6")
	}
	if len(hc.DNS) != 1 || hc.DNS[0] != workerDNS || len(hc.DNSSearch) != 1 || hc.DNSSearch[0] != "." {
		t.Fatalf("Docker 生成的 resolv.conf 不应带宿主的 search 域: %v %v", hc.DNS, hc.DNSSearch)
	}
	if hc.Memory != 8<<30 || hc.NanoCPUs != 2_500_000_000 || hc.PidsLimit != model.DefaultPidsLimit {
		t.Fatalf("资源限额不对: %d %d %d", hc.Memory, hc.NanoCPUs, hc.PidsLimit)
	}

	env := strings.Join(spec.Env, "\n")
	if strings.Contains(strings.ToLower(env), "proxy") {
		t.Fatalf("worker 环境里不允许出现任何代理变量:\n%s", env)
	}
	for _, want := range []string{"HOME=/home/kevin", "USER=kevin", "EDITOR=vim", "TZ=Asia/Shanghai", "LANG=en_US.UTF-8"} {
		if !strings.Contains(env, want) {
			t.Errorf("环境变量缺少 %s", want)
		}
	}

	binds := strings.Join(hc.Binds, "\n")
	for _, want := range []string{"/d/slots/a/home:/home/kevin", "/data/proj:/work", "/data/ref:/ref:ro"} {
		if !strings.Contains(binds, want) {
			t.Errorf("挂载缺少 %s", want)
		}
	}
	// 身份文件写进容器自己的文件层，不走挂载：挂载会在容器内的 mountinfo 里带出宿主路径。
	for _, never := range []string{"docker.sock", "/etc/passwd", "/etc/machine-id", "/proc/cpuinfo", dmiPath} {
		if strings.Contains(binds, never) {
			t.Errorf("默认不应有挂载 %s", never)
		}
	}

	masked := strings.Join(hc.MaskedPaths, "\n")
	if !strings.Contains(masked, dmiPath) {
		t.Fatal("默认必须遮蔽 DMI 信息")
	}
	if !strings.Contains(masked, "/proc/kcore") || !strings.Contains(masked, "/sys/firmware") {
		t.Fatal("给出 MaskedPaths 会替换 Docker 的默认列表，默认项必须一并带上")
	}
}

func TestWorkerSpecHardwareOverrides(t *testing.T) {
	p := testParams()
	p.DMIDir, p.CPUInfo, p.MemInfo = "/d/slots/a/etc/dmi", "/d/slots/a/etc/cpuinfo", "/d/slots/a/etc/meminfo"
	hc := workerSpec(p).HostConfig
	binds := strings.Join(hc.Binds, "\n")
	for _, want := range []string{"/d/slots/a/etc/dmi:" + dmiPath + ":ro", "/d/slots/a/etc/cpuinfo:/proc/cpuinfo:ro", "/d/slots/a/etc/meminfo:/proc/meminfo:ro"} {
		if !strings.Contains(binds, want) {
			t.Errorf("挂载缺少 %s", want)
		}
	}
	// 遮蔽在挂载之后生效，留在列表里会把覆盖的内容再遮掉。
	if strings.Contains(strings.Join(hc.MaskedPaths, "\n"), dmiPath) {
		t.Fatal("覆盖 DMI 时不能同时遮蔽它")
	}
}

func TestWorkerDigestTracksChanges(t *testing.T) {
	p := testParams()
	a := workerDigest(workerSpec(p), p.Ident, nil)
	if a != workerDigest(workerSpec(p), p.Ident, nil) {
		t.Fatal("相同输入应得到相同摘要")
	}
	q := p
	q.Slot.Mounts = q.Slot.Mounts[:1]
	if a == workerDigest(workerSpec(q), q.Ident, nil) {
		t.Fatal("挂载变化必须改变摘要")
	}
	q = p
	q.Ident.MachineID = strings.Repeat("cd", 16)
	if a == workerDigest(workerSpec(q), q.Ident, nil) {
		t.Fatal("machine-id 不在容器规格里，但写进了容器，也必须改变摘要")
	}
	if a != workerDigest(workerSpec(p), p.Ident, map[string][]byte{}) {
		t.Fatal("没有额外文件时摘要不应变化：否则已有的 worker 会被无故要求重建")
	}
	if a == workerDigest(workerSpec(p), p.Ident, map[string][]byte{"etc/x": []byte("1")}) {
		t.Fatal("写进容器的额外文件必须改变摘要")
	}
}

func TestMergePasswdAndGroup(t *testing.T) {
	sl, ident := testSlot()
	base := "root:x:0:0:root:/root:/bin/bash\ndaemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin\nnode:x:1002:1002::/home/node:/bin/bash\nkevin:x:1500:1500::/home/old:/bin/sh\n"
	pw := string(mergePasswd([]byte(base), ident, sl))
	if !strings.HasSuffix(pw, "kevin:x:1002:1002:Kevin:/home/kevin:/bin/bash\n") || !strings.HasPrefix(pw, "root:x:0:0:") || !strings.Contains(pw, "daemon:x:1:1:") {
		t.Fatalf("passwd 内容不对:\n%s", pw)
	}
	if strings.Contains(pw, "node:") || strings.Contains(pw, "/home/old") {
		t.Fatalf("同 UID 或同名的旧条目应被去掉:\n%s", pw)
	}
	if got := string(mergePasswd(nil, ident, sl)); !strings.HasPrefix(got, "root:x:0:0:") || !strings.Contains(got, "kevin:x:1002:1002:") {
		t.Fatalf("镜像没有 passwd 时应从最小内容开始:\n%s", got)
	}

	gr := string(mergeGroup([]byte("root:x:0:\nusers:x:100:\nkevin:x:1500:\n"), ident, sl))
	if gr != "root:x:0:\nusers:x:100:\nkevin:x:1002:\n" {
		t.Fatalf("group 内容不对:\n%s", gr)
	}
	if gr := string(mergeGroup([]byte("root:x:0:\nstaff:x:1002:\n"), ident, sl)); gr != "root:x:0:\nstaff:x:1002:\n" {
		t.Fatalf("已有同 GID 的组时应沿用:\n%s", gr)
	}
}

func TestLimitCPUInfo(t *testing.T) {
	block := func(i int) string {
		return "processor\t: " + strconv.Itoa(i) + "\nmodel name\t: Intel(R) Xeon(R)\nsiblings\t: 48\ncore id\t\t: " + strconv.Itoa(i) + "\ncpu cores\t: 24"
	}
	real := block(0) + "\n\n" + block(1) + "\n\n" + block(2) + "\n\n" + block(3) + "\n\n"
	got := string(limitCPUInfo([]byte(real), 2))
	if strings.Count(got, "processor\t:") != 2 || strings.Contains(got, ": 48") || strings.Contains(got, ": 24") {
		t.Fatalf("应只剩 2 个处理器且核数字段一致:\n%s", got)
	}
	if !strings.Contains(got, "siblings\t: 2\n") || !strings.Contains(got, "cpu cores\t: 2") || !strings.Contains(got, "model name\t: Intel(R) Xeon(R)") {
		t.Fatalf("字段改写不对:\n%s", got)
	}
	if n := strings.Count(string(limitCPUInfo([]byte(real), 99)), "processor\t:"); n != 4 {
		t.Fatalf("限额超过实际核数时保持原样，得到 %d", n)
	}
}

func TestLimitMemInfo(t *testing.T) {
	real := "MemTotal:       16000000 kB\nMemFree:         8000000 kB\nHugePages_Total:       0\nHugepagesize:       2048 kB\n"
	got := string(limitMemInfo([]byte(real), 4000000*1024))
	for _, want := range []string{"MemTotal:        4000000 kB", "MemFree:         2000000 kB", "HugePages_Total:       0", "Hugepagesize:       2048 kB"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q:\n%s", want, got)
		}
	}
	if string(limitMemInfo([]byte(real), 32000000*1024)) != real {
		t.Fatal("限额不小于实际总量时保持原样")
	}
}

func TestDMIFiles(t *testing.T) {
	_, ident := testSlot()
	files := dmiFiles(ident.DMI)
	if string(files["sys_vendor"]) != "Dell Inc.\n" || string(files["product_name"]) != "OptiPlex 7090\n" {
		t.Fatalf("字段内容不对: %q", files["sys_vendor"])
	}
	if !strings.HasPrefix(string(files["modalias"]), "dmi:bvn") || !strings.Contains(string(files["modalias"]), ":svnDellInc.:pnOptiPlex7090:") {
		t.Fatalf("modalias 不对: %s", files["modalias"])
	}
	if string(files["uevent"]) != "MODALIAS="+string(files["modalias"]) {
		t.Fatal("uevent 应与 modalias 一致")
	}
}
