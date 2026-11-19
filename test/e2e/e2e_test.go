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
	ipLandA         = "198.51.100.3"
	ipLandB         = "198.51.100.4"
	ipTarget        = "198.51.100.5"
	fakenet         = "/opt/e2e/fakenet"
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

	data := filepath.Join(base, "data")
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	e.st, err = store.Init(data, model.Cluster{Name: cluster, GatewayImage: image, WorkerImage: image})
	if err != nil {
		t.Fatal(err)
	}
	e.eng, err = engine.New(e.st, dk, logWriter{t})
	if err != nil {
		t.Fatal(err)
	}

	socks := func(server string) map[string]any {
		return map[string]any{"type": "socks5", "server": server, "port": 1080}
	}
	// 中转用域名 + connect_ipv4，顺带验证核心的 hosts 固定。
	must(t, e.eng.AddNode(e.ctx, model.Node{ID: "transit", Role: model.RoleTransit, Proxy: socks("transit.test"), ConnectIPv4: ipTransitEgress}))
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
		e.expect("worker 自己不能改路由", `not permitted`, e.wsh("a", "ip route add 192.0.2.0/24 dev eth0 2>&1"))
	})

	t.Run("透明转发与按来源分流", func(t *testing.T) {
		e.t = t
		e.expect("A 出口为落地 A", seenA, e.w("a", "get", ipTarget, "80", "sniff-a.example"))
		e.expect("B 出口为落地 B", seenB, e.w("b", "get", ipTarget, "80", "sniff-b.example"))
		e.expect("A 非嗅探端口 8080", seenA, e.w("a", "get", ipTarget, "8080"))
		e.expect("A 系统解析（经 TUN 接管）", `^OK 198\.51\.100\.5$`, e.w("a", "resolve", "bar.example"))
		e.expect("A 直接问 8.8.8.8 也被接管", `^OK 198\.51\.100\.5$`, e.w("a", "dnsq", "8.8.8.8", "qux.example"))
		e.expect("核心的解析经中转到达 DNS", `DNSQ from 198\.51\.100\.2 `, e.logs("target"))
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
