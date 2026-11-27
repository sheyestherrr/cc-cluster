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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/identity"
	"github.com/sheyestherrr/cc-cluster/internal/mihomo"
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
		for _, t := range g.Transits() {
			if t == id {
				return fmt.Errorf("网关 %s 仍在使用节点 %s 作为中转", g.ID, id)
			}
		}
	}
	slots, err := e.st.Slots()
	if err != nil {
		return err
	}
	for _, s := range slots {
		if s.Landing == id {
			return fmt.Errorf("槽位 %s 仍在使用节点 %s 作为落地", s.ID, id)
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
	if g.EgressNetwork == "" {
		g.EgressNetwork = e.DefaultEgressNetwork()
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		return err
	}
	if err := e.checkTransits(g); err != nil {
		return err
	}
	return e.st.SaveGateway(g, true)
}

// checkTransits 核对网关引用的中转都已登记、角色是中转、有首跳地址。
func (e *Engine) checkTransits(g model.Gateway) error {
	for _, id := range g.Transits() {
		t, err := e.st.Node(id)
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
	return nil
}

// transitChainFailures 给 transits 里的每个中转接上落地 landing 测整条链路，返回接不通的中转 ID。
// 核心在候选之间切换时只看中转自己通不通；能不能接通落地要在登记时另外测。
// 不通的再测一次，免得一次抖动就判成不通。
func (e *Engine) transitChainFailures(ctx context.Context, g model.Gateway, transits []model.Node, landing model.Node) ([]string, error) {
	opt := DelayOptions{URL: g.ConnectivityURLs()[0], Network: g.EgressNetwork, Hosts: map[string]string{}}
	for _, n := range append(append([]model.Node(nil), transits...), landing) {
		if n.ConnectIPv4 != "" && n.ServerIsDomain() {
			opt.Hosts[n.Server()] = n.ConnectIPv4
		}
	}
	pending := transits
	for attempt := 0; attempt < 2 && len(pending) > 0; attempt++ {
		fragments := make([]map[string]any, len(pending))
		for i, n := range pending {
			fragments[i] = n.Proxy
		}
		delays, err := e.measureDelays(ctx, fragments, landing.Proxy, opt)
		if err != nil {
			return nil, err
		}
		var failed []model.Node
		for i, d := range delays {
			if d <= 0 {
				failed = append(failed, pending[i])
			}
		}
		pending = failed
	}
	ids := make([]string, len(pending))
	for i, n := range pending {
		ids[i] = n.ID
	}
	return ids, nil
}

// verifyTransitChains 核对网关 g 的每个中转都能接通它上面各槽位所用的落地；
// 有接不通的就返回错误并指明是哪一对。网关还没有槽位时无从验证，留到槽位启动时。
func (e *Engine) verifyTransitChains(ctx context.Context, g model.Gateway) error {
	slots, err := e.st.Slots()
	if err != nil {
		return err
	}
	users := map[string][]string{}
	var landings []string
	for _, sl := range slots {
		if sl.Gateway != g.ID || sl.Landing == "" {
			continue
		}
		if _, seen := users[sl.Landing]; !seen {
			landings = append(landings, sl.Landing)
		}
		users[sl.Landing] = append(users[sl.Landing], sl.ID)
	}
	if len(landings) == 0 {
		e.logf("网关 %s 暂无槽位，中转到落地的连通性将在槽位启动时验证", g.ID)
		return nil
	}
	var transits []model.Node
	for _, id := range g.Transits() {
		n, err := e.st.Node(id)
		if err != nil {
			return err
		}
		transits = append(transits, n)
	}
	const hint = "可跳过此项验证（命令行加 --skip-check，网页中开启“跳过落地连通性验证”）"
	var problems []string
	for _, lid := range landings {
		landing, err := e.st.Node(lid)
		if err != nil {
			return err
		}
		e.logf("正在验证 %d 个中转到落地 %s 的连通性", len(transits), lid)
		bad, err := e.transitChainFailures(ctx, g, transits, landing)
		if err != nil {
			return fmt.Errorf("验证中转到落地 %s 的连通性时出错，未更换：%w；确认无影响时%s", lid, err, hint)
		}
		if len(bad) > 0 {
			problems = append(problems, fmt.Sprintf("%s 无法连通落地 %s（槽位 %s 使用中）", strings.Join(bad, "、"), lid, strings.Join(users[lid], "、")))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("未更换：中转 %s。请更换为可连通落地的中转（取自订阅的节点可用 sub test <订阅> --landing <落地> 测试整条链路）；落地仅暂时无法连接、确认无影响时%s",
			strings.Join(problems, "；"), hint)
	}
	return nil
}

// warnBackupChains 在槽位启动时验证候选中转能否接通这个槽的落地；只提醒，不拦启动
// （主用的那条链路已经由 checkChain 测过）。
func (e *Engine) warnBackupChains(ctx context.Context, g model.Gateway, landing model.Node) {
	var backups []model.Node
	for _, id := range g.Backups {
		n, err := e.st.Node(id)
		if err != nil {
			e.logf("提醒：未能验证备用中转到落地 %s 的连通性：%v", landing.ID, err)
			return
		}
		backups = append(backups, n)
	}
	bad, err := e.transitChainFailures(ctx, g, backups, landing)
	switch {
	case err != nil:
		e.logf("提醒：未能验证备用中转到落地 %s 的连通性：%v", landing.ID, err)
	case len(bad) > 0:
		e.logf("提醒：备用中转 %s 无法连通落地 %s，切换到这些中转后该槽位将无网络；请用 gateway set-transit 更换", strings.Join(bad, "、"), landing.ID)
	}
}

// TransitOptions 是 SetGatewayTransits 的选项。
type TransitOptions struct {
	// SkipChainCheck 跳过“每个中转都能接通网关上各槽位的落地”这项检查。
	SkipChainCheck bool
}

// TransitChange 是 SetGatewayTransits 的结果。
type TransitChange struct {
	// Reloaded 表示网关在运行，新配置已经生效。
	Reloaded bool
	// Rechecked 是换完之后重新核对过链路与出口的槽位；Failed 是其中没有通过的及原因，
	// 它们不再算作就绪，排查后要重新 slot up。
	Rechecked []string
	Failed    map[string]string
}

// SetGatewayTransits 更换一个网关的中转：transits 的第一个是主用的，其余是候选。
// 网关在运行时在线重载，并让换之前已就绪的槽位重新走一遍链路检查与出口核对
// （出口由落地决定，换中转不应改变它）。
//
// 保存之前先接上网关上各槽位的落地逐个测一遍：有中转接不通落地就不更换。
func (e *Engine) SetGatewayTransits(ctx context.Context, id string, transits []string, opt TransitOptions) (res TransitChange, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return res, err
	}
	defer unlock()
	defer func() { e.record("gateway.transit", id, err, strings.Join(transits, ",")) }()
	g, err := e.st.Gateway(id)
	if err != nil {
		return res, err
	}
	if g.Transit == "" {
		return res, fmt.Errorf("网关 %s 直连落地，未使用中转；如需中转，请另建带中转的网关", id)
	}
	if len(transits) == 0 {
		return res, errors.New("至少指定一个中转")
	}
	g.Transit, g.Backups = transits[0], append([]string(nil), transits[1:]...)
	g.Normalize()
	if err := g.Validate(); err != nil {
		return res, err
	}
	if err := e.checkTransits(g); err != nil {
		return res, err
	}
	if !opt.SkipChainCheck {
		if err := e.verifyTransitChains(ctx, g); err != nil {
			return res, err
		}
	}

	// 先记下现在已接线的槽位，再保存：保存之后它们的摘要就对不上了。
	var ready []string
	if c, ok, ierr := e.inspect(ctx, e.GatewayContainer(id)); ierr != nil {
		return res, ierr
	} else if ok && c.State.Running {
		res.Reloaded = true
		slots, err := e.st.Slots()
		if err != nil {
			return res, err
		}
		for _, sl := range slots {
			if sl.Gateway != id {
				continue
			}
			if s, err := e.slotStatus(ctx, sl); err == nil && s.State == StateReady {
				ready = append(ready, sl.ID)
			}
		}
	}
	if err := e.st.SaveGateway(g, false); err != nil {
		return res, err
	}
	if !res.Reloaded {
		return res, nil
	}
	if _, _, err := e.gatewayUp(ctx, id); err != nil {
		return res, err
	}
	res.Failed = map[string]string{}
	for _, sid := range ready {
		res.Rechecked = append(res.Rechecked, sid)
		if _, uerr := e.slotUp(ctx, sid, UpOptions{skipBackupCheck: true}); uerr != nil {
			res.Failed[sid] = uerr.Error()
			e.logf("提醒：槽位 %s 更换中转后未通过出口核对：%v", sid, uerr)
		}
	}
	return res, nil
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
	if sl.TmuxBridge {
		if sl.TmuxShell == "" {
			sl.TmuxShell = HostShellType()
		}
		if len(e.TmuxSocket(sl.ID)) > maxSocketPath {
			_, err := e.prepareTmuxDir(sl.ID) // 只为取得同一句说明
			return sl, nil, err
		}
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
	var sharing []string
	for _, o := range all {
		if o.ID == sl.ID {
			return sl, nil, fmt.Errorf("槽位 %s 已存在", sl.ID)
		}
		if o.Landing == sl.Landing {
			sharing = append(sharing, o.ID)
		}
		if o.Gateway != sl.Gateway {
			continue
		}
		if p, err := netip.ParsePrefix(o.Subnet); err == nil {
			used = append(used, p)
		}
	}
	if len(sharing) > 0 {
		warnings = append(warnings, fmt.Sprintf("落地 %s 已绑定槽位 %s，这些槽位的出口地址相同；同一落地最多同时为 %d 个槽位接线",
			sl.Landing, strings.Join(sharing, "、"), model.MaxSlotsPerLanding))
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
		return sl, nil, fmt.Errorf("槽位 %s 已有保留的身份，不能再指定 hostname 或用户名", sl.ID)
	}
	if !has {
		if ident, err = identity.New(identity.Options{Hostname: opt.Hostname, User: opt.User}); err != nil {
			return sl, nil, err
		}
	}
	for _, m := range sl.Mounts {
		if err := e.checkMountSource(m.Source); err != nil {
			return sl, nil, err
		}
		if err := model.CheckMountTarget(m.Target, ident.Home()); err != nil {
			return sl, nil, err
		}
	}
	if sl.FakeDMI && len(ident.DMI) == 0 {
		return sl, nil, fmt.Errorf("槽位 %s 保留的身份中没有机型信息，无法开启 DMI 覆盖", sl.ID)
	}
	if tz, _ := model.EffectiveLocale(sl, landing); tz == "" {
		warnings = append(warnings, fmt.Sprintf("槽位与落地 %s 均未设置时区：首次启动时按探测到的出口所在时区设置；探测失败时槽内使用 UTC", landing.ID))
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
	// skipBackupCheck 不验证候选中转能否接通落地（刚换完中转、已经验证过时）。
	skipBackupCheck bool
}

// UpResult 是 SlotUp 的结果。
type UpResult struct {
	ExitIP    string
	Worker    string
	Replumbed bool
	// Exit 是链路探测得到的出口信息（地区、运营商、时区），可能只有 IP。
	Exit exitInfo
	// Bridge 报告宿主 tmux 桥接是否已接通；槽位没有开启桥接时为 false。
	Bridge bool
}

// ExitSummary 返回出口的一行描述，例如 "203.0.113.8（US，AS64500 Example）"。
func (r UpResult) ExitSummary() string {
	if where := exitWhere(r.Exit.Country, r.Exit.Org); where != "" {
		return r.ExitIP + "（" + where + "）"
	}
	return r.ExitIP
}

// exitWhere 把出口的国家与运营商拼成一段，都没有时为空。
func exitWhere(country, org string) string {
	var parts []string
	for _, s := range []string{country, org} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "，")
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
		return res, &Guided{
			Msg: fmt.Sprintf("槽位 %s 已手动断网（%s）", id, orDash(h.Reason)),
			CLI: fmt.Sprintf("；确认后请先执行 slot release %s", id),
			Web: "；确认后请先点击“解除断网”，再启动",
		}
	}
	ident, ok, err := e.st.Identity(id)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, fmt.Errorf("槽位 %s 缺少身份文件", id)
	}
	if err := e.checkLandingLoad(ctx, slot); err != nil {
		return res, err
	}

	p, rt, err := e.gatewayUp(ctx, slot.Gateway)
	if err != nil {
		return res, err
	}
	link, landing := p.links[id], p.landings[id]

	e.logf("经网关回环入口探测槽位 %s 的链路", id)
	if err := e.checkChain(ctx, rt, p, landing); err != nil {
		return res, fmt.Errorf("槽位 %s 的链路不可用，未接线：%w", id, err)
	}
	if len(p.gw.Backups) > 0 && !opt.skipBackupCheck {
		e.warnBackupChains(ctx, p.gw, landing)
	}
	// 落地还没有真实的时区（没设过，或者只有兜底值）时，按探测到的出口所在时区设置并记下，
	// 之后不再随探测结果变动：worker 的时区是容器规格的一部分，变了就要重建。
	wantTZ := landing.TZ == "" || landing.TZFallback
	needTZ := wantTZ && slot.TZ == ""
	exit, err := e.probeChain(ctx, rt, link, p.gw.EchoURLs, needTZ)
	if err != nil {
		return res, fmt.Errorf("槽位 %s 的链路可连通，但出口探测失败，未接线：%w", id, err)
	}
	chainIP, echoURL := exit.IP, exit.URL
	if landing.ExpectExitIP != "" && chainIP != landing.ExpectExitIP {
		return res, fmt.Errorf("槽位 %s 的链路出口为 %s，与落地 %s 指定的 %s 不符，未接线", id, chainIP, landing.ID, landing.ExpectExitIP)
	}
	switch {
	case wantTZ && exit.TZ != "":
		landing.TZ, landing.TZAuto, landing.TZFallback = exit.TZ, true, false
		if err := e.st.SaveNode(landing, false); err != nil {
			return res, err
		}
		p.landings[id] = landing
		e.logf("落地 %s 的时区按出口探测结果设为 %s", landing.ID, exit.TZ)
	case needTZ && landing.TZ == "":
		// 带时区的探测地址都没有应答（多半是被限流）。用兜底值，并标出来，下次再探测。
		landing.TZ, landing.TZAuto, landing.TZFallback = model.FallbackTZ, true, true
		if err := e.st.SaveNode(landing, false); err != nil {
			return res, err
		}
		p.landings[id] = landing
		e.logf("提醒：未探测到出口所在的时区，落地 %s 暂用默认值 %s；下次启动时重新探测，也可用 --tz 为落地指定", landing.ID, model.FallbackTZ)
	case needTZ:
		e.logf("提醒：仍未探测到出口所在的时区，落地 %s 继续使用默认值 %s", landing.ID, landing.TZ)
	}
	e.refreshExitReport(ctx, rt, link, &landing, chainIP, false)
	p.landings[id] = landing

	wc, err := e.ensureWorker(ctx, slot, ident, landing, opt.Recreate)
	if err != nil {
		return res, err
	}
	res.Worker = wc.Name
	if slot.TmuxBridge {
		res.Bridge = e.bridgeUp(ctx, slot, wc.Name)
	}

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
			return res, fmt.Errorf("在槽位 %s 的 worker 侧探测出口失败，已撤线：%w", id, err)
		}
		return res, fmt.Errorf("槽位 %s 的 worker 实际出口为 %s，与链路出口 %s 不符，已撤线", id, workerIP, chainIP)
	}

	res.ExitIP = workerIP
	digest, err := e.slotDigest(slot, ident, p)
	if err != nil {
		return res, err
	}
	err = e.st.SaveApplied(id, model.Applied{
		Digest: digest, WorkerID: wc.ID, GatewayID: rt.id, ExitIP: workerIP,
		AppliedAt: time.Now().UTC(), EchoURLUsed: echoURL,
		ExitCountry: exit.Country, ExitOrg: exit.Org, ExitTZ: exit.TZ,
	})
	_ = e.st.ClearWatch(id) // 重新接线后，之前的检测结果不再适用
	res.Exit = exit
	return res, err
}

// slotDigest 是"这个槽的期望配置"的摘要：槽位、身份、它用到的节点与网关设置。
// 别的槽的增删不改变它。备注只用于展示，不计入：改备注不应让已经接好线的槽位变成“需要重新应用”。
func (e *Engine) slotDigest(slot model.Slot, ident model.Identity, p *gatewayPlan) (string, error) {
	image := slot.Image
	if image == "" {
		image = e.cl.WorkerImage
	}
	landing, gw, transit := p.landings[slot.ID], p.gw, p.transit
	slot.Note, landing.Note, gw.Note = "", "", ""
	if transit != nil {
		t := *transit
		t.Note = ""
		transit = &t
	}
	b, err := json.Marshal(struct {
		Slot     model.Slot
		Identity model.Identity
		Image    string
		Landing  model.Node
		Transit  *model.Node
		Gateway  model.Gateway
		GWImage  string
	}{slot, ident, image, landing, transit, gw, p.image})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// seedVersion 计入 worker 的规格摘要：写入容器的身份文件的做法改变时提高它，已有的容器就会被要求重建。
const seedVersion = "2"

// profilePathFile 是写进 worker 的 profile 片段（路径相对容器根），内容是 profilePathScript。
//
// 容器的环境变量里 PATH 以 $HOME/.local/bin 开头（见 workerSpec），但登录 shell 会读 /etc/profile，
// 而 Debian 等发行版的 /etc/profile 把 PATH 重设成固定的值，装在 HOME 里的程序（Claude Code 的原生安装）
// 就找不到了。/etc/profile 随后会读 /etc/profile.d/*.sh，在这里把它补回去；镜像没有这套机制时这个文件不起作用。
const (
	profilePathFile   = "etc/profile.d/local-bin.sh"
	profilePathScript = `case ":$PATH:" in
*":$HOME/.local/bin:"*) ;;
*) PATH="$HOME/.local/bin:$PATH" ;;
esac
export PATH
`
)

// profilePromptFile 是另一个 profile 片段：交互的 bash 用两行的提示符，
// 第一行是用户名、当前目录和时间，第二行是输入处。不显示 hostname（它是按机型生成的一长串）。
// /etc/profile 先读 /etc/bash.bashrc 再读 profile.d，所以这里的设置盖过发行版的默认值；
// 使用者在自己的 ~/.bashrc 或 ~/.profile 里另设 PS1 的话以他的为准。
// 没有计入 seedVersion：已有的容器不必为它重建，下次重建时自然带上。
const (
	profilePromptFile   = "etc/profile.d/prompt.sh"
	profilePromptScript = `if [ -n "${BASH_VERSION-}" ] && [ -n "${PS1-}" ]; then
	PS1='\n\[\e[1;32m\]\u\[\e[0m\] \[\e[1;34m\]\w\[\e[0m\] \[\e[2m\]\A\[\e[0m\]\n❯ '
fi
`
)

// workerDigest 是 worker 容器的规格摘要：容器规格加上创建后写进容器的身份内容。
// extra 是另外写进容器文件层的文件（开启桥接时的配置）；为空时不计入，摘要与没有这一项时相同。
func workerDigest(spec dockerapi.ContainerSpec, ident model.Identity, extra map[string][]byte) string {
	b, _ := json.Marshal(struct {
		Spec      dockerapi.ContainerSpec
		MachineID string
		DMI       map[string]string
		Seed      string
		Extra     map[string][]byte `json:",omitempty"`
	}{spec, ident.MachineID, ident.DMI, seedVersion, extra})
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
		// 登记时检查过的路径之后可能被换成符号链接，每次都按它实际指向的位置重新核对。
		if err := e.checkMountSource(m.Source); err != nil {
			return none, fmt.Errorf("槽位 %s：%w", slot.ID, err)
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
	linkMounts(p.HomeDir, ident.Home(), slot.Mounts)
	etc := e.st.EtcDir(slot.ID)
	if slot.FakeDMI {
		if len(ident.DMI) == 0 {
			return none, fmt.Errorf("槽位 %s 的身份中没有机型信息，无法开启 DMI 覆盖", slot.ID)
		}
		p.DMIDir = filepath.Join(etc, "dmi")
	}
	if slot.FakeProc && slot.CPUs > 0 {
		p.CPUInfo = filepath.Join(etc, "cpuinfo")
		p.CPUOnline, p.FakeCPUs = filepath.Join(etc, "cpu-online"), int(math.Ceil(slot.CPUs))
	}
	if slot.FakeProc && slot.MemoryBytes > 0 {
		p.MemInfo = filepath.Join(etc, "meminfo")
	}
	extra, err := bridgeFiles(slot, e.TmuxSocket(slot.ID))
	if err != nil {
		return none, fmt.Errorf("槽位 %s：%w", slot.ID, err)
	}
	if slot.TmuxBridge {
		if p.TmuxDir, err = e.prepareTmuxDir(slot.ID); err != nil {
			return none, err
		}
	}
	spec := workerSpec(p)
	digest := workerDigest(spec, ident, extra)
	spec.Labels[labelSpec] = digest

	c, ok, err := e.inspect(ctx, name)
	if err != nil {
		return none, err
	}
	if ok && c.Config.Labels[labelSpec] != digest {
		if !recreate {
			return none, &Guided{
				Msg: fmt.Sprintf("槽位 %s 的 worker 容器与当前配置不一致；重建将结束容器内的进程", slot.ID),
				CLI: fmt.Sprintf("，确认后请执行 slot up %s --recreate", slot.ID),
				Web: "，确认后请在该槽位的菜单中选择“重建 worker 并接线”",
			}
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
			return none, fmt.Errorf("本机未找到 worker 镜像 %s", image)
		}
		if err := e.writeHardwareFiles(ctx, image, slot, ident, p); err != nil {
			return none, err
		}
		e.logf("创建 worker 容器 %s（无网络）", name)
		if _, err := e.dk.ContainerCreate(ctx, name, spec); err != nil {
			return none, err
		}
		// 身份文件在第一次启动之前写好；写不进去就不留下一个半成品容器。
		if err := e.seedWorker(ctx, name, slot, ident, extra); err != nil {
			_ = e.dk.ContainerRemove(ctx, name)
			return none, fmt.Errorf("向 worker 容器 %s 写入身份文件失败：%w", name, err)
		}
	}
	if !ok || !c.State.Running {
		if err := e.dk.ContainerStart(ctx, name); err != nil {
			if slot.FakeDMI {
				return none, fmt.Errorf("%w（该槽位已开启 DMI 覆盖；宿主不存在 %s 时无法使用）", err, dmiPath)
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

// seedWorker 把该槽的用户、组、machine-id 和补全 PATH 的 profile 片段写进刚创建、尚未启动的容器。
// 文件进入容器自己的文件层，重启后仍在；重建容器时重新写入。extra 是另外要写入的文件（路径相对容器根）。
func (e *Engine) seedWorker(ctx context.Context, name string, slot model.Slot, ident model.Identity, extra map[string][]byte) error {
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
	files := map[string]dockerapi.File{
		"etc/passwd":              {Data: mergePasswd(passwd, ident, slot), Mode: 0o644},
		"etc/group":               {Data: mergeGroup(group, ident, slot), Mode: 0o644},
		"etc/machine-id":          {Data: mid, Mode: 0o444},
		"var/lib/dbus/machine-id": {Data: mid, Mode: 0o444},
		profilePathFile:           {Data: []byte(profilePathScript), Mode: 0o644},
		profilePromptFile:         {Data: []byte(profilePromptScript), Mode: 0o644},
	}
	for p, data := range extra {
		files[p] = dockerapi.File{Data: data, Mode: 0o644}
	}
	return e.dk.CopyTo(ctx, name, "/", files)
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
		if _, err := e.st.WriteSlotFile(slot.ID, filepath.Base(p.CPUInfo), limitCPUInfo([]byte(res.Stdout[:i]), p.FakeCPUs)); err != nil {
			return err
		}
		if _, err := e.st.WriteSlotFile(slot.ID, filepath.Base(p.CPUOnline), []byte(cpuRange(p.FakeCPUs)+"\n")); err != nil {
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
		return fmt.Errorf("添加策略路由：%w", err)
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

// exitInfo 是一次出口探测的结果。除 IP 外的各项只有探测地址返回 JSON（如 ipinfo.io）时才有。
type exitInfo struct {
	IP      string
	TZ      string
	Country string
	Org     string
	URL     string // 给出这个结果的探测地址
	plain   bool   // 应答是纯文本，只有 IP
}

// parseExit 解析探测地址的应答：JSON（带 ip 字段，可带时区等）或者含有 IPv4 地址的纯文本。
// JSON 的字段名按几家常见的写法都认：时区可以是字符串，也可以是带 id 的对象；
// 国家优先取两个字母的代码；运营商可以在 org、organization 或 connection 里。
func parseExit(body string) (exitInfo, bool) {
	var info exitInfo
	if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "{") {
		var j struct {
			IP           string          `json:"ip"`
			Timezone     json.RawMessage `json:"timezone"`
			Country      string          `json:"country"`
			CountryCode  string          `json:"country_code"`
			Org          string          `json:"org"`
			Organization string          `json:"organization"`
			Connection   struct {
				Org string `json:"org"`
				ISP string `json:"isp"`
			} `json:"connection"`
		}
		if json.Unmarshal([]byte(trimmed), &j) != nil {
			return info, false
		}
		ip, ok := parseExitIP(j.IP)
		if !ok {
			return info, false
		}
		info.IP = ip
		info.Country = truncate(firstNonEmpty(j.CountryCode, j.Country), 16)
		info.Org = truncate(firstNonEmpty(j.Org, j.Organization, j.Connection.Org, j.Connection.ISP), 80)
		var tz string
		if json.Unmarshal(j.Timezone, &tz) != nil {
			var obj struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(j.Timezone, &obj) == nil {
				tz = obj.ID
			}
		}
		// 时区会进入容器的环境变量和启动脚本，写法不对的一律不用。
		if model.CheckTZ(tz) == nil {
			info.TZ = tz
		}
		return info, true
	}
	// Cloudflare 的 trace：每行一个 名=值，其中 ip 是出口，loc 是两个字母的国家代码。
	if kv := traceFields(body); kv["ip"] != "" {
		if ip, ok := parseExitIP(kv["ip"]); ok {
			info.IP, info.plain = ip, true
			if countryCodeRe.MatchString(kv["loc"]) {
				info.Country = kv["loc"]
			}
			return info, true
		}
	}
	ip, ok := parseExitIP(body)
	info.IP, info.plain = ip, true
	return info, ok
}

var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// traceFields 把 名=值 逐行排列的应答拆成映射；不是这种格式时返回空映射。
func traceFields(body string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t{") {
			continue
		}
		kv[k] = v
	}
	return kv
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// checkLandingLoad 限制同一个落地同时接线的槽位数。哪个槽位用哪个落地由用户配置，
// 登记多少个都可以；这里只看当前已经接着线的。
func (e *Engine) checkLandingLoad(ctx context.Context, slot model.Slot) error {
	all, err := e.st.Slots()
	if err != nil {
		return err
	}
	var using []string
	for _, o := range all {
		if o.ID == slot.ID || o.Landing != slot.Landing {
			continue
		}
		st, err := e.slotStatus(ctx, o)
		if err != nil {
			return err
		}
		if st.State == StateReady || st.State == StatePending {
			using = append(using, o.ID)
		}
	}
	if len(using) >= e.landingLimit() {
		return fmt.Errorf("落地 %s 已有 %d 个槽位接线（%s），同一落地最多同时为 %d 个槽位接线；请先停止其中一个（slot stop），或为槽位 %s 更换落地",
			slot.Landing, len(using), strings.Join(using, "、"), e.landingLimit(), slot.ID)
	}
	return nil
}

// proxyDelay 让网关里的核心经指定的代理访问 urls 中的一个，返回耗时（毫秒）。
// 它走的是核心的控制接口，不经过分流规则，所以可以单独测中转这一跳。
func (e *Engine) proxyDelay(ctx context.Context, rt *gatewayRuntime, proxy string, urls []string) (int, error) {
	var last error
	for _, u := range urls {
		query := url.Values{"url": {u}, "timeout": {"5000"}}
		out, err := e.sh(ctx, rt.name, fmt.Sprintf("curl -sS -m 12 --unix-socket %s 'http://localhost/proxies/%s/delay?%s'",
			mihomo.SocketPath, url.PathEscape(proxy), query.Encode()))
		if err != nil {
			last = err
			continue
		}
		var res struct {
			Delay   int    `json:"delay"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &res) == nil && res.Delay > 0 {
			return res.Delay, nil
		}
		last = fmt.Errorf("访问 %s 失败", u)
	}
	return 0, last
}

// checkChain 分段检查链路的连通性：先看经整条链路（中转 → 落地）能否访问一个常见地址；
// 不行再单独测中转，据此指出是哪一段不通。出口探测在它之后进行。
func (e *Engine) checkChain(ctx context.Context, rt *gatewayRuntime, p *gatewayPlan, landing model.Node) error {
	urls := p.gw.ConnectivityURLs()
	var err error
	for attempt := 0; attempt < 2; attempt++ { // 配置刚重载时第一次可能还没就绪
		if _, err = e.proxyDelay(ctx, rt, mihomo.LandingName(landing.ID), urls); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if p.transit == nil {
		return fmt.Errorf("经落地 %s 无法访问 %s：落地无法连接，或其协议、账号配置有误", landing.ID, strings.Join(urls, "、"))
	}
	if _, terr := e.proxyDelay(ctx, rt, mihomo.TransitName, urls); terr != nil {
		if len(p.gw.Backups) > 0 {
			return fmt.Errorf("中转不可用：经主用 %s 和备用 %s 均无法访问 %s；请更换中转（gateway set-transit）", p.transit.ID, strings.Join(p.gw.Backups, "、"), strings.Join(urls, "、"))
		}
		return fmt.Errorf("中转 %s 不可用（经其无法访问 %s）；请更换中转，取自订阅的可用 sub test 查找可用节点", p.transit.ID, strings.Join(urls, "、"))
	}
	hint := "请更换中转"
	if p.transit.Source != nil {
		hint = fmt.Sprintf("请用 sub test %s --landing %s 测试整条链路，更换为可连通落地的中转", p.transit.Source.Sub, landing.ID)
	}
	return fmt.Errorf("中转 %s 自身可用，但经其无法连接落地 %s：该中转可能不允许到落地的连接，或落地不接受来自该中转的连接。%s；也可改为直连落地（使用不带 --transit 的网关）",
		p.transit.ID, landing.ID, hint)
}

// probeChain 经该槽所用链路在网关回环上的入口访问探测地址，返回出口信息。
//
// needTZ 为 false 时取第一个给出 IPv4 的结果。为 true 时（槽位和落地都还没有时区）
// 继续往后找一个带时区的；找不到就用第一个结果，那时只有 IP。纯文本的后备地址排在最后，
// 轮到它们就不再往下试：它们都不带时区。
func (e *Engine) probeChain(ctx context.Context, rt *gatewayRuntime, link netplan.Link, urls []string, needTZ bool) (exitInfo, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		var first exitInfo
		for _, u := range urls {
			out, err := e.sh(ctx, rt.name, fmt.Sprintf("curl -sS -m 10 -x socks5h://127.0.0.1:%d '%s'", link.ProbePort(), u))
			if err != nil {
				last = err
				continue
			}
			info, ok := parseExit(out)
			if !ok {
				last = fmt.Errorf("%s 的响应中没有 IPv4 地址", u)
				continue
			}
			info.URL = u
			if info.TZ != "" || !needTZ {
				return info, nil
			}
			if first.IP == "" {
				first = info
			}
			if info.plain {
				break
			}
		}
		if first.IP != "" {
			return first, nil
		}
		select {
		case <-ctx.Done():
			return exitInfo{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return exitInfo{}, last
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
		if info, ok := parseExit(out); ok {
			return info.IP, nil
		}
		last = fmt.Errorf("%s 的响应中没有 IPv4 地址", u)
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
	if err := model.ValidateHoldReason(reason); err != nil {
		return err
	}
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
		return fmt.Errorf("撤线失败，槽位 %s 的网卡 %s 仍在网关中：%w", slot.ID, link.GatewayIf(), err)
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
	// 该槽的宿主 tmux 还在运行时：连 HOME 一起删会把它的 socket 也删掉，留下一个接不上的 server，所以先拦住；
	// 只删登记时不动它（停止槽位与结束宿主上的进程是两件事），给出提示。
	if e.TmuxAlive(ctx, id) {
		if purge {
			return &Guided{
				Msg: fmt.Sprintf("槽位 %s 的宿主 tmux 仍在运行，其中可能有正在执行的程序", id),
				CLI: fmt.Sprintf("；请先执行 cc-cluster tmux %s --kill 结束，再删除槽位", id),
				Web: fmt.Sprintf("；请先在终端执行 cc-cluster tmux %s --kill 结束，再删除槽位", id),
			}
		}
		e.logf("注意：槽位 %s 的宿主 tmux 仍在运行，未随槽位删除；socket 位于 %s", id, e.TmuxSocket(id))
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
	// 直出槽位专用的出口节点随槽位一起删掉（它只为这个槽登记，见 PrepareDirect）。
	if n, nerr := e.st.Node(slot.Landing); nerr == nil && n.Direct && n.ID == id+"-exit" {
		if err := e.st.DeleteNode(n.ID); err != nil {
			e.logf("提醒：未能删除槽位 %s 的出口节点 %s：%v", id, n.ID, err)
		}
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
	if err := e.checkMountSource(dir); err != nil {
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
	if ident, ok, _ := e.st.Identity(id); ok {
		unlinkMount(e.st.HomeDir(id), ident.Home(), target)
	}
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
