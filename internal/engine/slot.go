package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/identity"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// ---- 登记期望配置 ----

// AddNode 登记一个节点。
func (e *Engine) AddNode(n model.Node) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("node.add", n.ID, err, n.Role) }()
	if err := n.Validate(); err != nil {
		return err
	}
	return e.st.SaveNode(n, true)
}

// RemoveNode 删除一个节点；仍被网关或槽位引用时拒绝。
func (e *Engine) RemoveNode(id string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("node.rm", id, err, "") }()
	gws, err := e.st.Gateways()
	if err != nil {
		return err
	}
	for _, g := range gws {
		if g.Transit == id {
			return fmt.Errorf("网关 %s 还在用节点 %s 作中转", g.ID, id)
		}
	}
	slots, err := e.st.Slots()
	if err != nil {
		return err
	}
	for _, s := range slots {
		if s.Landing == id {
			return fmt.Errorf("槽位 %s 还在用节点 %s 作落地", s.ID, id)
		}
	}
	return e.st.DeleteNode(id)
}

// AddGateway 登记一个网关。
func (e *Engine) AddGateway(g model.Gateway) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("gateway.add", g.ID, err, "") }()
	g.Normalize()
	if err := g.Validate(); err != nil {
		return err
	}
	if g.Transit != "" {
		t, err := e.st.Node(g.Transit)
		if err != nil {
			return err
		}
		if t.Role != model.RoleTransit {
			return fmt.Errorf("节点 %s 的角色不是中转", t.ID)
		}
		if _, err := t.FirstHopIP(); err != nil {
			return err
		}
	}
	return e.st.SaveGateway(g, true)
}

// AddSlot 登记一个槽位：分配网段，首次创建时生成身份。
// 返回补全后的槽位和需要提醒用户的事项。
func (e *Engine) AddSlot(sl model.Slot, hostname string) (out model.Slot, warnings []string, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return sl, nil, err
	}
	defer unlock()
	defer func() { e.record("slot.add", sl.ID, err, "") }()

	if err := model.ValidateID("槽位", sl.ID); err != nil {
		return sl, nil, err
	}
	gw, err := e.st.Gateway(sl.Gateway)
	if err != nil {
		return sl, nil, err
	}
	gw.Normalize()
	landing, err := e.st.Node(sl.Landing)
	if err != nil {
		return sl, nil, err
	}
	if landing.Role != model.RoleLanding {
		return sl, nil, fmt.Errorf("节点 %s 的角色不是落地", landing.ID)
	}
	if gw.Transit == "" {
		if _, err := landing.FirstHopIP(); err != nil {
			return sl, nil, err
		}
	}

	all, err := e.st.Slots()
	if err != nil {
		return sl, nil, err
	}
	var used []netip.Prefix
	for _, o := range all {
		if o.ID == sl.ID {
			return sl, nil, fmt.Errorf("槽位 %s 已存在", sl.ID)
		}
		if o.Landing == sl.Landing {
			warnings = append(warnings, fmt.Sprintf("落地 %s 已被槽位 %s 使用；两个槽共用一个出口地址会让它们互相关联", sl.Landing, o.ID))
		}
		if o.Gateway != sl.Gateway {
			continue
		}
		if gw.Transit == "" {
			return sl, nil, fmt.Errorf("网关 %s 没有中转，只能接一个槽（已有 %s）；给新槽另建一个专用网关", gw.ID, o.ID)
		}
		if p, err := netip.ParsePrefix(o.Subnet); err == nil {
			used = append(used, p)
		}
	}
	pool, err := netip.ParsePrefix(gw.Pool)
	if err != nil {
		return sl, nil, err
	}
	link, err := netplan.Allocate(pool, used)
	if err != nil {
		return sl, nil, fmt.Errorf("网关 %s：%w", gw.ID, err)
	}
	sl.Subnet = link.Subnet.String()
	sl.Normalize()
	if err := sl.Validate(); err != nil {
		return sl, nil, err
	}
	for _, m := range sl.Mounts {
		if _, err := os.Stat(m.Source); err != nil {
			return sl, nil, fmt.Errorf("挂载源 %s 不可用：%w", m.Source, err)
		}
	}

	_, has, err := e.st.Identity(sl.ID)
	if err != nil {
		return sl, nil, err
	}
	if has && hostname != "" {
		return sl, nil, fmt.Errorf("槽位 %s 已有持久化的身份，不能再指定 hostname", sl.ID)
	}
	if err := e.st.SaveSlot(sl, true); err != nil {
		return sl, nil, err
	}
	if has {
		warnings = append(warnings, fmt.Sprintf("沿用槽位 %s 之前保留的身份与 HOME", sl.ID))
	} else {
		ident, err := identity.New(hostname)
		if err != nil {
			return sl, nil, err
		}
		if err := e.st.SaveIdentity(sl.ID, ident); err != nil {
			return sl, nil, err
		}
	}
	return sl, warnings, nil
}

// ---- 启动与接线 ----

// UpOptions 是 SlotUp 的选项。
type UpOptions struct {
	// Recreate 允许在 worker 容器的设置与期望不一致时删掉重建（容器内的进程会结束）。
	Recreate bool
}

// UpResult 是 SlotUp 的结果。
type UpResult struct {
	ExitIP    string
	Worker    string
	Replumbed bool
}

// SlotUp 按下面的顺序启动一个槽：
// 对齐网关 → 经回环入口探测链路 → 启动无网的 worker → 接线 → 从 worker 的位置复测出口。
// 任何一步不符都不放行；已接的线会撤掉。
func (e *Engine) SlotUp(ctx context.Context, id string, opt UpOptions) (res UpResult, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return res, err
	}
	defer unlock()
	defer func() { e.record("slot.up", id, err, res.ExitIP) }()
	return e.slotUp(ctx, id, opt)
}

func (e *Engine) slotUp(ctx context.Context, id string, opt UpOptions) (res UpResult, err error) {
	slot, err := e.st.Slot(id)
	if err != nil {
		return res, err
	}
	if h, held, err := e.st.Hold(id); err != nil {
		return res, err
	} else if held {
		return res, fmt.Errorf("槽位 %s 处于手动断网（%s）；确认后先执行 slot release %s", id, orDash(h.Reason), id)
	}
	ident, ok, err := e.st.Identity(id)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, fmt.Errorf("槽位 %s 缺少身份文件", id)
	}

	p, rt, err := e.gatewayUp(ctx, slot.Gateway)
	if err != nil {
		return res, err
	}
	link, landing := p.links[id], p.landings[id]

	e.logf("经网关回环入口探测槽位 %s 的链路", id)
	chainIP, echoURL, err := e.probeChain(ctx, rt, link, p.gw.EchoURLs)
	if err != nil {
		return res, fmt.Errorf("槽位 %s 的链路不可用，未接线：%w", id, err)
	}
	if landing.ExpectExitIP != "" && chainIP != landing.ExpectExitIP {
		return res, fmt.Errorf("槽位 %s 的链路出口是 %s，与落地 %s 绑定的 %s 不符，未接线", id, chainIP, landing.ID, landing.ExpectExitIP)
	}

	wc, err := e.ensureWorker(ctx, slot, ident, opt.Recreate)
	if err != nil {
		return res, err
	}
	res.Worker = wc.Name

	plumbed, err := e.isPlumbed(ctx, rt, link)
	if err != nil {
		return res, err
	}
	if !plumbed {
		e.logf("为槽位 %s 接线（%s ↔ %s）", id, link.WorkerIP, link.GatewayIP)
		if err := e.plumb(ctx, p.image, rt, wc.Name, ident, link); err != nil {
			e.unplumb(ctx, rt, link)
			return res, fmt.Errorf("槽位 %s 接线失败：%w", id, err)
		}
		res.Replumbed = true
	}

	// 从 worker 的位置复测：规则映射写错时，这里看到的出口会与该链的出口不同。
	workerIP, err := e.probeFromWorker(ctx, p.image, wc.Name, append([]string{echoURL}, p.gw.EchoURLs...))
	if err != nil || workerIP != chainIP {
		e.unplumb(ctx, rt, link)
		_ = e.st.ClearApplied(id)
		if err != nil {
			return res, fmt.Errorf("从槽位 %s 的 worker 位置探测出口失败，已撤线：%w", id, err)
		}
		return res, fmt.Errorf("槽位 %s 的 worker 实际出口是 %s，与链路出口 %s 不符，已撤线", id, workerIP, chainIP)
	}

	res.ExitIP = workerIP
	digest, err := e.slotDigest(slot, ident, p)
	if err != nil {
		return res, err
	}
	err = e.st.SaveApplied(id, model.Applied{
		Digest: digest, WorkerID: wc.ID, GatewayID: rt.id, ExitIP: workerIP,
		AppliedAt: time.Now().UTC(), EchoURLUsed: echoURL,
	})
	return res, err
}

// slotDigest 是"这个槽的期望配置"的摘要：槽位、身份、它用到的节点与网关设置。
// 别的槽的增删不改变它。
func (e *Engine) slotDigest(slot model.Slot, ident model.Identity, p *gatewayPlan) (string, error) {
	image := slot.Image
	if image == "" {
		image = e.cl.WorkerImage
	}
	b, err := json.Marshal(struct {
		Slot     model.Slot
		Identity model.Identity
		Image    string
		Landing  model.Node
		Transit  *model.Node
		Gateway  model.Gateway
		GWImage  string
	}{slot, ident, image, p.landings[slot.ID], p.transit, p.gw, p.image})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ensureWorker 确保 worker 容器存在并在运行。容器以 --network none 启动，此时没有网络。
func (e *Engine) ensureWorker(ctx context.Context, slot model.Slot, ident model.Identity, recreate bool) (dockerapi.Container, error) {
	var none dockerapi.Container
	name := e.WorkerContainer(slot.ID)
	image := slot.Image
	if image == "" {
		image = e.cl.WorkerImage
	}

	files := workerFiles{Home: e.st.HomeDir(slot.ID)}
	var err error
	if files.MachineID, err = e.st.WriteSlotFile(slot.ID, "machine-id", []byte(ident.MachineID+"\n")); err != nil {
		return none, err
	}
	if files.Passwd, err = e.st.WriteSlotFile(slot.ID, "passwd", passwdFile(slot)); err != nil {
		return none, err
	}
	if files.Group, err = e.st.WriteSlotFile(slot.ID, "group", groupFile(slot)); err != nil {
		return none, err
	}
	if files.ResolvConf, err = e.st.WriteSlotFile(slot.ID, "resolv.conf", []byte("nameserver "+workerDNS+"\n")); err != nil {
		return none, err
	}
	// 挂载源不存在时 Docker 会以 root 身份在宿主上把它建出来，所以先自己检查。
	for _, m := range slot.Mounts {
		if _, err := os.Stat(m.Source); err != nil {
			return none, fmt.Errorf("槽位 %s 的挂载源 %s 不可用：%w", slot.ID, m.Source, err)
		}
	}

	spec := workerSpec(image, slot, ident, files, e.labels(roleWorker, slot.ID))
	digest := specDigest(spec)
	spec.Labels[labelSpec] = digest

	c, ok, err := e.inspect(ctx, name)
	if err != nil {
		return none, err
	}
	if ok && c.Config.Labels[labelSpec] != digest {
		if !recreate {
			return none, fmt.Errorf("槽位 %s 的 worker 容器与期望配置不一致；重建会结束容器内的进程，确认后执行 slot up %s --recreate", slot.ID, slot.ID)
		}
		e.logf("重建 worker 容器 %s", name)
		if err := e.dk.ContainerRemove(ctx, name); err != nil {
			return none, err
		}
		ok = false
	}
	if !ok {
		if has, err := e.dk.ImageExists(ctx, image); err != nil {
			return none, err
		} else if !has {
			return none, fmt.Errorf("本地没有 worker 镜像 %s", image)
		}
		e.logf("创建 worker 容器 %s（无网络）", name)
		if _, err := e.dk.ContainerCreate(ctx, name, spec); err != nil {
			return none, err
		}
	}
	if !ok || !c.State.Running {
		if err := e.dk.ContainerStart(ctx, name); err != nil {
			return none, err
		}
	}
	return e.dk.ContainerInspect(ctx, name)
}

// isPlumbed 报告该槽在网关一侧的网卡和策略路由是否都在。
// veth 的两端同生共死：网关侧网卡在，说明 worker 侧的那一端也在当前的 worker 里。
func (e *Engine) isPlumbed(ctx context.Context, rt *gatewayRuntime, link netplan.Link) (bool, error) {
	_, err := e.sh(ctx, rt.name, fmt.Sprintf("ip link show %s >/dev/null 2>&1 && ip rule show pref %d | grep -q . && ip rule show pref %d | grep -q .",
		link.GatewayIf(), link.RulePref(), link.RulePref()+1))
	var xe *exitError
	if errors.As(err, &xe) {
		return false, nil
	}
	return err == nil, err
}

// plumb 在 worker 与网关之间拉一条 veth 并登记策略路由。
//
// 顺序有讲究：
//  1. 先在网关登记"该网卡进来的包只能进 TUN，否则不可达"，再让网卡出现，
//     这样网卡一出现就受约束，不存在可以被主路由表直接转发的空档。
//  2. 助手站在 worker 一侧把对端推给网关。反方向会因无权打开 worker 进程的命名空间文件而失败。
func (e *Engine) plumb(ctx context.Context, image string, rt *gatewayRuntime, worker string, ident model.Identity, link netplan.Link) error {
	ifn, pref := link.GatewayIf(), link.RulePref()
	prep := fmt.Sprintf(
		"while ip rule del pref %[1]d 2>/dev/null; do :; done; while ip rule del pref %[2]d 2>/dev/null; do :; done; "+
			"ip link del %[3]s 2>/dev/null; "+
			"ip rule add pref %[1]d iif %[3]s from %[4]s lookup %[5]d && ip rule add pref %[2]d iif %[3]s unreachable",
		pref, pref+1, ifn, link.Subnet, netplan.RouteTable)
	if _, err := e.sh(ctx, rt.name, prep); err != nil {
		return fmt.Errorf("登记策略路由：%w", err)
	}

	wif := netplan.WorkerIfName
	create := fmt.Sprintf(
		"ip link add %[1]s type veth peer name %[2]s netns /proc/1/ns/net && ip link set %[1]s address %[3]s && "+
			"ip addr add %[4]s/30 dev %[1]s && ip link set %[1]s up",
		wif, ifn, ident.MAC, link.WorkerIP)
	// 助手的能力集必须与网关的主进程相同（仅 NET_ADMIN），内核才允许它打开网关的命名空间文件。
	if _, err := e.runHelper(ctx, e.helperSpec(image, worker, rt.name, []string{"NET_ADMIN"}, create)); err != nil {
		return fmt.Errorf("建立 veth：%w", err)
	}
	if _, err := e.sh(ctx, rt.name, fmt.Sprintf("ip addr add %s/30 dev %s && ip link set %s up", link.GatewayIP, ifn, ifn)); err != nil {
		return fmt.Errorf("配置网关一端：%w", err)
	}
	route := fmt.Sprintf("ip route add default via %s dev %s", link.GatewayIP, wif)
	if _, err := e.runHelper(ctx, e.helperSpec(image, worker, "", []string{"NET_ADMIN"}, route)); err != nil {
		return fmt.Errorf("写入 worker 的默认路由：%w", err)
	}
	return nil
}

// unplumb 删除该槽的 veth。worker 随即只剩 lo；别的槽不受影响。尽力而为，不返回错误。
func (e *Engine) unplumb(ctx context.Context, rt *gatewayRuntime, link netplan.Link) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	_, _ = e.sh(cctx, rt.name, fmt.Sprintf("ip link del %s 2>/dev/null; true", link.GatewayIf()))
}

// ---- 出口探测 ----

var ipv4Re = regexp.MustCompile(`(?:\d{1,3}\.){3}\d{1,3}`)

// parseExitIP 从探测地址的应答里取第一个 IPv4 地址。
func parseExitIP(body string) (string, bool) {
	for _, m := range ipv4Re.FindAllString(body, -1) {
		if a, err := netip.ParseAddr(m); err == nil && a.Is4() {
			return a.String(), true
		}
	}
	return "", false
}

// probeChain 经该槽所用链路在网关回环上的入口访问探测地址，返回出口 IP 和成功的那个地址。
func (e *Engine) probeChain(ctx context.Context, rt *gatewayRuntime, link netplan.Link, urls []string) (ip, used string, err error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		for _, u := range urls {
			out, err := e.sh(ctx, rt.name, fmt.Sprintf("curl -sS -m 10 -x socks5h://127.0.0.1:%d '%s'", link.ProbePort(), u))
			if err != nil {
				last = err
				continue
			}
			if ip, ok := parseExitIP(out); ok {
				return ip, u, nil
			}
			last = fmt.Errorf("%s 的应答里没有 IPv4 地址", u)
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return "", "", last
}

// probeFromWorker 让一个不带任何能力的助手进入 worker 的网络命名空间访问探测地址。
// 它看到的网络与 worker 内的程序完全相同，不依赖 worker 镜像里有没有 curl。
func (e *Engine) probeFromWorker(ctx context.Context, image, worker string, urls []string) (string, error) {
	var last error
	seen := map[string]bool{}
	for _, u := range urls {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		spec := e.helperSpec(image, worker, "", nil, fmt.Sprintf("curl -sS -m 10 '%s'", u))
		spec.User = "65534:65534"
		out, err := e.runHelper(ctx, spec)
		if err != nil {
			last = err
			continue
		}
		if ip, ok := parseExitIP(out); ok {
			return ip, nil
		}
		last = fmt.Errorf("%s 的应答里没有 IPv4 地址", u)
	}
	return "", last
}

// ---- 停止、断网、删除 ----

// SlotStop 停止 worker 容器。它的网络命名空间随之销毁，线也就没了；网关继续运行。
func (e *Engine) SlotStop(ctx context.Context, id string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("slot.stop", id, err, "") }()
	if _, err := e.st.Slot(id); err != nil {
		return err
	}
	return e.dk.ContainerStop(ctx, e.WorkerContainer(id), 10*time.Second)
}

// SlotHold 手动断开一个槽的网络并写下持久标记；标记在时 SlotUp 拒绝接线。
func (e *Engine) SlotHold(ctx context.Context, id, reason string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("slot.hold", id, err, reason) }()
	slot, err := e.st.Slot(id)
	if err != nil {
		return err
	}
	// 先落标记再撤线：即使撤线中途失败，之后也不会被自动接回去。
	if err := e.st.SetHold(id, model.Hold{Reason: reason, At: time.Now().UTC()}); err != nil {
		return err
	}
	return e.disconnect(ctx, slot, false)
}

// SlotRelease 清除手动断网标记。不会自动接线，之后需要再执行 SlotUp。
func (e *Engine) SlotRelease(id string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("slot.release", id, err, "") }()
	if _, err := e.st.Slot(id); err != nil {
		return err
	}
	return e.st.ClearHold(id)
}

// disconnect 在网关还在运行时撤掉该槽的线；dropRules 为 true 时连策略路由一起删。
func (e *Engine) disconnect(ctx context.Context, slot model.Slot, dropRules bool) error {
	gw, err := e.st.Gateway(slot.Gateway)
	if err != nil {
		return err
	}
	gw.Normalize()
	c, ok, err := e.inspect(ctx, e.GatewayContainer(gw.ID))
	if err != nil {
		return err
	}
	if !ok || !c.State.Running {
		return nil // 网关不在，线本来就不存在
	}
	pool, err := netip.ParsePrefix(gw.Pool)
	if err != nil {
		return err
	}
	subnet, err := netip.ParsePrefix(slot.Subnet)
	if err != nil {
		return err
	}
	link, err := netplan.Locate(pool, subnet)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("ip link del %s 2>/dev/null; ", link.GatewayIf())
	if dropRules {
		script += fmt.Sprintf("while ip rule del pref %d 2>/dev/null; do :; done; while ip rule del pref %d 2>/dev/null; do :; done; ", link.RulePref(), link.RulePref()+1)
	}
	script += fmt.Sprintf("! ip link show %s >/dev/null 2>&1", link.GatewayIf())
	if _, err := e.sh(ctx, c.Name, script); err != nil {
		return fmt.Errorf("撤线失败，槽位 %s 的网卡 %s 仍在网关里：%w", slot.ID, link.GatewayIf(), err)
	}
	return nil
}

// SlotRemove 删除一个槽：撤线、删 worker 容器、从网关配置里去掉它的规则。
// purge 为 false 时保留 HOME 与身份。
func (e *Engine) SlotRemove(ctx context.Context, id string, purge bool) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("slot.rm", id, err, fmt.Sprintf("purge=%v", purge)) }()
	slot, err := e.st.Slot(id)
	if err != nil {
		return err
	}
	if err := e.disconnect(ctx, slot, true); err != nil {
		return err
	}
	if err := e.dk.ContainerRemove(ctx, e.WorkerContainer(id)); err != nil {
		return err
	}
	if err := e.st.DeleteSlot(id, purge); err != nil {
		return err
	}
	// 网关在运行就立刻把这条来源规则去掉；不在运行时，下次启动自然按新配置生成。
	if c, ok, err := e.inspect(ctx, e.GatewayContainer(slot.Gateway)); err != nil {
		return err
	} else if ok && c.State.Running {
		if _, _, err := e.gatewayUp(ctx, slot.Gateway); err != nil {
			return fmt.Errorf("槽位已删除，但更新网关 %s 的配置失败：%w", slot.Gateway, err)
		}
	}
	return nil
}

// SlotExec 在槽位的 worker 里以项目用户身份执行命令（不接标准输入）。
func (e *Engine) SlotExec(ctx context.Context, id string, cmd []string) (dockerapi.RunResult, error) {
	if _, err := e.st.Slot(id); err != nil {
		return dockerapi.RunResult{}, err
	}
	return e.dk.Exec(ctx, e.WorkerContainer(id), cmd, dockerapi.ExecOptions{})
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未注明原因"
	}
	return s
}
