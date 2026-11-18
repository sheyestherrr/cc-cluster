package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/identity"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// ---- 登记期望配置 ----

// AddNode 登记一个节点。中转节点的 server 是域名且没有指定首跳地址时，在宿主上解析一次并记下。
func (e *Engine) AddNode(ctx context.Context, n model.Node) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("node.add", n.ID, err, n.Role) }()
	if err := n.Validate(); err != nil {
		return err
	}
	if n.Role == model.RoleTransit {
		if err := resolveFirstHop(ctx, &n); err != nil {
			return err
		}
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

// SlotOptions 是登记槽位时可以指定的身份部分；留空则随机生成。
type SlotOptions struct {
	Hostname string
	User     string
}

// AddSlot 登记一个槽位：分配网段，首次创建时生成身份。
// 返回补全后的槽位和需要提醒用户的事项。
func (e *Engine) AddSlot(ctx context.Context, sl model.Slot, opt SlotOptions) (out model.Slot, warnings []string, err error) {
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
		// 专用网关直连落地：落地就是首跳，同样需要事先知道它的地址。
		if _, err := landing.FirstHopIP(); err != nil {
			if err := resolveFirstHop(ctx, &landing); err != nil {
				return sl, nil, err
			}
			if err := e.st.SaveNode(landing, false); err != nil {
				return sl, nil, err
			}
		}
	}

	all, err := e.st.Slots()
	if err != nil {
		return sl, nil, err
	}
	var used []netip.Prefix
	var takenUsers []string
	for _, o := range all {
		if o.ID == sl.ID {
			return sl, nil, fmt.Errorf("槽位 %s 已存在", sl.ID)
		}
		if oi, ok, _ := e.st.Identity(o.ID); ok {
			takenUsers = append(takenUsers, oi.UserName())
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
	ident, has, err := e.st.Identity(sl.ID)
	if err != nil {
		return sl, nil, err
	}
	if has && (opt.Hostname != "" || opt.User != "") {
		return sl, nil, fmt.Errorf("槽位 %s 已有持久化的身份，不能再指定 hostname 或用户名", sl.ID)
	}
	if !has {
		if ident, err = identity.New(identity.Options{Hostname: opt.Hostname, User: opt.User, TakenUsers: takenUsers}); err != nil {
			return sl, nil, err
		}
	}
	for _, m := range sl.Mounts {
		if _, err := os.Stat(m.Source); err != nil {
			return sl, nil, fmt.Errorf("挂载源 %s 不可用：%w", m.Source, err)
		}
		if err := model.CheckMountTarget(m.Target, ident.Home()); err != nil {
			return sl, nil, err
		}
	}
	if sl.FakeDMI && len(ident.DMI) == 0 {
		return sl, nil, fmt.Errorf("槽位 %s 保留的身份里没有机型信息，不能开启 DMI 覆盖", sl.ID)
	}
	if tz, _ := model.EffectiveLocale(sl, landing); tz == "" {
		warnings = append(warnings, fmt.Sprintf("槽位和落地 %s 都没有设置时区，槽内将是 UTC；建议按出口所在地区设置（slot add --tz，或 node add --tz）", landing.ID))
	}

	if err := e.st.SaveSlot(sl, true); err != nil {
		return sl, nil, err
	}
	if has {
		warnings = append(warnings, fmt.Sprintf("沿用槽位 %s 之前保留的身份与 HOME", sl.ID))
	} else if err := e.st.SaveIdentity(sl.ID, ident); err != nil {
		return sl, nil, err
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

	wc, err := e.ensureWorker(ctx, slot, ident, landing, opt.Recreate)
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

// seedVersion 计入 worker 的规格摘要：写入容器的身份文件的做法改变时提高它，已有的容器就会被要求重建。
const seedVersion = "1"

// workerDigest 是 worker 容器的规格摘要：容器规格加上创建后写进容器的身份内容。
func workerDigest(spec dockerapi.ContainerSpec, ident model.Identity) string {
	b, _ := json.Marshal(struct {
		Spec      dockerapi.ContainerSpec
		MachineID string
		DMI       map[string]string
		Seed      string
	}{spec, ident.MachineID, ident.DMI, seedVersion})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:32]
}

// ensureWorker 确保 worker 容器存在并在运行。容器以 --network none 启动，此时没有网络。
func (e *Engine) ensureWorker(ctx context.Context, slot model.Slot, ident model.Identity, landing model.Node, recreate bool) (dockerapi.Container, error) {
	var none dockerapi.Container
	name := e.WorkerContainer(slot.ID)
	image := slot.Image
	if image == "" {
		image = e.cl.WorkerImage
	}
	for _, m := range slot.Mounts {
		// 挂载源不存在时 Docker 会以 root 身份在宿主上把它建出来，所以先自己检查。
		if _, err := os.Stat(m.Source); err != nil {
			return none, fmt.Errorf("槽位 %s 的挂载源 %s 不可用：%w", slot.ID, m.Source, err)
		}
		if err := model.CheckMountTarget(m.Target, ident.Home()); err != nil {
			return none, fmt.Errorf("槽位 %s：%w", slot.ID, err)
		}
	}

	tz, lang := model.EffectiveLocale(slot, landing)
	p := workerParams{
		Image: image, Slot: slot, Ident: ident, TZ: tz, Lang: lang,
		HomeDir: e.st.HomeDir(slot.ID), Labels: e.labels(roleWorker, slot.ID),
	}
	etc := e.st.EtcDir(slot.ID)
	if slot.FakeDMI {
		if len(ident.DMI) == 0 {
			return none, fmt.Errorf("槽位 %s 的身份里没有机型信息，不能开启 DMI 覆盖", slot.ID)
		}
		p.DMIDir = filepath.Join(etc, "dmi")
	}
	if slot.FakeProc && slot.CPUs > 0 {
		p.CPUInfo = filepath.Join(etc, "cpuinfo")
	}
	if slot.FakeProc && slot.MemoryBytes > 0 {
		p.MemInfo = filepath.Join(etc, "meminfo")
	}
	spec := workerSpec(p)
	digest := workerDigest(spec, ident)
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
		if err := e.writeHardwareFiles(ctx, image, slot, ident, p); err != nil {
			return none, err
		}
		e.logf("创建 worker 容器 %s（无网络）", name)
		if _, err := e.dk.ContainerCreate(ctx, name, spec); err != nil {
			return none, err
		}
		// 身份文件在第一次启动之前写好；写不进去就不留下一个半成品容器。
		if err := e.seedWorker(ctx, name, slot, ident); err != nil {
			_ = e.dk.ContainerRemove(ctx, name)
			return none, fmt.Errorf("向 worker 容器 %s 写入身份文件失败：%w", name, err)
		}
	}
	if !ok || !c.State.Running {
		if err := e.dk.ContainerStart(ctx, name); err != nil {
			if slot.FakeDMI {
				return none, fmt.Errorf("%w（该槽开启了 DMI 覆盖；宿主没有 %s 时无法使用）", err, dmiPath)
			}
			return none, err
		}
	}
	// 每次都执行：容器可能在管理器之外被重启过，而 Docker 每次启动都会重新生成 resolv.conf。
	if err := e.settleWorker(ctx, name, tz); err != nil {
		_ = e.dk.ContainerStop(ctx, name, 0)
		return none, fmt.Errorf("worker 容器 %s 启动后的整理失败，已停止该容器：%w", name, err)
	}
	return e.dk.ContainerInspect(ctx, name)
}

// seedWorker 把该槽的用户、组和 machine-id 写进刚创建、尚未启动的容器。
// 文件进入容器自己的文件层，重启后仍在；重建容器时重新写入。
func (e *Engine) seedWorker(ctx context.Context, name string, slot model.Slot, ident model.Identity) error {
	read := func(p string) ([]byte, error) {
		b, err := e.dk.CopyFrom(ctx, name, p)
		if dockerapi.IsNotFound(err) {
			return nil, nil
		}
		return b, err
	}
	passwd, err := read("/etc/passwd")
	if err != nil {
		return err
	}
	group, err := read("/etc/group")
	if err != nil {
		return err
	}
	mid := []byte(ident.MachineID + "\n")
	return e.dk.CopyTo(ctx, name, "/", map[string]dockerapi.File{
		"etc/passwd":              {Data: mergePasswd(passwd, ident, slot), Mode: 0o644},
		"etc/group":               {Data: mergeGroup(group, ident, slot), Mode: 0o644},
		"etc/machine-id":          {Data: mid, Mode: 0o444},
		"var/lib/dbus/machine-id": {Data: mid, Mode: 0o444},
	})
}

// settleWorker 在运行中的 worker 里以容器内的 root 身份执行 workerSettleScript。
func (e *Engine) settleWorker(ctx context.Context, name, tz string) error {
	res, err := e.dk.Exec(ctx, name, []string{"/bin/sh", "-c", workerSettleScript},
		dockerapi.ExecOptions{User: "0:0", Env: []string{"CC_DNS=" + workerDNS, "CC_TZ=" + tz}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &exitError{code: res.ExitCode, output: strings.TrimSpace(res.Stderr + res.Stdout)}
	}
	return nil
}

// writeHardwareFiles 生成可选的硬件信息覆盖文件。只在创建容器时生成一次，之后内容不变。
func (e *Engine) writeHardwareFiles(ctx context.Context, image string, slot model.Slot, ident model.Identity, p workerParams) error {
	if p.DMIDir != "" {
		if _, err := e.st.WriteSlotDir(slot.ID, filepath.Base(p.DMIDir), dmiFiles(ident.DMI)); err != nil {
			return err
		}
	}
	if p.CPUInfo == "" && p.MemInfo == "" {
		return nil
	}
	// 从一个同镜像的一次性无网容器里读出真实内容，再按限额改写。
	res, err := e.dk.RunOnce(ctx, dockerapi.ContainerSpec{
		Image:      image,
		Entrypoint: []string{"cat"},
		Cmd:        []string{"/proc/cpuinfo", "/proc/meminfo"},
		Labels:     e.labels(roleHelper, ""),
		HostConfig: dockerapi.HostConfig{NetworkMode: "none", CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"}},
	})
	if err != nil {
		return err
	}
	i := strings.Index(res.Stdout, "MemTotal:")
	if res.ExitCode != 0 || i <= 0 {
		return fmt.Errorf("读取 /proc/cpuinfo 与 /proc/meminfo 失败（退出码 %d）", res.ExitCode)
	}
	if p.CPUInfo != "" {
		if _, err := e.st.WriteSlotFile(slot.ID, filepath.Base(p.CPUInfo), limitCPUInfo([]byte(res.Stdout[:i]), int(math.Ceil(slot.CPUs)))); err != nil {
			return err
		}
	}
	if p.MemInfo != "" {
		if _, err := e.st.WriteSlotFile(slot.ID, filepath.Base(p.MemInfo), limitMemInfo([]byte(res.Stdout[i:]), slot.MemoryBytes)); err != nil {
			return err
		}
	}
	return nil
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

// SlotAddWorkspace 把宿主目录 dir 以相同的路径加进槽位的挂载。只改期望配置；
// 运行中的 worker 要重建后才看得到它（Docker 不能给运行中的容器追加挂载）。
func (e *Engine) SlotAddWorkspace(id, dir string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("slot.mount", id, err, dir) }()

	sl, err := e.st.Slot(id)
	if err != nil {
		return err
	}
	ident, _, err := e.st.Identity(id)
	if err != nil {
		return err
	}
	dir = filepath.Clean(dir)
	if fi, err := os.Stat(dir); err != nil {
		return fmt.Errorf("目录 %s 不可用：%w", dir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s 不是目录", dir)
	}
	if err := model.CheckMountTarget(dir, ident.Home()); err != nil {
		return err
	}
	sl.Mounts = model.AddWorkspace(sl.Mounts, dir)
	if err := sl.Validate(); err != nil {
		return err
	}
	return e.st.SaveSlot(sl, false)
}

// SlotRemoveMount 去掉槽位里目标路径为 target 的挂载。同样只改期望配置。
func (e *Engine) SlotRemoveMount(id, target string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("slot.unmount", id, err, target) }()

	sl, err := e.st.Slot(id)
	if err != nil {
		return err
	}
	kept := sl.Mounts[:0:0]
	for _, m := range sl.Mounts {
		if m.Target != target {
			kept = append(kept, m)
		}
	}
	if len(kept) == len(sl.Mounts) {
		return fmt.Errorf("槽位 %s 没有挂载到 %s 的目录", id, target)
	}
	sl.Mounts = kept
	return e.st.SaveSlot(sl, false)
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
