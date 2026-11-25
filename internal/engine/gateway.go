package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/mihomo"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// gatewayPlan 是一个网关的期望状态及其渲染结果。
type gatewayPlan struct {
	gw       model.Gateway
	image    string
	pool     netip.Prefix
	transit  *model.Node
	slots    []model.Slot
	links    map[string]netplan.Link
	landings map[string]model.Node
	out      mihomo.Output
}

// gatewayRuntime 是运行中网关容器的现场信息。
type gatewayRuntime struct {
	id        string
	name      string
	egressIf  string
	egressNet netip.Prefix
	bridgeGW  netip.Addr // 出口网络没有网关地址时为零值
}

// plan 读取一个网关及其全部槽位的期望配置并生成核心配置。
func (e *Engine) plan(gwID string) (*gatewayPlan, error) {
	gw, err := e.st.Gateway(gwID)
	if err != nil {
		return nil, err
	}
	gw.Normalize()
	if err := gw.Validate(); err != nil {
		return nil, err
	}
	p := &gatewayPlan{gw: gw, image: gw.Image, links: map[string]netplan.Link{}, landings: map[string]model.Node{}}
	if p.image == "" {
		p.image = e.cl.GatewayImage
	}
	p.pool, _ = netip.ParsePrefix(gw.Pool)

	in := mihomo.Input{Gateway: gw}
	for i, tid := range gw.Transits() {
		t, err := e.st.Node(tid)
		if err != nil {
			return nil, fmt.Errorf("网关 %s 的中转：%w", gw.ID, err)
		}
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if i == 0 {
			p.transit, in.Transit = &t, &t
		} else {
			in.Backups = append(in.Backups, t)
		}
	}
	slots, err := e.st.Slots()
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		if sl.Gateway != gw.ID {
			continue
		}
		if err := sl.Validate(); err != nil {
			return nil, err
		}
		subnet, _ := netip.ParsePrefix(sl.Subnet)
		link, err := netplan.Locate(p.pool, subnet)
		if err != nil {
			return nil, fmt.Errorf("槽位 %s：%w", sl.ID, err)
		}
		landing, err := e.st.Node(sl.Landing)
		if err != nil {
			return nil, fmt.Errorf("槽位 %s 的落地：%w", sl.ID, err)
		}
		if err := landing.Validate(); err != nil {
			return nil, err
		}
		p.slots = append(p.slots, sl)
		p.links[sl.ID] = link
		p.landings[sl.ID] = landing
		in.Bindings = append(in.Bindings, mihomo.Binding{SlotID: sl.ID, Link: link, Landing: landing})
	}
	p.out, err = mihomo.Render(in)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Render 返回一个网关的核心配置；redact 为 true 时隐藏节点凭据。
func (e *Engine) Render(gwID string, redact bool) ([]byte, error) {
	p, err := e.plan(gwID)
	if err != nil {
		return nil, err
	}
	if !redact {
		return p.out.Config, nil
	}
	return mihomo.Redact(p.out.Config)
}

// gatewaySpec 构造网关容器：只有 NET_ADMIN，带 TUN 设备，不设自动重启。
func (e *Engine) gatewaySpec(p *gatewayPlan) dockerapi.ContainerSpec {
	return dockerapi.ContainerSpec{
		Image:    p.image,
		Hostname: "gw-" + p.gw.ID,
		Labels:   e.labels(roleGateway, p.gw.ID),
		HostConfig: dockerapi.HostConfig{
			NetworkMode: p.gw.EgressNetwork,
			CapDrop:     []string{"ALL"},
			CapAdd:      []string{"NET_ADMIN"},
			SecurityOpt: []string{"no-new-privileges"},
			Devices:     []dockerapi.DeviceMapping{{PathOnHost: "/dev/net/tun", PathInContainer: "/dev/net/tun", CgroupPermissions: "rwm"}},
			Sysctls: map[string]string{
				"net.ipv4.ip_forward":                "1",
				"net.ipv4.conf.all.rp_filter":        "0",
				"net.ipv4.conf.default.rp_filter":    "0",
				"net.ipv6.conf.all.disable_ipv6":     "1",
				"net.ipv6.conf.default.disable_ipv6": "1",
			},
			// 每次启动都是空的：监督进程在这里放 ready 标记。
			Tmpfs:     map[string]string{"/run/ccgw": "rw,nosuid,nodev,noexec,size=1m"},
			Init:      dockerapi.Bool(true),
			PidsLimit: 512,
			LogConfig: &dockerapi.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
		},
	}
}

func specDigest(spec dockerapi.ContainerSpec) string {
	b, _ := json.Marshal(spec)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:32]
}

// GatewayUp 启动（或对齐）一个网关：容器、首跳路由、核心配置、TUN。
func (e *Engine) GatewayUp(ctx context.Context, id string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("gateway.up", id, err, "") }()
	_, _, err = e.gatewayUp(ctx, id)
	return err
}

func (e *Engine) gatewayUp(ctx context.Context, id string) (*gatewayPlan, *gatewayRuntime, error) {
	// 先核对自动解析的首跳地址：中转，或者专用网关直连的那个落地。
	if gw, err := e.st.Gateway(id); err == nil {
		if gw.Transit != "" {
			for _, t := range gw.Transits() {
				e.refreshFirstHop(ctx, t)
			}
		} else if slots, err := e.st.Slots(); err == nil {
			for _, s := range slots {
				if s.Gateway == id {
					e.refreshFirstHop(ctx, s.Landing)
				}
			}
		}
	}
	p, err := e.plan(id)
	if err != nil {
		return nil, nil, err
	}
	rt, err := e.ensureGateway(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	if err := e.syncRoutes(ctx, rt, p.out.FirstHops); err != nil {
		return nil, nil, fmt.Errorf("设置网关 %s 的首跳路由：%w", id, err)
	}
	if err := e.syncConfig(ctx, rt, p.out); err != nil {
		return nil, nil, err
	}
	return p, rt, nil
}

// ensureGateway 确保网关容器存在并在运行，返回它的现场信息。
func (e *Engine) ensureGateway(ctx context.Context, p *gatewayPlan) (*gatewayRuntime, error) {
	name := e.GatewayContainer(p.gw.ID)
	spec := e.gatewaySpec(p)
	digest := specDigest(spec)
	spec.Labels[labelSpec] = digest

	c, ok, err := e.inspect(ctx, name)
	if err != nil {
		return nil, err
	}
	if ok && c.Config.Labels[labelSpec] != digest {
		return nil, fmt.Errorf("网关容器 %s 的设置与期望不一致；先执行 gateway down %s 再 up（它上面所有槽的线会断开，之后逐槽 slot up）", name, p.gw.ID)
	}
	if !ok {
		if has, err := e.dk.ImageExists(ctx, p.image); err != nil {
			return nil, err
		} else if !has {
			return nil, fmt.Errorf("本地没有网关镜像 %s；先%s", p.image, imageHint(p.image))
		}
		if err := e.ensureEgressNetwork(ctx, p.gw.EgressNetwork); err != nil {
			return nil, err
		}
		e.logf("创建网关容器 %s", name)
		if _, err := e.dk.ContainerCreate(ctx, name, spec); err != nil {
			return nil, err
		}
	}
	if !ok || !c.State.Running {
		e.logf("启动网关容器 %s", name)
		if err := e.dk.ContainerStart(ctx, name); err != nil {
			return nil, err
		}
	}
	// 监督进程清空主路由表后才放 ready 标记；在那之前写的路由会被清掉。
	err = poll(ctx, 20*time.Second, 200*time.Millisecond, func() (bool, error) {
		_, err := e.sh(ctx, name, "test -f /run/ccgw/ready")
		var xe *exitError
		if errors.As(err, &xe) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return nil, fmt.Errorf("网关 %s 的监督进程没有就绪：%w", p.gw.ID, err)
	}
	return e.gatewayRuntime(ctx, p.gw)
}

// ensureEgressNetwork 在出口网络不存在时创建一个普通 bridge 网络；已存在的网络原样使用。
func (e *Engine) ensureEgressNetwork(ctx context.Context, name string) error {
	_, err := e.dk.NetworkInspect(ctx, name)
	if err == nil {
		return nil
	}
	if !dockerapi.IsNotFound(err) {
		return err
	}
	e.logf("创建出口网络 %s", name)
	return e.dk.NetworkCreate(ctx, dockerapi.NetworkSpec{Name: name, Labels: map[string]string{labelCluster: e.cl.Name}})
}

// gatewayRuntime 读取运行中网关的出口网卡信息。
func (e *Engine) gatewayRuntime(ctx context.Context, gw model.Gateway) (*gatewayRuntime, error) {
	name := e.GatewayContainer(gw.ID)
	c, ok, err := e.inspect(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok || !c.State.Running {
		return nil, fmt.Errorf("网关 %s 没有在运行", gw.ID)
	}
	ep, ok := c.NetworkSettings.Networks[gw.EgressNetwork]
	if !ok || ep.IPAddress == "" {
		return nil, fmt.Errorf("网关容器 %s 没有接在出口网络 %s 上", name, gw.EgressNetwork)
	}
	ip, err := netip.ParseAddr(ep.IPAddress)
	if err != nil {
		return nil, err
	}
	rt := &gatewayRuntime{id: c.ID, name: name, egressNet: netip.PrefixFrom(ip, ep.IPPrefixLen).Masked()}
	if ep.Gateway != "" {
		rt.bridgeGW, _ = netip.ParseAddr(ep.Gateway)
	}
	out, err := e.sh(ctx, name, "ip -o -4 addr show")
	if err != nil {
		return nil, err
	}
	rt.egressIf = ifaceWithAddr(out, ip)
	if rt.egressIf == "" {
		return nil, fmt.Errorf("在网关容器 %s 里找不到地址为 %s 的网卡", name, ip)
	}
	return rt, nil
}

// ifaceWithAddr 从 `ip -o -4 addr show` 的输出里找出持有给定地址的网卡名。
func ifaceWithAddr(out string, ip netip.Addr) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 2; i+1 < len(f); i++ {
			if f[i] != "inet" {
				continue
			}
			if p, err := netip.ParsePrefix(f[i+1]); err == nil && p.Addr() == ip {
				return strings.TrimSuffix(f[1], ":")
			}
		}
	}
	return ""
}

// syncRoutes 让网关主路由表里出口网卡上的路由恰好等于首跳集合。
// 只做增量改动：已有且仍需要的路由不动，其它槽的既有连接不受影响。
func (e *Engine) syncRoutes(ctx context.Context, rt *gatewayRuntime, hops []netip.Addr) error {
	cur, err := e.sh(ctx, rt.name, "ip -4 route show table main")
	if err != nil {
		return err
	}
	cmds, err := routeCommands(cur, rt.egressIf, rt.egressNet, rt.bridgeGW, hops)
	if err != nil {
		return err
	}
	if len(cmds) == 0 {
		return nil
	}
	_, err = e.sh(ctx, rt.name, strings.Join(cmds, " && "))
	return err
}

// routeCommands 计算把主路由表对齐到首跳集合所需的命令。
//
// 首跳与网关在同一网段时直接走出口网卡；否则经出口网络的网桥地址转发。
// 出口网卡上的其它路由（默认路由、网段直连路由、不再使用的首跳）一律删除；
// 各槽 veth 上的直连路由属于回程路径，不在这里处理。
func routeCommands(current, egressIf string, egressNet netip.Prefix, bridgeGW netip.Addr, hops []netip.Addr) ([]string, error) {
	want := map[string]string{}
	for _, h := range hops {
		if !h.Is4() {
			return nil, fmt.Errorf("首跳 %s 不是 IPv4 地址", h)
		}
		switch {
		case egressNet.Contains(h):
			want[h.String()] = fmt.Sprintf("%s dev %s scope link", h, egressIf)
		case bridgeGW.IsValid():
			want[h.String()] = fmt.Sprintf("%s via %s dev %s onlink", h, bridgeGW, egressIf)
		default:
			return nil, fmt.Errorf("首跳 %s 不在出口网段 %s 内，而出口网络没有网关地址（internal 网络只能到达同网段的首跳）", h, egressNet)
		}
	}

	var cmds []string
	have := map[string]bool{}
	for _, line := range strings.Split(current, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		dest, dev := f[0], ""
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "dev" {
				dev = f[i+1]
			}
		}
		if dest != "default" && dev != egressIf {
			continue
		}
		spec, wanted := want[dest]
		if wanted && sameRoute(f, strings.Fields(spec)) {
			have[dest] = true
			continue
		}
		if !wanted {
			del := "ip route del " + dest
			if dev != "" {
				del += " dev " + dev
			}
			cmds = append(cmds, del)
		}
	}
	for _, h := range hops {
		if !have[h.String()] {
			cmds = append(cmds, "ip route replace "+want[h.String()])
		}
	}
	return cmds, nil
}

// sameRoute 判断一条现有路由是否已经是期望的形式（只比较目的与下一跳）。
func sameRoute(have, want []string) bool {
	via := func(f []string) string {
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "via" {
				return f[i+1]
			}
		}
		return ""
	}
	return have[0] == want[0] && via(have) == via(want)
}

// configError 是核心拒绝配置时的错误。核心的报错可能带出配置片段，因此不写入操作记录。
type configError struct{ detail string }

func (c *configError) Error() string { return "核心配置校验失败：" + c.detail }

// syncConfig 把核心配置送进网关并让它生效，然后等 TUN 与表 100 就绪。
func (e *Engine) syncConfig(ctx context.Context, rt *gatewayRuntime, out mihomo.Output) error {
	cur, _ := e.sh(ctx, rt.name, "cat "+mihomo.HomeDir+"/config.digest 2>/dev/null")
	if strings.TrimSpace(cur) != out.Digest {
		e.logf("更新网关 %s 的核心配置", rt.name)
		// 经归档接口写入：内容不经过命令行，也不出现在容器元数据里。
		err := e.dk.CopyTo(ctx, rt.name, mihomo.HomeDir, map[string]dockerapi.File{
			"config.new.yaml": {Data: out.Config, Mode: 0o600},
		})
		if err != nil {
			return fmt.Errorf("写入核心配置：%w", err)
		}
		newPath := mihomo.HomeDir + "/config.new.yaml"
		if _, err := e.sh(ctx, rt.name, fmt.Sprintf("mihomo -t -d %s -f %s 2>&1", mihomo.HomeDir, newPath)); err != nil {
			_, _ = e.sh(ctx, rt.name, "rm -f "+newPath)
			var xe *exitError
			if errors.As(err, &xe) {
				return &configError{detail: lastLines(xe.output, 3)}
			}
			return err
		}
		if _, err := e.sh(ctx, rt.name, fmt.Sprintf("mv %s %s && echo %s > %s/config.digest", newPath, mihomo.ConfigPath, out.Digest, mihomo.HomeDir)); err != nil {
			return err
		}
		// 核心在运行就在线重载（不影响其它槽的既有连接）；没在运行时监督进程会用新配置拉起它。
		reload := fmt.Sprintf(`if [ -S %[1]s ]; then curl -sS -o /dev/null -w '%%{http_code}' --unix-socket %[1]s -X PUT -H 'Content-Type: application/json' -d '{"path":""}' 'http://localhost/configs?force=true' 2>/dev/null || echo fail; else echo nosock; fi`, mihomo.SocketPath)
		code, err := e.sh(ctx, rt.name, reload)
		if err != nil {
			return err
		}
		switch code = strings.TrimSpace(code); {
		case code == "204", code == "nosock":
		default:
			// 在线重载没成功：让监督进程用新配置重启核心。所有槽会短暂断网，但不会带着旧配置继续跑。
			e.logf("在线重载未成功（%s），重启核心", code)
			if _, err := e.sh(ctx, rt.name, "pkill mihomo; true"); err != nil {
				return err
			}
		}
	}
	err := poll(ctx, 40*time.Second, 300*time.Millisecond, func() (bool, error) {
		_, err := e.sh(ctx, rt.name, fmt.Sprintf("ip link show %s >/dev/null 2>&1 && ip route show table %d | grep -q '^default'", netplan.TUNDevice, netplan.RouteTable))
		var xe *exitError
		if errors.As(err, &xe) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return fmt.Errorf("网关 %s 的 TUN 没有就绪（%w）；用 docker logs %s 查看核心输出", rt.name, err, rt.name)
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// GatewayDown 停止并删除网关容器。它上面还有运行中的 worker 时需要 force。
func (e *Engine) GatewayDown(ctx context.Context, id string, force bool) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("gateway.down", id, err, "") }()

	if _, err := e.st.Gateway(id); err != nil {
		return err
	}
	slots, err := e.st.Slots()
	if err != nil {
		return err
	}
	var running []string
	for _, sl := range slots {
		if sl.Gateway != id {
			continue
		}
		if c, ok, err := e.inspect(ctx, e.WorkerContainer(sl.ID)); err != nil {
			return err
		} else if ok && c.State.Running {
			running = append(running, sl.ID)
		}
	}
	if len(running) > 0 && !force {
		return fmt.Errorf("网关 %s 上还有运行中的槽位 %s；停网关会让它们全部断网，确认后加 --force", id, strings.Join(running, "、"))
	}
	name := e.GatewayContainer(id)
	if err := e.dk.ContainerStop(ctx, name, 10*time.Second); err != nil {
		return err
	}
	return e.dk.ContainerRemove(ctx, name)
}

// RemoveGateway 删除网关的期望配置；仍有槽位引用或容器还在时拒绝。
func (e *Engine) RemoveGateway(ctx context.Context, id string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("gateway.rm", id, err, "") }()

	slots, err := e.st.Slots()
	if err != nil {
		return err
	}
	for _, sl := range slots {
		if sl.Gateway == id {
			return fmt.Errorf("槽位 %s 还在使用网关 %s", sl.ID, id)
		}
	}
	if _, ok, err := e.inspect(ctx, e.GatewayContainer(id)); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("网关 %s 的容器还在，先执行 gateway down %s", id, id)
	}
	return e.st.DeleteGateway(id)
}

// GatewayStatus 是一个网关的现状。
type GatewayStatus struct {
	Gateway   model.Gateway
	Container string // absent / created / running / exited …
	CoreReady bool   // TUN 与表 100 是否就位
	// ActiveTransit 是有候选中转的网关当前在用的那个中转；没有候选或问不到时为空。
	ActiveTransit string
	Slots         []string
	// ConfigCurrent 表示容器里的核心配置与期望一致。
	ConfigCurrent bool
}

// GatewayStatuses 返回全部网关的现状。
func (e *Engine) GatewayStatuses(ctx context.Context) ([]GatewayStatus, error) {
	gws, err := e.st.Gateways()
	if err != nil {
		return nil, err
	}
	var out []GatewayStatus
	for _, gw := range gws {
		gw.Normalize()
		s := GatewayStatus{Gateway: gw, Container: "absent"}
		p, perr := e.plan(gw.ID)
		if perr == nil {
			for _, sl := range p.slots {
				s.Slots = append(s.Slots, sl.ID)
			}
		}
		name := e.GatewayContainer(gw.ID)
		c, ok, err := e.inspect(ctx, name)
		if err != nil {
			return nil, err
		}
		if ok {
			s.Container = c.State.Status
		}
		if ok && c.State.Running {
			_, rerr := e.sh(ctx, name, fmt.Sprintf("ip link show %s >/dev/null 2>&1 && ip route show table %d | grep -q '^default'", netplan.TUNDevice, netplan.RouteTable))
			s.CoreReady = rerr == nil
			if perr == nil {
				cur, _ := e.sh(ctx, name, "cat "+mihomo.HomeDir+"/config.digest 2>/dev/null")
				s.ConfigCurrent = strings.TrimSpace(cur) == p.out.Digest
			}
			if len(gw.Backups) > 0 && s.CoreReady {
				s.ActiveTransit = e.activeTransit(ctx, name, gw)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// activeTransit 问核心有候选中转的网关现在用的是哪一个；问不到时返回空。
func (e *Engine) activeTransit(ctx context.Context, container string, gw model.Gateway) string {
	out, err := e.sh(ctx, container, fmt.Sprintf("curl -sS -m 4 --unix-socket %s 'http://localhost/proxies/%s'", mihomo.SocketPath, mihomo.TransitName))
	if err != nil {
		return ""
	}
	var res struct {
		Now string `json:"now"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &res) != nil {
		return ""
	}
	for i, id := range gw.Transits() {
		if mihomo.TransitMemberName(i) == res.Now {
			return id
		}
	}
	return ""
}
