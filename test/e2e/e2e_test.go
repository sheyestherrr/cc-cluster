//go:build e2e

// Package e2e 是端到端测试：在真实的 Docker 上用假节点验证数据路径与故障时的行为。
//
// 拓扑：两个 internal 网络（出口网段 203.0.113.0/28、远端网段 198.51.100.0/28），
// 假中转跨接两个网络，两个假落地和一个假目标在远端网段。所有网络都是 internal，
// 没有任何一条通往宿主外部的路径；真实代理协议不在这里验证。
//
// 运行：make e2e（需要 Docker 和网关镜像）。
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/cli"
	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

const (
	cluster   = "cce2e"
	netEgress = "cce2e-egress"
	netRemote = "cce2e-remote"
	e2eLabel  = "cc-cluster.e2e"

	ipTransitEgress = "203.0.113.3"
	ipTransitRemote = "198.51.100.2"
	// 第二个假中转，用作候选。
	ipTransit2Egress = "203.0.113.9"
	ipTransit2Remote = "198.51.100.9"
	ipLandA          = "198.51.100.3"
	ipLandB          = "198.51.100.4"
	ipTarget         = "198.51.100.5"
	fakenet          = "/opt/e2e/fakenet"
)

type env struct {
	t     *testing.T
	ctx   context.Context
	dk    *dockerapi.Client
	eng   *engine.Engine
	st    *store.Store
	image string
	bin   string
}

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func setup(t *testing.T) *env {
	t.Helper()
	base := os.Getenv("CC_E2E_DIR")
	if base == "" {
		t.Fatal("需要环境变量 CC_E2E_DIR（用 make e2e 运行）")
	}
	base, _ = filepath.Abs(base)
	image := os.Getenv("CC_E2E_GATEWAY_IMAGE")
	if image == "" {
		image = model.DefaultGatewayImage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	t.Cleanup(cancel)

	dk, err := dockerapi.New(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if has, err := dk.ImageExists(ctx, image); err != nil || !has {
		t.Fatalf("本地没有网关镜像 %s（%v）；先执行 make image-gateway", image, err)
	}
	e := &env{t: t, ctx: ctx, dk: dk, image: image, bin: filepath.Join(base, "bin")}

	// 假服务的静态二进制，挂进各容器使用。
	if err := os.MkdirAll(e.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(e.bin, "fakenet"), "./fakenet")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建 fakenet 失败: %v\n%s", err, out)
	}

	e.cleanup()
	if os.Getenv("CC_E2E_KEEP") == "" {
		t.Cleanup(e.cleanup)
	}

	for name, subnet := range map[string]string{netEgress: "203.0.113.0/28", netRemote: "198.51.100.0/28"} {
		err := dk.NetworkCreate(ctx, dockerapi.NetworkSpec{Name: name, Internal: true, Subnet: subnet, Labels: map[string]string{e2eLabel: "1"}})
		if err != nil {
			t.Fatalf("创建网络 %s: %v", name, err)
		}
	}
	e.service("target", netRemote, ipTarget, "target", ipTarget)
	e.service("land-a", netRemote, ipLandA, "socks5", "1080", ipTarget)
	e.service("land-b", netRemote, ipLandB, "socks5", "1080", ipTarget)
	e.service("transit", netEgress, ipTransitEgress, "socks5", "1080")
	if err := dk.NetworkConnect(ctx, netRemote, cluster+"-svc-transit", ipTransitRemote); err != nil {
		t.Fatalf("假中转接入远端网络: %v", err)
	}
	e.service("transit2", netEgress, ipTransit2Egress, "socks5", "1080")
	if err := dk.NetworkConnect(ctx, netRemote, cluster+"-svc-transit2", ipTransit2Remote); err != nil {
		t.Fatalf("第二个假中转接入远端网络: %v", err)
	}

	data := filepath.Join(base, "data")
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	e.st, err = store.Init(data, model.Cluster{Name: cluster, GatewayImage: image, WorkerImage: image})
	if err != nil {
		t.Fatal(err)
	}
	e.eng, err = engine.New(e.st, dk, logWriter{t})
	if e.eng != nil {
		e.eng.NoExitReport = true // 出口评估要访问外部接口，端到端测试不连外部地址
	}
	if err != nil {
		t.Fatal(err)
	}

	socks := func(server string) map[string]any {
		return map[string]any{"type": "socks5", "server": server, "port": 1080}
	}
	// 中转用域名 + connect_ipv4，顺带验证核心的 hosts 固定。
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "transit", Role: model.RoleTransit, Proxy: socks("transit.test"), ConnectIPv4: ipTransitEgress}))
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "transit2", Role: model.RoleTransit, Proxy: socks(ipTransit2Egress)}))
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "la", Role: model.RoleLanding, Proxy: socks(ipLandA), ExpectExitIP: ipLandA, TZ: "Asia/Tokyo"}))
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "lb", Role: model.RoleLanding, Proxy: socks(ipLandB), ExpectExitIP: ipLandB}))
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "lwrong", Role: model.RoleLanding, Proxy: socks(ipLandB), ExpectExitIP: "198.51.100.99"}))
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "ldead", Role: model.RoleLanding, Proxy: socks("198.51.100.98")}))
	must(t, e.eng.AddGateway(model.Gateway{
		ID: "main", Transit: "transit", EgressNetwork: netEgress,
		DNS: []string{"tcp://" + ipTarget + ":53"}, EchoURLs: []string{"http://" + ipTarget + "/json"},
		ConnectURLs: []string{"http://" + ipTarget + "/"},
	}))
	e.addSlot("a", "la")
	e.addSlot("b", "lb")
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) hostTmuxVersion(bin string) string {
	out, _ := exec.Command(bin, "-V").Output()
	return strings.TrimSpace(string(out))
}

func truncateForLog(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

func (e *env) addSlot(id, landing string) {
	e.t.Helper()
	sl := model.Slot{
		ID: id, Gateway: "main", Landing: landing, Shell: "/bin/sh", UID: os.Getuid(), GID: os.Getgid(),
		Mounts: []model.Mount{{Source: e.bin, Target: "/opt/e2e", ReadOnly: true}},
	}
	var opt engine.SlotOptions
	if id == "a" {
		// 槽 a 打开全部可选项，槽 b 保持默认，两种情形各有一个槽覆盖。
		opt.User = "kevin"
		sl.FakeDMI, sl.FakeProc, sl.CPUs, sl.MemoryBytes = true, true, 2, 1<<30
	}
	_, _, err := e.eng.AddSlot(e.ctx, sl, opt)
	must(e.t, err)
}

// service 启动一个假服务容器：没有任何能力，只接在 internal 网络上。
func (e *env) service(name, network, ip string, args ...string) {
	e.t.Helper()
	spec := dockerapi.ContainerSpec{
		Image:      e.image,
		Entrypoint: append([]string{fakenet}, args...),
		Labels:     map[string]string{e2eLabel: "1"},
		HostConfig: dockerapi.HostConfig{
			NetworkMode: network,
			Binds:       []string{e.bin + ":/opt/e2e:ro"},
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
		},
		NetworkingConfig: &dockerapi.NetworkingConfig{EndpointsConfig: map[string]dockerapi.EndpointSettings{
			network: {IPAMConfig: &dockerapi.EndpointIPAM{IPv4Address: ip}},
		}},
	}
	cname := cluster + "-svc-" + name
	if _, err := e.dk.ContainerCreate(e.ctx, cname, spec); err != nil {
		e.t.Fatalf("创建假服务 %s: %v", name, err)
	}
	if err := e.dk.ContainerStart(e.ctx, cname); err != nil {
		e.t.Fatalf("启动假服务 %s: %v", name, err)
	}
}

// cleanup 删除本测试创建的全部容器和网络。
func (e *env) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, sel := range []map[string]string{{"cc-cluster.cluster": cluster}, {e2eLabel: "1"}} {
		list, err := e.dk.ContainerList(ctx, sel)
		if err != nil {
			continue
		}
		for _, c := range list {
			_ = e.dk.ContainerRemove(ctx, c.ID)
		}
	}
	for _, n := range []string{netEgress, netRemote} {
		_ = e.dk.NetworkRemove(ctx, n)
	}
}

// w 在槽位的 worker 里运行 fakenet 客户端，返回它输出的那一行。
func (e *env) w(slot string, args ...string) string {
	e.t.Helper()
	return e.exec(e.eng.WorkerContainer(slot), append([]string{fakenet}, args...)...)
}

// wsh 在槽位的 worker 里运行一段 shell。
func (e *env) wsh(slot, script string) string {
	e.t.Helper()
	return e.exec(e.eng.WorkerContainer(slot), "/bin/sh", "-c", script)
}

// gw 在网关容器里运行一段 shell。
func (e *env) gw(script string) string {
	e.t.Helper()
	return e.exec(e.eng.GatewayContainer("main"), "/bin/sh", "-c", script)
}

func (e *env) exec(container string, cmd ...string) string {
	e.t.Helper()
	res, err := e.dk.Exec(e.ctx, container, cmd, dockerapi.ExecOptions{})
	if err != nil {
		e.t.Fatalf("在 %s 里执行 %v: %v", container, cmd, err)
	}
	return strings.TrimSpace(res.Stdout + res.Stderr)
}

func (e *env) logs(service string) string {
	e.t.Helper()
	out, errOut, err := e.dk.ContainerLogs(e.ctx, cluster+"-svc-"+service)
	must(e.t, err)
	return out + errOut
}

func (e *env) expect(name, pattern, got string) {
	e.t.Helper()
	if !regexp.MustCompile(pattern).MatchString(got) {
		e.t.Errorf("%s：得到 %q，期望匹配 /%s/", name, got, pattern)
	}
}

func (e *env) state(slot string) string {
	e.t.Helper()
	s, err := e.eng.SlotStatus(e.ctx, slot)
	must(e.t, err)
	return s.State
}

func (e *env) up(slot, wantExit string) {
	e.t.Helper()
	res, err := e.eng.SlotUp(e.ctx, slot, engine.UpOptions{})
	if err != nil {
		e.t.Fatalf("slot up %s: %v", slot, err)
	}
	if res.ExitIP != wantExit {
		e.t.Fatalf("槽位 %s 的出口是 %s，期望 %s", slot, res.ExitIP, wantExit)
	}
}

const (
	seenA = `^OK SEEN 198\.51\.100\.3$`
	seenB = `^OK SEEN 198\.51\.100\.4$`
)

func TestEndToEnd(t *testing.T) {
	e := setup(t)

	t.Run("启动两个槽", func(t *testing.T) {
		e.t = t
		e.up("a", ipLandA)
		e.up("b", ipLandB)
		if e.state("a") != engine.StateReady || e.state("b") != engine.StateReady {
			t.Fatalf("两个槽都应是 ready: a=%s b=%s", e.state("a"), e.state("b"))
		}
	})
	if t.Failed() {
		t.Fatal("槽位没有起来，后续检查没有意义")
	}

	identA, _, _ := e.st.Identity("a")

	t.Run("worker内看到的环境", func(t *testing.T) {
		e.t = t
		e.expect("网卡只有 lo 和 eth0，没有 TUN", `^eth0 lo$`, e.wsh("a", "ls /sys/class/net | tr '\\n' ' ' | sed 's/ $//'"))
		e.expect("环境里没有代理变量", `^0$`, e.wsh("a", "env | grep -ci proxy"))
		e.expect("resolv.conf 只有一行 nameserver，不带宿主的 search 域", `^nameserver 1\.1\.1\.1$`, e.wsh("a", "cat /etc/resolv.conf"))
		e.expect("默认路由指向本槽网关", `^default via 10\.213\.250\.1 dev eth0`, e.wsh("a", "ip route show default"))
		e.expect("MAC 是持久化的那一个", "^"+identA.MAC+"$", e.wsh("a", "cat /sys/class/net/eth0/address"))
		e.expect("hostname", "^"+identA.Hostname+"/"+identA.Hostname+"$", e.wsh("a", "echo $(hostname)/$(cat /etc/hostname)"))
		e.expect("machine-id", "^"+identA.MachineID+"/"+identA.MachineID+"$", e.wsh("a", "echo $(cat /etc/machine-id)/$(cat /var/lib/dbus/machine-id)"))
		e.expect("以项目用户运行", `^`+regexp.QuoteMeta(e.wsh("a", "echo $(id -u):$(id -g)"))+`$`, e.wsh("a", "stat -c %u:%g /home/kevin"))
		e.expect("用户名与 HOME 是该槽自己的", `^kevin /home/kevin /home/kevin$`, e.wsh("a", "echo $(whoami) $HOME $(pwd)"))
		e.expect("passwd 保留镜像的系统账号并加入该槽用户", `^root:x:0:0:.*\nkevin:x:\d+:\d+:Kevin:/home/kevin:/bin/sh$`, e.wsh("a", "grep -E '^(root|kevin):' /etc/passwd"))
		e.expect("登录 shell 的 PATH 里仍有 HOME 下的程序目录", `(^|:)/home/kevin/\.local/bin(:|$)`, e.exec(e.eng.WorkerContainer("a"), "/bin/sh", "-lc", "echo $PATH"))
		e.expect("另一个槽的用户名是随机挑的，不相同", `^ok$`, e.wsh("b", "[ \"$(whoami)\" != kevin ] && [ \"$HOME\" = \"/home/$(whoami)\" ] && echo ok"))
		e.expect("没有任何能力", `^CapEff:\s+0000000000000000$`, e.wsh("a", "grep CapEff /proc/self/status"))
		e.expect("系统目录写不动", `^denied denied$`, e.wsh("a", "for d in /etc /usr; do touch $d/.probe 2>/dev/null && echo wrote || echo denied; done | tr '\\n' ' ' | sed 's/ $//'"))
		e.expect("HOME 可写", `^ok$`, e.wsh("a", "touch /home/kevin/.probe && echo ok"))
		e.expect("/.dockerenv 已删除", `^gone$`, e.wsh("a", "test -e /.dockerenv && echo present || echo gone"))
		e.expect("cgroup 路径里没有容器 ID", `^0$`, e.wsh("a", "grep -c docker /proc/self/cgroup"))
		e.expect("身份文件不是挂载进来的", `^0$`, e.wsh("a", "grep -cE ' /etc/(passwd|group|machine-id) ' /proc/self/mountinfo"))
		e.expect("主机名解析到 127.0.1.1", `^127\.0\.1\.1\s+`+identA.Hostname+`$`, e.wsh("a", "grep 127.0.1.1 /etc/hosts"))
		e.expect("落地设置了时区的，沿用它而不是探测结果", `^Asia/Tokyo$`, e.wsh("a", "echo $TZ"))
		e.expect("落地没有设置时区的，首次启动时按出口探测结果设置", `^Europe/Berlin$`, e.wsh("b", "echo $TZ"))
		if lb, err := e.st.Node("lb"); err != nil || lb.TZ != "Europe/Berlin" || !lb.TZAuto {
			t.Errorf("探测到的时区应记在落地上，之后不再变动: %+v %v", lb, err)
		}
		e.expect("槽 a 的 DMI 是该槽身份里的机型", "^"+regexp.QuoteMeta(identA.DMI["sys_vendor"]+"/"+identA.DMI["product_name"])+"$", e.wsh("a", "echo \"$(cat /sys/class/dmi/id/sys_vendor)/$(cat /sys/class/dmi/id/product_name)\""))
		e.expect("槽 a 的 DMI 不含只有 root 可读的序列号", `^0$`, e.wsh("a", "ls /sys/class/dmi/id | grep -cE 'serial|uuid'"))
		e.expect("槽 b 的 DMI 信息被遮蔽", `^0$`, e.wsh("b", "ls /sys/devices/virtual/dmi/id 2>/dev/null | wc -l"))
		e.expect("槽 a 的 cpuinfo 与 meminfo 按限额显示", `^2 1048576$`, e.wsh("a", "echo $(grep -c ^processor /proc/cpuinfo) $(awk '/^MemTotal:/{print $2}' /proc/meminfo)"))
		e.expect("槽 a 被绑到前 2 个核，/sys 里的核范围一致", `^0-1 0-1 0-1 0-1$`, e.wsh("a", "echo $(cat /sys/devices/system/cpu/online) $(cat /sys/devices/system/cpu/possible) $(cat /sys/devices/system/cpu/present) $(awk '/^Cpus_allowed_list:/{print $2}' /proc/self/status)"))
		e.expect("worker 自己不能改路由", `not permitted`, e.wsh("a", "ip route add 192.0.2.0/24 dev eth0 2>&1"))
	})

	t.Run("透明转发与按来源分流", func(t *testing.T) {
		e.t = t
		e.expect("A 出口为落地 A", seenA, e.w("a", "get", ipTarget, "80", "sniff-a.example"))
		e.expect("B 出口为落地 B", seenB, e.w("b", "get", ipTarget, "80", "sniff-b.example"))
		e.expect("A 非嗅探端口 8080", seenA, e.w("a", "get", ipTarget, "8080"))
		// 槽内的 A 查询由核心答一个映射地址（100.64.0.0/10），不向外解析；连接时域名交给落地。
		const mapped = `^OK 100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.`
		e.expect("A 系统解析得到映射地址（经 TUN 接管）", mapped, e.w("a", "resolve", "bar.example"))
		e.expect("A 直接问 8.8.8.8 也被接管", mapped, e.w("a", "dnsq", "8.8.8.8", "qux.example"))
		e.expect("A 按域名访问：出口为落地 A", seenA, e.w("a", "get", "byname.example", "80"))
		e.expect("按域名访问时域名交给落地解析", `REQ domain byname\.example:80`, e.logs("land-a"))
		if regexp.MustCompile(`DNSQ from \S+ qtype 1\b`).MatchString(e.logs("target")) {
			t.Errorf("槽内的 A 查询不应到达外部的 DNS 服务器：\n%s", e.logs("target"))
		}
		e.w("a", "dnsq", "8.8.8.8", "txt.example", "16")
		e.expect("映射答不了的查询（TXT）经中转到达 DNS", `DNSQ from 198\.51\.100\.2 qtype 16`, e.logs("target"))
		e.expect("嗅探到的域名交给落地", `REQ domain sniff-a\.example:80`, e.logs("land-a"))
		e.expect("A 业务 UDP 被拒", `^ERR`, e.w("a", "udp", ipTarget, "9999"))
		e.expect("A→B 跨槽不可达", `^ERR`, e.w("a", "get", "10.213.250.6", "80"))
		e.expect("A→网关地址上无开放端口", `^ERR ECONNREFUSED$`, e.w("a", "get", "10.213.250.1", "17000"))
		e.expect("A→首跳地址不被直接转发", `^ERR`, e.w("a", "get", ipTransitEgress, "1080"))
		e.expect("A→私网目的被拒", `^ERR`, e.w("a", "get", "192.168.1.1", "80"))

		gwIP := e.gw("ip -o -4 addr show eth0 | awk '{print $4}' | cut -d/ -f1")
		peers := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^PEER (\S+)`).FindAllStringSubmatch(e.logs("transit"), -1) {
			peers[m[1]] = true
		}
		if len(peers) != 1 || !peers[gwIP] {
			t.Errorf("中转只应见过网关（%s）一个来源，实际 %v", gwIP, peers)
		}
		if strings.Contains(e.logs("land-b"), "sniff-a.example") || strings.Contains(e.logs("land-a"), "sniff-b.example") {
			t.Error("出现了串线：一个槽的请求出现在另一个槽的落地上")
		}
	})

	t.Run("网关自身的出站限制", func(t *testing.T) {
		e.t = t
		e.expect("主路由表没有默认路由", `^0$`, e.gw("ip route show default | wc -l"))
		e.expect("出口网卡上只有首跳一条路由", `^203\.0\.113\.3 dev eth0 scope link\s*$`, e.gw("ip route show | grep eth0"))
		e.expect("网关直连目标不可达", `unreachable|Couldn't connect|Failed to connect`, e.gw("curl -sS -m 3 http://"+ipTarget+"/ 2>&1; true"))
		e.expect("网关直连出口网段其它地址不可达", `unreachable|Couldn't connect|Failed to connect`, e.gw("curl -sS -m 3 http://203.0.113.1/ 2>&1; true"))
		e.expect("控制接口只在 Unix socket 上", `^0$`, e.gw("netstat -lnt 2>/dev/null | grep -c ':9090' ; true"))
		// 127.0.0.11 是 Docker 给自定义网络配的内置解析器，同样只在回环网段上。
		e.expect("TCP 监听只在回环网段上", `^$`, e.gw("netstat -lnt 2>/dev/null | awk 'NR>2 && $4 !~ /^127\\./'"))
	})

	t.Run("出口与绑定不符时不接线且重载不影响别的槽", func(t *testing.T) {
		e.t = t
		// 槽 B 开一条 6 秒的长连接；期间登记槽 C 并尝试启动，这会让核心在线重载配置。
		stream := make(chan string, 1)
		go func() {
			res, err := e.dk.Exec(e.ctx, e.eng.WorkerContainer("b"), []string{fakenet, "stream", ipTarget, "8081"}, dockerapi.ExecOptions{})
			if err != nil {
				stream <- "ERR " + err.Error()
				return
			}
			stream <- strings.TrimSpace(res.Stdout)
		}()
		time.Sleep(1500 * time.Millisecond)

		e.addSlot("c", "lwrong")
		_, err := e.eng.SlotUp(e.ctx, "c", engine.UpOptions{})
		if err == nil || !strings.Contains(err.Error(), "不符") {
			t.Fatalf("出口与绑定不符时 slot up 应失败: %v", err)
		}
		if got := e.state("c"); got != engine.StateAbsent {
			t.Errorf("链路探测不过时不应创建 worker，状态是 %s", got)
		}
		e.expect("核心已加载新配置（槽 C 的探测入口在）", `^1$`, e.gw("netstat -lnt | grep -c '127.0.0.1:17002'"))
		e.expect("重载期间 B 的长连接一拍没丢", `^TICKS 12 closed$`, <-stream)
		e.expect("重载后 B 仍从落地 B 出", seenB, e.w("b", "get", ipTarget, "80"))
		e.expect("重载后 A 仍从落地 A 出", seenA, e.w("a", "get", ipTarget, "80"))

		must(t, e.eng.SlotRemove(e.ctx, "c", true))
		e.expect("删除槽 C 后它的探测入口消失", `^0$`, e.gw("netstat -lnt | grep -c '127.0.0.1:17002'; true"))
	})

	t.Run("链路不通时指出是哪一段", func(t *testing.T) {
		e.t = t
		// 中转可用，落地连不上：分段检查应当直接指出是中转到落地这一段。
		e.addSlot("d", "ldead")
		_, err := e.eng.SlotUp(e.ctx, "d", engine.UpOptions{})
		if err == nil || !strings.Contains(err.Error(), "中转 transit 本身可用，但经它连不上落地 ldead") {
			t.Errorf("应指出中转可用而落地连不上: %v", err)
		}
		if got := e.state("d"); got != engine.StateAbsent {
			t.Errorf("链路不通时不应创建 worker，状态是 %s", got)
		}
		must(t, e.eng.SlotRemove(e.ctx, "d", true))

		// 同一个落地同时接线的槽位数有上限：槽 A 已经接着落地 la。
		e.eng.LandingLimit = 1
		e.addSlot("d", "la")
		_, err = e.eng.SlotUp(e.ctx, "d", engine.UpOptions{})
		if err == nil || !strings.Contains(err.Error(), "最多同时给 1 个槽位用") {
			t.Errorf("落地接线的槽位数到上限时应拒绝: %v", err)
		}
		e.eng.LandingLimit = 0
		must(t, e.eng.SlotRemove(e.ctx, "d", true))
		e.expect("A 不受影响", seenA, e.w("a", "get", ipTarget, "80"))
	})

	t.Run("候选中转", func(t *testing.T) {
		e.t = t
		active := func() string {
			sts, err := e.eng.GatewayStatuses(e.ctx)
			must(t, err)
			for _, s := range sts {
				if s.Gateway.ID == "main" {
					return s.ActiveTransit
				}
			}
			return ""
		}
		// 候选接不通落地（这里是出口网段里一个没人应答的地址）：验证不过，不更换；明确跳过验证才换。
		must(t, e.eng.AddNode(e.ctx, model.Node{ID: "tdead", Role: model.RoleTransit, Proxy: map[string]any{"type": "socks5", "server": "203.0.113.13", "port": 1080}}))
		if _, err := e.eng.SetGatewayTransits(e.ctx, "main", []string{"transit", "tdead"}, engine.TransitOptions{}); err == nil || !strings.Contains(err.Error(), "tdead 接不通落地") {
			t.Fatalf("接不通落地的候选应被拒绝并指名: %v", err)
		}
		if g, err := e.st.Gateway("main"); err != nil || len(g.Backups) != 0 {
			t.Fatalf("验证不过时网关不应改动: %+v %v", g, err)
		}
		if a := e.state("a"); a != engine.StateReady {
			t.Fatalf("验证不过不应影响已就绪的槽位: %s", a)
		}
		res, err := e.eng.SetGatewayTransits(e.ctx, "main", []string{"transit", "tdead"}, engine.TransitOptions{SkipChainCheck: true})
		must(t, err)
		if len(res.Failed) != 0 {
			t.Fatalf("跳过验证后应能更换，主用的仍可用: %+v", res)
		}

		// 在线加上一个候选：先验证每个中转都能接通两个槽位的落地（主用的中转是域名 + 固定地址），
		// 已就绪的槽位重新核对后仍然就绪，出口不变。
		res, err = e.eng.SetGatewayTransits(e.ctx, "main", []string{"transit", "transit2"}, engine.TransitOptions{})
		must(t, err)
		must(t, e.eng.RemoveNode("tdead"))
		if !res.Reloaded || len(res.Failed) != 0 || len(res.Rechecked) < 2 {
			t.Fatalf("加候选中转后的核对结果: %+v", res)
		}
		if a, b := e.state("a"), e.state("b"); a != engine.StateReady || b != engine.StateReady {
			t.Fatalf("加候选中转后槽位应仍就绪: a=%s b=%s", a, b)
		}
		e.expect("A 经主用中转，出口仍是落地 A", seenA, e.w("a", "get", ipTarget, "80"))
		if got := active(); got != "transit" {
			t.Errorf("主用的可用时应在用它，得到 %q", got)
		}

		// 主用的中转停掉：核心自己改用候选的，出口仍是各自的落地。
		must(t, e.dk.ContainerStop(e.ctx, cluster+"-svc-transit", 0))
		deadline := time.Now().Add(150 * time.Second)
		got := ""
		for time.Now().Before(deadline) {
			if got = e.w("a", "get", ipTarget, "80"); regexp.MustCompile(seenA).MatchString(got) {
				break
			}
			time.Sleep(3 * time.Second)
		}
		e.expect("主用中转停掉后 A 经候选恢复，出口仍是落地 A", seenA, got)
		e.expect("B 同样恢复，出口仍是落地 B", seenB, e.w("b", "get", ipTarget, "80"))
		if got := active(); got != "transit2" {
			t.Errorf("主用的不通时应在用候选，得到 %q", got)
		}
		if a := e.state("a"); a != engine.StateReady {
			t.Errorf("自动改用候选不应让槽位退出就绪: %s", a)
		}

		// 恢复主用的，并换回只有一个中转：后面的测试照旧。
		must(t, e.dk.ContainerStart(e.ctx, cluster+"-svc-transit"))
		res, err = e.eng.SetGatewayTransits(e.ctx, "main", []string{"transit"}, engine.TransitOptions{})
		must(t, err)
		if len(res.Failed) != 0 {
			t.Fatalf("换回单个中转后的核对结果: %+v", res)
		}
		e.expect("换回后 A 正常", seenA, e.w("a", "get", ipTarget, "80"))
		if _, err := e.eng.SetGatewayTransits(e.ctx, "main", []string{"transit", "la"}, engine.TransitOptions{}); err == nil {
			t.Error("落地不能当候选中转")
		}
		if err := e.eng.RemoveNode("transit2"); err != nil {
			t.Errorf("不再被引用的候选中转应能删除: %v", err)
		}
	})

	t.Run("持续检测", func(t *testing.T) {
		e.t = t
		watch := func() map[string]model.Watch {
			res, err := e.eng.WatchOnce(e.ctx)
			must(t, err)
			out := map[string]model.Watch{}
			for _, w := range res {
				out[w.Slot] = w
			}
			return out
		}
		if w := watch(); w["a"].Status != engine.WatchOK || w["b"].Status != engine.WatchOK || w["a"].ExitIP != ipLandA {
			t.Fatalf("两个槽都正常时的检测结果: %+v", w)
		}

		// 落地连不上：只记下来，不断网保持；落地回来后自己恢复。
		must(t, e.dk.ContainerStop(e.ctx, cluster+"-svc-land-b", 0))
		if w := watch(); w["b"].Status != engine.WatchUnreachable || w["a"].Status != engine.WatchOK {
			t.Errorf("落地 B 停掉后的检测结果: %+v", w)
		}
		e.expect("落地停掉时 B 没有网络（不会改从中转出去）", `^ERR`, e.w("b", "get", ipTarget, "80"))
		if got := e.state("b"); got != engine.StateReady {
			t.Errorf("连不上不应让槽位被断网保持，状态是 %s", got)
		}
		must(t, e.dk.ContainerStart(e.ctx, cluster+"-svc-land-b"))
		time.Sleep(time.Second)
		if w := watch(); w["b"].Status != engine.WatchOK {
			t.Errorf("落地 B 回来后的检测结果: %+v", w["b"])
		}
		e.expect("落地回来后 B 自己恢复", seenB, e.w("b", "get", ipTarget, "80"))

		// 出口变了：把记录里“接线时核对过的出口”改成另一个地址，等于现在看到的出口与当时不同。
		applied, ok, err := e.st.Applied("a")
		must(t, err)
		if !ok {
			t.Fatal("槽 a 应有接线记录")
		}
		applied.ExitIP = "198.51.100.77"
		must(t, e.st.SaveApplied("a", applied))
		if w := watch(); w["a"].Status != engine.WatchChanged || w["b"].Status != engine.WatchOK {
			t.Errorf("出口变了时的检测结果: %+v", w)
		}
		s, err := e.eng.SlotStatus(e.ctx, "a")
		must(t, err)
		if s.State != engine.StateHold || !strings.Contains(s.HoldReason, "持续检测") || !strings.Contains(s.HoldReason, ipLandA) {
			t.Errorf("出口变了应自动断网保持并写明原因: %s %q", s.State, s.HoldReason)
		}
		e.expect("断网保持后 A 没有网络", `^ERR`, e.w("a", "get", ipTarget, "80"))
		e.expect("B 不受影响", seenB, e.w("b", "get", ipTarget, "80"))
		if w := watch(); len(w) != 1 || w["b"].Status != engine.WatchOK {
			t.Errorf("被断网保持的槽位不再检测: %+v", w)
		}
		must(t, e.eng.SlotRelease("a"))
		e.up("a", ipLandA)
		if s, _ := e.eng.SlotStatus(e.ctx, "a"); s.Watch != nil {
			t.Errorf("重新接线后应清掉之前的检测结果: %+v", s.Watch)
		}
	})

	t.Run("核心被杀", func(t *testing.T) {
		e.t = t
		stream := make(chan string, 1)
		go func() {
			res, _ := e.dk.Exec(e.ctx, e.eng.WorkerContainer("a"), []string{fakenet, "stream", ipTarget, "8081"}, dockerapi.ExecOptions{})
			stream <- strings.TrimSpace(res.Stdout)
		}()
		time.Sleep(1500 * time.Millisecond)
		// 先让监督进程暂停，否则它 1 秒后就把核心拉起来了，来不及观察"核心不在"时的状态。
		// 监督进程是容器 init 的唯一子进程。
		e.gw("kill -STOP $(pgrep -P 1 | head -1) && pkill mihomo")
		time.Sleep(300 * time.Millisecond)
		e.expect("TUN 消失且表 100 为空", `^nodev/$`, e.gw("(ip link show cc0 >/dev/null 2>&1 && echo dev || echo nodev) | tr -d '\\n'; echo /$(ip route show table 100 2>/dev/null)"))
		e.expect("A 新连接不可达", `^ERR E(NET|HOST)UNREACH$`, e.w("a", "get", ipTarget, "80"))
		e.expect("B 新连接不可达", `^ERR E(NET|HOST)UNREACH$`, e.w("b", "get", ipTarget, "80"))
		e.expect("A 的既有长连接中断", `^TICKS ([0-9]|1[01]) `, <-stream)

		// 不经管理器：监督进程自己把核心拉起来并恢复路由。
		e.gw("kill -CONT $(pgrep -P 1 | head -1)")
		deadline := time.Now().Add(30 * time.Second)
		var got string
		for time.Now().Before(deadline) {
			if got = e.w("a", "get", ipTarget, "80"); regexp.MustCompile(seenA).MatchString(got) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		e.expect("监督进程重启核心后 A 自动恢复", seenA, got)
		e.expect("B 也恢复", seenB, e.w("b", "get", ipTarget, "80"))
	})

	t.Run("手动断网", func(t *testing.T) {
		e.t = t
		must(t, e.eng.SlotHold(e.ctx, "a", "e2e"))
		e.expect("A 断开后只剩 lo", `^lo$`, e.wsh("a", "ls /sys/class/net | tr '\\n' ' ' | sed 's/ $//'"))
		e.expect("A 无网", `^ERR ENETUNREACH$`, e.w("a", "get", ipTarget, "80"))
		e.expect("B 不受影响", seenB, e.w("b", "get", ipTarget, "80"))
		if got := e.state("a"); got != engine.StateHold {
			t.Errorf("状态应为 hold，得到 %s", got)
		}
		if _, err := e.eng.SlotUp(e.ctx, "a", engine.UpOptions{}); err == nil || !strings.Contains(err.Error(), "手动断网") {
			t.Fatalf("hold 期间 slot up 必须被拒绝: %v", err)
		}
		e.expect("被拒绝后仍然只有 lo", `^lo$`, e.wsh("a", "ls /sys/class/net | tr '\\n' ' ' | sed 's/ $//'"))
		must(t, e.eng.SlotRelease("a"))
		e.up("a", ipLandA)
		e.expect("解除后 A 恢复", seenA, e.w("a", "get", ipTarget, "80"))
	})

	t.Run("worker重启后没网直到重新接线", func(t *testing.T) {
		e.t = t
		name := e.eng.WorkerContainer("a")
		must(t, e.dk.ContainerStop(e.ctx, name, time.Second))
		must(t, e.dk.ContainerStart(e.ctx, name))
		e.expect("重启后只有 lo", `^lo$`, e.wsh("a", "ls /sys/class/net | tr '\\n' ' ' | sed 's/ $//'"))
		e.expect("重启后无网", `^ERR ENETUNREACH$`, e.w("a", "get", ipTarget, "80"))
		e.expect("网关侧的 veth 随之消失", `^gone$`, e.gw("ip link show cs0 >/dev/null 2>&1 && echo present || echo gone"))
		if got := e.state("a"); got != engine.StateOffline {
			t.Errorf("状态应为 offline，得到 %s", got)
		}
		e.up("a", ipLandA)
		e.expect("重新接线后 MAC 不变", "^"+identA.MAC+"$", e.wsh("a", "cat /sys/class/net/eth0/address"))
		e.expect("machine-id 不变", "^"+identA.MachineID+"$", e.wsh("a", "cat /etc/machine-id"))
		e.expect("HOME 里的文件还在", `^ok$`, e.wsh("a", "test -f /home/kevin/.probe && echo ok"))
		e.expect("重启后 /.dockerenv 没有回来，resolv.conf 仍只有一行", `^gone nameserver 1\.1\.1\.1$`, e.wsh("a", "echo $(test -e /.dockerenv && echo present || echo gone) $(cat /etc/resolv.conf)"))
	})

	t.Run("网关重建后逐槽恢复", func(t *testing.T) {
		e.t = t
		if err := e.eng.GatewayDown(e.ctx, "main", false); err == nil {
			t.Fatal("还有运行中的槽位时，不带 force 的 gateway down 必须被拒绝")
		}
		must(t, e.eng.GatewayDown(e.ctx, "main", true))
		e.expect("网关没了，A 只剩 lo", `^lo$`, e.wsh("a", "ls /sys/class/net | tr '\\n' ' ' | sed 's/ $//'"))
		e.expect("网关没了，B 无网", `^ERR ENETUNREACH$`, e.w("b", "get", ipTarget, "80"))
		workerBefore, _ := e.dk.ContainerInspect(e.ctx, e.eng.WorkerContainer("a"))

		e.up("a", ipLandA)
		e.expect("A 接回后出口正确", seenA, e.w("a", "get", ipTarget, "80"))
		e.expect("B 还没接线，仍无网", `^ERR ENETUNREACH$`, e.w("b", "get", ipTarget, "80"))
		if got := e.state("b"); got != engine.StateOffline {
			t.Errorf("B 的状态应为 offline，得到 %s", got)
		}
		e.up("b", ipLandB)
		e.expect("B 接回后出口正确", seenB, e.w("b", "get", ipTarget, "80"))
		workerAfter, _ := e.dk.ContainerInspect(e.ctx, e.eng.WorkerContainer("a"))
		if workerBefore.ID != workerAfter.ID || workerBefore.State.StartedAt != workerAfter.State.StartedAt {
			t.Error("重建网关不应重建或重启 worker")
		}
	})

	t.Run("在项目目录里进入槽位", func(t *testing.T) {
		e.t = t
		proj := filepath.Join(filepath.Dir(e.st.Dir()), "proj-b")
		other := filepath.Join(filepath.Dir(e.st.Dir()), "proj-c")
		must(t, os.MkdirAll(filepath.Join(proj, "log"), 0o755))
		must(t, os.MkdirAll(other, 0o755))
		must(t, os.WriteFile(filepath.Join(proj, "log", "note.txt"), []byte("from-host\n"), 0o644))
		worker := e.eng.WorkerContainer("b")
		containerID := func() string {
			c, err := e.dk.ContainerInspect(e.ctx, worker)
			must(t, err)
			return c.ID
		}
		// 以非交互方式运行命令行：与用户在终端里敲的是同一条路径，只是命令的输出被收集回来。
		enter := func(args ...string) (string, int) {
			var out, errOut strings.Builder
			code := cli.Run(append([]string{"--data-dir", e.st.Dir(), "enter", "b"}, args...), strings.NewReader(""), &out, &errOut)
			return strings.TrimSpace(out.String()) + "|" + strings.TrimSpace(errOut.String()), code
		}

		before := containerID()
		got, code := enter(proj, "--", "pwd")
		if code == 0 || !strings.Contains(got, "--yes") {
			t.Fatalf("目录不在挂载范围内、又没有确认时，应拒绝并提示 --yes: %d %s", code, got)
		}
		if containerID() != before {
			t.Fatal("被拒绝时不应动 worker 容器")
		}

		got, code = enter(proj, "--yes", "--", "pwd")
		e.expect("新目录以相同的路径挂入并进入", "^"+regexp.QuoteMeta(proj)+`\|`, got)
		if code != 0 || containerID() == before {
			t.Fatalf("挂载新目录应重建 worker: code=%d", code)
		}
		e.expect("重建后仍从自己的落地出去", seenB, e.w("b", "get", ipTarget, "80"))
		e.expect("重建后身份文件重新写入，/.dockerenv 仍被删除", `^gone$`, e.wsh("b", "test -e /.dockerenv && echo present || echo gone"))

		after := containerID()
		got, code = enter(filepath.Join(proj, "log"), "--", "cat", "note.txt")
		e.expect("已挂载目录的子目录直接进入，工作目录就是它", `^from-host\|$`, got)
		if code != 0 || containerID() != after {
			t.Fatalf("子目录不应触发重建: code=%d", code)
		}

		// 挂上级目录后，原先单独挂的子目录并进去；它下面的其他项目也不用再重建。
		got, code = enter(other, "--mount", filepath.Dir(proj), "--yes", "--", "pwd")
		e.expect("用 --mount 指定挂上级目录", "^"+regexp.QuoteMeta(other)+`\|`, got)
		if code != 0 {
			t.Fatalf("挂上级目录失败: %s", got)
		}
		sl, err := e.st.Slot("b")
		must(t, err)
		if _, ok := model.ContainerPath(sl.Mounts, proj); !ok || len(sl.Mounts) != 2 {
			t.Fatalf("上级目录应覆盖原先的子目录挂载，只剩它和测试程序目录: %+v", sl.Mounts)
		}
		if e.state("b") != engine.StateReady {
			t.Fatalf("进入之后槽位应是 ready: %s", e.state("b"))
		}
	})

	t.Run("删除槽位保留HOME与身份", func(t *testing.T) {
		e.t = t
		must(t, e.eng.SlotRemove(e.ctx, "b", false))
		if _, err := e.dk.ContainerInspect(e.ctx, e.eng.WorkerContainer("b")); !dockerapi.IsNotFound(err) {
			t.Errorf("worker 容器应已删除: %v", err)
		}
		cfg, err := e.eng.Render("main", true)
		must(t, err)
		if strings.Contains(string(cfg), "land-lb") {
			t.Error("删除槽位后，网关配置里不应再有它的落地")
		}
		e.expect("网关里槽 B 的策略路由已删除", `^0$`, e.gw("ip rule | grep -c cs1; true"))
		if _, ok, _ := e.st.Identity("b"); !ok {
			t.Error("不带 purge 删除时身份应保留")
		}
		e.expect("A 不受影响", seenA, e.w("a", "get", ipTarget, "80"))
	})

	t.Run("订阅节点测延迟", func(t *testing.T) {
		socks5 := func(server string) map[string]any {
			return map[string]any{"type": "socks5", "server": server, "port": 1080}
		}
		must(t, e.st.SaveSub(model.Subscription{ID: "s", URL: "https://sub.invalid/x", Proxies: []model.SubProxy{
			{Name: "剩余流量：1 GB", Proxy: socks5(ipTransitEgress)},
			{Name: "不通的节点", Proxy: socks5("203.0.113.99")},
			{Name: "能用的节点", Proxy: socks5(ipTransitEgress)},
		}}, true))
		delays, err := e.eng.SubTest(e.ctx, "s", engine.DelayOptions{
			URL: "http://" + ipTarget + "/", Timeout: 4 * time.Second, Network: netEgress,
		})
		must(t, err)
		byName := map[string]engine.NodeDelay{}
		for _, d := range delays {
			byName[d.Name] = d
		}
		if d := byName["能用的节点"]; !d.Usable() {
			t.Errorf("经假中转能到目标，应测通: %+v", d)
		}
		if d := byName["不通的节点"]; d.DelayMS != model.DelayFailed || delays[len(delays)-1].Name != "不通的节点" {
			t.Errorf("连不上的节点应记为不通并排在最后: %+v", delays)
		}
		if best, ok := engine.Fastest(delays); !ok || best.Name != "能用的节点" {
			t.Errorf("自动挑选应跳过提示项，选中能用的节点: %+v %v", best, ok)
		}
		if s, err := e.st.Sub("s"); err != nil || s.TestedAt.IsZero() || s.Proxies[2].DelayMS <= 0 || s.Proxies[1].DelayMS != model.DelayFailed {
			t.Errorf("测速结果应记在订阅上: %+v %v", s.Proxies, err)
		}
		// 接上落地测整条链路：落地连得上的测通，连不上的全部不通。
		chain, err := e.eng.SubTest(e.ctx, "s", engine.DelayOptions{
			URL: "http://" + ipTarget + "/", Timeout: 4 * time.Second, Network: netEgress, Landing: "la",
		})
		must(t, err)
		if best, ok := engine.Fastest(chain); !ok || best.Name != "能用的节点" {
			t.Errorf("经假中转再到落地 la 应测通: %+v", chain)
		}
		chain, err = e.eng.SubTest(e.ctx, "s", engine.DelayOptions{
			URL: "http://" + ipTarget + "/", Timeout: 4 * time.Second, Network: netEgress, Landing: "ldead",
		})
		must(t, err)
		if best, ok := engine.Fastest(chain); ok {
			t.Errorf("落地连不上时整条链路都不通，却选出了 %+v", best)
		}
		if s, err := e.st.Sub("s"); err != nil || s.Proxies[2].DelayMS <= 0 {
			t.Errorf("链路测速的结果不应覆盖节点自身的延迟记录: %+v %v", s.Proxies, err)
		}
		// 测速用的一次性容器不能留下：里面有节点凭据。
		left, err := e.dk.ContainerList(e.ctx, map[string]string{"cc-cluster.cluster": cluster, "cc-cluster.role": "helper"})
		must(t, err)
		if len(left) != 0 {
			t.Errorf("测速后不应留下助手容器: %+v", left)
		}
	})

	t.Run("直出", func(t *testing.T) {
		e.t = t
		// 不用落地：订阅里的节点直接当出口。测试里的“节点”是假中转，目标看到的来源是它在远端网络的地址。
		// 直出的网关由管理器按默认值创建；这里先建好同名的，让它用测试的网络与探测地址。
		must(t, e.eng.AddGateway(model.Gateway{
			ID: engine.DirectGatewayID, EgressNetwork: netEgress, Pool: "10.213.249.0/24",
			DNS: []string{"tcp://" + ipTarget + ":53"}, EchoURLs: []string{"http://" + ipTarget + "/json"},
			ConnectURLs: []string{"http://" + ipTarget + "/"},
		}))
		const seenDirect = `^OK SEEN 198\.51\.100\.2$`
		for _, id := range []string{"d1", "d2"} {
			gw, exit, err := e.eng.PrepareDirect(e.ctx, id, "s", "能用的节点")
			must(t, err)
			if gw != engine.DirectGatewayID || exit != id+"-exit" {
				t.Fatalf("直出的网关与出口节点: %s %s", gw, exit)
			}
			_, _, err = e.eng.AddSlot(e.ctx, model.Slot{
				ID: id, Gateway: gw, Landing: exit, Shell: "/bin/sh", UID: os.Getuid(), GID: os.Getgid(),
				Mounts: []model.Mount{{Source: e.bin, Target: "/opt/e2e", ReadOnly: true}},
			}, engine.SlotOptions{})
			must(t, err)
			e.up(id, ipTransitRemote)
		}
		// 不带中转的网关接了两个槽，各自从自己固定的节点出去。
		e.expect("d1 从它的节点出去", seenDirect, e.w("d1", "get", ipTarget, "80"))
		e.expect("d2 从它的节点出去", seenDirect, e.w("d2", "get", ipTarget, "80"))
		e.expect("A 不受影响", seenA, e.w("a", "get", ipTarget, "80"))
		if n, err := e.st.Node("d1-exit"); err != nil || !n.Direct || n.Role != model.RoleLanding || n.Source == nil {
			t.Errorf("直出的出口节点应标记为直出并记下来源: %+v %v", n, err)
		}
		if _, _, err := e.eng.PrepareDirect(e.ctx, "d3", "s", "没有这个节点"); err == nil {
			t.Error("订阅里没有的节点不能直出")
		}
		// 删除槽位时，它专用的出口节点一起删掉。
		for _, id := range []string{"d1", "d2"} {
			must(t, e.eng.SlotRemove(e.ctx, id, true))
			if _, err := e.st.Node(id + "-exit"); err == nil {
				t.Errorf("槽位 %s 删除后，它的出口节点应一起删掉", id)
			}
		}
		must(t, e.eng.GatewayDown(e.ctx, engine.DirectGatewayID, false))
		must(t, e.eng.RemoveGateway(e.ctx, engine.DirectGatewayID))
	})

	t.Run("宿主tmux桥接", func(t *testing.T) {
		tmuxBin, err := exec.LookPath("tmux")
		if err != nil {
			t.Skip("宿主的 PATH 里没有 tmux")
		}
		wimg := os.Getenv("CC_E2E_WORKER_IMAGE")
		if wimg == "" {
			wimg = model.DefaultWorkerImage
		}
		if has, err := e.dk.ImageExists(e.ctx, wimg); err != nil || !has {
			t.Skipf("本地没有 worker 镜像 %s；先执行 make image-worker", wimg)
		}
		base := filepath.Dir(e.bin)
		home, proj := filepath.Join(base, "tmux-home"), filepath.Join(base, "bridge-proj")
		must(t, os.RemoveAll(proj))
		must(t, os.MkdirAll(home, 0o700))
		must(t, os.MkdirAll(proj, 0o755))
		// 宿主的 tmux 不读用户自己的配置，HOME 也指到测试目录里。
		e.eng.Tmux = engine.TmuxHost{Bin: tmuxBin, Conf: "/dev/null", Env: []string{"HOME=" + home, "SHELL=/bin/bash"}}
		t.Cleanup(func() {
			_ = e.eng.TmuxKill(e.ctx, "t")
			_ = e.eng.SlotRemove(e.ctx, "t", true)
		})

		_, _, err = e.eng.AddSlot(e.ctx, model.Slot{
			ID: "t", Gateway: "main", Landing: "la", Shell: "/bin/sh", UID: os.Getuid(), GID: os.Getgid(),
			Image: wimg, TmuxBridge: true, TmuxShell: "bash",
			Mounts: []model.Mount{{Source: proj, Target: proj}},
		}, engine.SlotOptions{})
		must(t, err)
		res, err := e.eng.SlotUp(e.ctx, "t", engine.UpOptions{})
		must(t, err)
		worker := e.eng.WorkerContainer("t")
		if !res.Bridge {
			if out := e.exec(worker, "/bin/sh", "-c", "command -v tmux-mcp-rs tmux || echo missing"); strings.Contains(out, "missing") {
				t.Skipf("worker 镜像 %s 里没有 tmux 或 tmux-mcp-rs；先执行 make image-worker 重建", wimg)
			}
			t.Fatal("桥接没有接通")
		}
		if res.ExitIP != ipLandA {
			t.Errorf("开启桥接不应影响槽位的出口：%s", res.ExitIP)
		}
		t.Logf("宿主 %s；worker 里 %s", e.hostTmuxVersion(tmuxBin), e.exec(worker, "tmux", "-V"))

		// 槽内的 socket 路径与宿主相同。
		sock := e.eng.TmuxSocket("t")
		list := fmt.Sprintf("tmux -S %s list-sessions -F '#{session_name}'", sock)
		e.expect("槽内看到宿主的 session", `^main$`, e.exec(worker, "/bin/sh", "-c", list))
		// 两个配置文件属于容器内的 root，槽内的用户改不了工具范围。
		e.expect("配置文件的属主与权限", `^root 644$`, e.exec(worker, "stat", "-c", "%U %a", "/etc/tmux-mcp/config.toml"))
		e.expect("槽内不能改写工具范围", `denied`, e.exec(worker, "/bin/sh", "-c", "echo x >> /etc/tmux-mcp/config.toml 2>&1 || echo denied"))

		// 用一个写死调用内容的 MCP 客户端走一轮（不经过任何模型）：列出工具、列出 session、
		// 试一个不在允许范围内的 socket、在宿主的 pane 里执行 hostname 并取回结果。
		client, err := os.ReadFile("mcpclient.js")
		must(t, err)
		must(t, os.WriteFile(filepath.Join(proj, "mcpclient.js"), client, 0o644))
		paneOut, err := exec.Command(tmuxBin, "-S", sock, "list-panes", "-a", "-F", "#{pane_id}").Output()
		must(t, err)
		pane := strings.TrimSpace(strings.Split(string(paneOut), "\n")[0])
		raw := e.exec(worker, "node", filepath.Join(proj, "mcpclient.js"), sock, pane)
		type toolResult struct {
			IsError bool   `json:"isError"`
			Text    string `json:"text"`
		}
		var got struct {
			Fatal       string     `json:"fatal"`
			Tools       []string   `json:"tools"`
			Sessions    toolResult `json:"sessions"`
			OtherSocket toolResult `json:"otherSocket"`
			Exec        toolResult `json:"exec"`
			Result      toolResult `json:"result"`
			Screen      toolResult `json:"screen"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil || got.Fatal != "" {
			t.Fatalf("MCP 客户端没有跑完：%v %s", err, truncateForLog(raw))
		}
		tools := " " + strings.Join(got.Tools, " ") + " "
		for _, want := range []string{"execute-command", "send-keys", "capture-pane"} {
			if !strings.Contains(tools, " "+want+" ") {
				t.Errorf("tmux-mcp 的工具列表里缺少 %s", want)
			}
		}
		for _, banned := range []string{"kill-session", "kill-window", "kill-pane", "detach-client"} {
			if strings.Contains(tools, " "+banned+" ") {
				t.Errorf("默认的工具范围不应包含 %s", banned)
			}
		}
		if got.Sessions.IsError || !strings.Contains(got.Sessions.Text, "main") {
			t.Errorf("经 tmux-mcp 调用 list-sessions 没有看到 main：%+v", got.Sessions)
		}
		if !got.OtherSocket.IsError {
			t.Errorf("不在允许范围内的 socket 应被拒绝：%+v", got.OtherSocket)
		}
		// 命令的结束与退出码能取回来：tmux-mcp 让宿主的 pane 用同一个 socket 路径回报命令结束，
		// 槽内外路径不同时这一步会失败（pane 里出现 error connecting）。
		if got.Exec.IsError || got.Result.IsError ||
			!strings.Contains(got.Result.Text, `"status":"completed"`) || !strings.Contains(got.Result.Text, `"exitCode":0`) {
			t.Errorf("execute-command 应执行完并取回退出码：%+v / %+v", got.Exec, got.Result)
		}
		// 命令是在宿主上执行的：pane 里出现的是宿主的主机名，不是 worker 的。
		hostName, _ := os.Hostname()
		if got.Screen.IsError || !strings.Contains(got.Screen.Text, hostName) ||
			strings.Contains(got.Screen.Text, "error connecting") || hostName == e.exec(worker, "hostname") {
			t.Errorf("hostname 应在宿主上执行（宿主是 %q），pane 里是：%s", hostName, truncateForLog(got.Screen.Text))
		}

		// 宿主的 server 重建后，worker 不用重建就能看到新的 socket。
		before, err := e.dk.ContainerInspect(e.ctx, worker)
		must(t, err)
		must(t, e.eng.TmuxKill(e.ctx, "t"))
		e.expect("server 结束后槽内连不上", `gone`, e.exec(worker, "/bin/sh", "-c", list+" 2>/dev/null || echo gone"))
		started, err := e.eng.TmuxEnsure(e.ctx, "t", proj)
		must(t, err)
		if !started {
			t.Error("server 结束后应重新启动")
		}
		e.expect("重建后槽内重新连上", `^main$`, e.exec(worker, "/bin/sh", "-c", list))
		after, err := e.dk.ContainerInspect(e.ctx, worker)
		must(t, err)
		if before.ID != after.ID {
			t.Error("worker 不应被重建")
		}

		// 停止槽位不结束宿主上的 tmux；它还在时不允许连 HOME 一起删除槽位。
		must(t, e.eng.SlotStop(e.ctx, "t"))
		if !e.eng.TmuxAlive(e.ctx, "t") {
			t.Error("停止槽位不应结束宿主的 tmux")
		}
		if err := e.eng.SlotRemove(e.ctx, "t", true); err == nil || !strings.Contains(err.Error(), "tmux") {
			t.Errorf("宿主 tmux 还在运行时应拒绝彻底删除槽位：%v", err)
		}
		must(t, e.eng.TmuxKill(e.ctx, "t"))
		must(t, e.eng.SlotRemove(e.ctx, "t", true))
	})

	t.Run("操作记录", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(e.st.Dir(), "ops.jsonl"))
		must(t, err)
		for _, want := range []string{`"op":"slot.up"`, `"op":"slot.hold"`, `"op":"gateway.down"`, `"ok":false`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("操作记录里缺少 %s", want)
			}
		}
	})
}
