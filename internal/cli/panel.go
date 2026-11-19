package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

// menuItem 是面板里的一项。
type menuItem struct {
	label string
	run   func(ctx context.Context) error
}

// panel 是不带参数运行 cc-cluster 时的交互面板：列出现状，按编号选择要做的事。
// 每一项最终调用的都是与对应命令相同的实现。
func (a *app) panel(ctx context.Context) error {
	if !a.interactive {
		return errors.New("交互面板需要在终端里运行；非交互环境请直接使用各个命令（cc-cluster help）")
	}
	if err := a.panelDataDir(); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	for {
		a.panelStatus(ctx)
		items := []menuItem{
			{"进入槽位（当前目录 " + cwd + "）", func(ctx context.Context) error { return a.enter(ctx, enterRequest{Dir: cwd}) }},
			{"启动 Claude（当前目录）", func(ctx context.Context) error { return a.enter(ctx, enterRequest{Dir: cwd, Claude: true}) }},
			{"槽位：启动、停止、断网、挂载、删除", a.menuSlot},
			{"新建槽位", a.wizardSlot},
			{"网关：新建、启动、停止、预览配置", a.menuGateway},
			{"节点与订阅：导入订阅、添加节点、刷新", a.menuNode},
			{"环境检查", func(ctx context.Context) error { return a.cmdDoctor(ctx, nil) }},
			{"打开网页", func(ctx context.Context) error { return a.cmdWeb(ctx, nil) }},
		}
		done, err := a.runMenu(ctx, "要做什么？", items)
		if done {
			return err
		}
	}
}

// runMenu 显示一个菜单并执行选中的项。返回 done 为 true 表示用户选择了退出这一层。
// 某一项执行出错时只把错误打印出来，然后回到菜单。
func (a *app) runMenu(ctx context.Context, title string, items []menuItem) (done bool, err error) {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	fmt.Fprintln(a.out)
	i, err := a.choose(title, labels, -1)
	if err != nil {
		return true, nil
	}
	// 常驻的命令（网页）自己决定运行多久，其余操作有统一的时限。
	opCtx, cancel := ctx, context.CancelFunc(func() {})
	if !strings.HasPrefix(items[i].label, "打开网页") {
		opCtx, cancel = context.WithTimeout(ctx, opTimeout)
	}
	defer cancel()
	fmt.Fprintln(a.out)
	if err := items[i].run(opCtx); err != nil && !errors.Is(err, errCancelled) {
		fmt.Fprintf(a.errw, "错误：%v\n", err)
	}
	return false, nil
}

// panelDataDir 确保有一个已初始化的数据目录；没有就带着用户建一个。
func (a *app) panelDataDir() error {
	if a.dataDir == "" {
		fmt.Fprintf(a.out, "还没有指定数据目录。它保存节点、网关、槽位的配置和每个槽的 HOME。\n")
		dir, err := a.askRequired("数据目录的路径")
		if err != nil {
			return err
		}
		if a.dataDir, err = filepath.Abs(dir); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "以后免得每次输入，可以把这一行加进 shell 的启动文件：\n  export %s=%s\n\n", EnvDataDir, a.dataDir)
	}
	if _, err := store.Open(a.dataDir); err == nil {
		return nil
	}
	ok, err := a.confirm(fmt.Sprintf("数据目录 %s 还没有初始化。现在初始化吗？", a.dataDir), true)
	if err != nil || !ok {
		return errCancelled
	}
	return a.cmdInit(nil)
}

// panelStatus 打印网关与槽位的现状。连不上 Docker 时只说明原因，面板照常可用。
func (a *app) panelStatus(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	fmt.Fprintf(a.out, "\n== cc-cluster · 数据目录 %s ==\n", a.dataDir)
	eng, _, err := a.open(ctx, true)
	if err != nil {
		fmt.Fprintf(a.out, "（无法读取现状：%v）\n", err)
		return
	}
	if err := a.printGateways(ctx, eng); err != nil {
		fmt.Fprintf(a.out, "（无法读取网关现状：%v）\n", err)
	}
	fmt.Fprintln(a.out)
	if err := a.printSlots(ctx, eng); err != nil {
		fmt.Fprintf(a.out, "（无法读取槽位现状：%v）\n", err)
	}
}

// ---- 槽位 ----

func (a *app) menuSlot(ctx context.Context) error {
	eng, st, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	id, err := a.pickSlot(ctx, eng, "操作哪个槽位？")
	if err != nil {
		return err
	}
	items := []menuItem{
		{"启动 / 重新接线", func(ctx context.Context) error { return a.upSlot(ctx, eng, id) }},
		{"停止（保留 HOME 与身份）", func(ctx context.Context) error { return eng.SlotStop(ctx, id) }},
		{"立即断网并保持", func(ctx context.Context) error {
			reason, err := a.ask("原因（可不填）", "")
			if err != nil {
				return err
			}
			return eng.SlotHold(ctx, id, reason)
		}},
		{"解除断网标记", func(ctx context.Context) error { return eng.SlotRelease(id) }},
		{"查看挂载的目录", func(ctx context.Context) error { return a.slotMounts([]string{id}) }},
		{"去掉一个挂载", func(ctx context.Context) error {
			sl, err := st.Slot(id)
			if err != nil {
				return err
			}
			if len(sl.Mounts) == 0 {
				return errors.New("这个槽位没有挂载任何目录")
			}
			labels := make([]string, len(sl.Mounts))
			for i, m := range sl.Mounts {
				labels[i] = m.Source + " → " + m.Target
			}
			i, err := a.choose("去掉哪一个？", labels, -1)
			if err != nil {
				return err
			}
			return a.slotUnmount([]string{id, sl.Mounts[i].Target})
		}},
		{"删除槽位（保留 HOME 与身份，之后同名重建可沿用）", func(ctx context.Context) error {
			ok, err := a.confirm("确认删除槽位 "+id+"？", false)
			if err != nil || !ok {
				return errCancelled
			}
			return eng.SlotRemove(ctx, id, false)
		}},
	}
	_, err = a.runMenu(ctx, "对槽位 "+id+" 做什么？", items)
	return err
}

// upSlot 启动槽位；容器需要重建时先征得同意。
func (a *app) upSlot(ctx context.Context, eng *engine.Engine, id string) error {
	res, err := eng.SlotUp(ctx, id, engine.UpOptions{})
	if err != nil && strings.Contains(err.Error(), "--recreate") {
		ok, cerr := a.confirm("槽位 "+id+" 的 worker 与期望配置不一致，需要重建：HOME 保留，容器内正在运行的进程会结束。继续吗？", false)
		if cerr != nil || !ok {
			return errCancelled
		}
		res, err = eng.SlotUp(ctx, id, engine.UpOptions{Recreate: true})
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "槽位 %s 已就绪：出口 %s（已从 worker 的位置复测）\n", id, res.ExitSummary())
	return nil
}

func (a *app) wizardSlot(ctx context.Context) error {
	eng, st, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	gws, err := st.Gateways()
	if err != nil {
		return err
	}
	nodes, err := st.Nodes()
	if err != nil {
		return err
	}
	var landings []model.Node
	for _, n := range nodes {
		if n.Role == model.RoleLanding {
			landings = append(landings, n)
		}
	}
	if len(gws) == 0 || len(landings) == 0 {
		return errors.New("新建槽位之前需要先有一个网关和至少一个落地节点（在主菜单的“网关”和“节点与订阅”里添加）")
	}

	sl := model.Slot{UID: os.Getuid(), GID: os.Getgid()}
	if sl.ID, err = a.askRequired("槽位的名字（小写字母、数字、连字符）"); err != nil {
		return err
	}
	labels := make([]string, len(gws))
	for i, g := range gws {
		labels[i] = g.ID + "（中转 " + orDash(g.Transit) + "）"
	}
	i, err := a.choose("接到哪个网关？", labels, 0)
	if err != nil {
		return err
	}
	sl.Gateway = gws[i].ID
	labels = make([]string, len(landings))
	for i, n := range landings {
		labels[i] = fmt.Sprintf("%s（%s:%d，时区 %s）", n.ID, n.Server(), n.Port(), orDash(n.TZ))
	}
	if i, err = a.choose("绑定哪个落地（它就是这个槽的出口）？", labels, -1); err != nil {
		return err
	}
	sl.Landing = landings[i].ID
	if landings[i].TZ == "" {
		if sl.TZ, err = a.ask("时区，按出口所在地区填，例如 Asia/Tokyo（不填则为 UTC）", ""); err != nil {
			return err
		}
	}
	mem, err := a.ask("内存上限，例如 8g（不填不限）", "")
	if err != nil {
		return err
	}
	if sl.MemoryBytes, err = model.ParseBytes(mem); err != nil {
		return err
	}
	cpus, err := a.ask("CPU 上限（核数，不填不限）", "")
	if err != nil {
		return err
	}
	if cpus != "" {
		if sl.CPUs, err = strconv.ParseFloat(cpus, 64); err != nil {
			return fmt.Errorf("CPU 上限 %q 不是数字", cpus)
		}
	}
	cwd, _ := os.Getwd()
	if ok, err := a.confirm("把当前目录 "+cwd+" 挂进去吗？（以后在别的目录里进入时也可以再加）", true); err != nil {
		return err
	} else if ok {
		sl.Mounts = []model.Mount{{Source: cwd, Target: cwd}}
	}
	var opt engine.SlotOptions
	if ok, err := a.confirm("设置高级选项（用户名、硬件信息覆盖）吗？", false); err != nil {
		return err
	} else if ok {
		if opt.User, err = a.ask("worker 内的用户名（不填随机挑一个）", ""); err != nil {
			return err
		}
		if sl.FakeDMI, err = a.confirm("用该槽自己的机型信息覆盖 DMI（默认是遮成空的）？", false); err != nil {
			return err
		}
		if sl.CPUs > 0 || sl.MemoryBytes > 0 {
			if sl.FakeProc, err = a.confirm("让 /proc/cpuinfo 与 /proc/meminfo 按上面的限额显示（静态内容）？", false); err != nil {
				return err
			}
		}
	}

	out, warnings, err := eng.AddSlot(ctx, sl, opt)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintf(a.errw, "提醒：%s\n", w)
	}
	ident, _, _ := st.Identity(out.ID)
	fmt.Fprintf(a.out, "已登记槽位 %s：用户 %s，主机名 %s，网段 %s\n", out.ID, ident.UserName(), ident.Hostname, out.Subnet)
	if ok, err := a.confirm("现在启动它吗？", true); err != nil || !ok {
		return err
	}
	withDocker, _, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	return a.upSlot(ctx, withDocker, out.ID)
}

// ---- 网关 ----

func (a *app) menuGateway(ctx context.Context) error {
	items := []menuItem{
		{"新建网关", a.wizardGateway},
		{"启动 / 对齐网关", func(ctx context.Context) error {
			id, err := a.pickGateway(ctx)
			if err != nil {
				return err
			}
			return a.cmdGateway(ctx, []string{"up", id})
		}},
		{"停止网关", func(ctx context.Context) error {
			id, err := a.pickGateway(ctx)
			if err != nil {
				return err
			}
			args := []string{"down", id}
			if ok, err := a.confirm("它上面还有运行中的槽位时也停止吗？（这些槽会断网）", false); err != nil {
				return err
			} else if ok {
				args = append(args, "--force")
			}
			return a.cmdGateway(ctx, args)
		}},
		{"预览核心配置（隐藏凭据）", func(ctx context.Context) error {
			id, err := a.pickGateway(ctx)
			if err != nil {
				return err
			}
			return a.cmdRender([]string{id})
		}},
		{"删除网关", func(ctx context.Context) error {
			id, err := a.pickGateway(ctx)
			if err != nil {
				return err
			}
			return a.cmdGateway(ctx, []string{"rm", id})
		}},
	}
	_, err := a.runMenu(ctx, "网关", items)
	return err
}

func (a *app) pickGateway(ctx context.Context) (string, error) {
	_, st, err := a.open(ctx, false)
	if err != nil {
		return "", err
	}
	gws, err := st.Gateways()
	if err != nil {
		return "", err
	}
	switch len(gws) {
	case 0:
		return "", errors.New("还没有网关")
	case 1:
		return gws[0].ID, nil
	}
	labels := make([]string, len(gws))
	for i, g := range gws {
		labels[i] = g.ID + "（中转 " + orDash(g.Transit) + "）"
	}
	i, err := a.choose("哪个网关？", labels, -1)
	if err != nil {
		return "", err
	}
	return gws[i].ID, nil
}

func (a *app) wizardGateway(ctx context.Context) error {
	eng, st, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	nodes, err := st.Nodes()
	if err != nil {
		return err
	}
	labels := []string{"不用中转：直连落地（专用网关，只能接一个槽）"}
	var transits []model.Node
	for _, n := range nodes {
		if n.Role == model.RoleTransit {
			transits = append(transits, n)
			labels = append(labels, fmt.Sprintf("中转 %s（%s:%d）", n.ID, n.Server(), n.Port()))
		}
	}
	g := model.Gateway{}
	if g.ID, err = a.ask("网关的名字", "main"); err != nil {
		return err
	}
	def := 0
	if len(transits) > 0 {
		def = 1
	}
	i, err := a.choose("这个网关的流量先经过哪个中转？", labels, def)
	if err != nil {
		return err
	}
	if i > 0 {
		g.Transit = transits[i-1].ID
	}
	if err := eng.AddGateway(g); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已登记网关 %s；第一次启动槽位时它会自动启动\n", g.ID)
	return nil
}

// ---- 节点与订阅 ----

func (a *app) menuNode(ctx context.Context) error {
	items := []menuItem{
		{"导入订阅（Clash 格式的订阅地址）", func(ctx context.Context) error {
			id, err := a.ask("给这个订阅起个名字", "sub")
			if err != nil {
				return err
			}
			url, err := a.askSecret("订阅地址")
			if err != nil {
				return err
			}
			if err := a.addSub(ctx, id, url); err != nil {
				return err
			}
			if ok, err := a.confirm("现在从里面选一个节点吗？", true); err != nil || !ok {
				return err
			}
			return a.wizardNodeFromSub(ctx, id)
		}},
		{"从订阅里选一个节点", func(ctx context.Context) error {
			id, err := a.pickSub(ctx)
			if err != nil {
				return err
			}
			return a.wizardNodeFromSub(ctx, id)
		}},
		{"手工添加节点（SOCKS5 / HTTP）", a.wizardNodeManual},
		{"测订阅里各节点的延迟", func(ctx context.Context) error {
			id, err := a.pickSub(ctx)
			if err != nil {
				return err
			}
			return a.cmdSub(ctx, []string{"test", id})
		}},
		{"刷新订阅", func(ctx context.Context) error {
			id, err := a.pickSub(ctx)
			if err != nil {
				return err
			}
			return a.cmdSub(ctx, []string{"refresh", id})
		}},
		{"列出节点", func(ctx context.Context) error { return a.nodeList(nil) }},
		{"列出订阅", func(ctx context.Context) error { return a.cmdSub(ctx, []string{"list"}) }},
	}
	_, err := a.runMenu(ctx, "节点与订阅", items)
	return err
}

func (a *app) pickSub(ctx context.Context) (string, error) {
	_, st, err := a.open(ctx, false)
	if err != nil {
		return "", err
	}
	subs, err := st.Subs()
	if err != nil {
		return "", err
	}
	switch len(subs) {
	case 0:
		return "", errors.New("还没有订阅；先导入一个")
	case 1:
		return subs[0].ID, nil
	}
	labels := make([]string, len(subs))
	for i, s := range subs {
		labels[i] = fmt.Sprintf("%s（%d 个节点）", s.ID, len(s.Proxies))
	}
	i, err := a.choose("哪个订阅？", labels, -1)
	if err != nil {
		return "", err
	}
	return subs[i].ID, nil
}

// askRole 询问节点的角色，并在落地时补问出口相关的设置。
func (a *app) askRole(n *model.Node, defLanding bool) error {
	def := 0
	if defLanding {
		def = 1
	}
	i, err := a.choose("这个节点的角色？", []string{"中转：多个槽共用的第一跳", "落地：某个槽的出口"}, def)
	if err != nil {
		return err
	}
	n.Role = model.RoleTransit
	if i == 1 {
		n.Role = model.RoleLanding
		if n.ExpectExitIP, err = a.ask("预期的出口 IPv4（填了就必须与实测一致；不填不检查）", ""); err != nil {
			return err
		}
		if n.TZ, err = a.ask("出口所在地区的时区，例如 Asia/Tokyo（不填则首次启动槽位时按探测到的出口所在地设置）", ""); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) wizardNodeFromSub(ctx context.Context, subID string) error {
	eng, st, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	n := model.Node{}
	if n.ID, err = a.askRequired("给这个节点起个名字（小写字母、数字、连字符）"); err != nil {
		return err
	}
	if err := a.askRole(&n, false); err != nil {
		return err
	}
	// 挑中转时，已有落地的话接上它测整条链路：节点自己能用不代表它肯转发到落地。
	landing := ""
	if n.Role == model.RoleTransit {
		nodes, err := st.Nodes()
		if err != nil {
			return err
		}
		items := []string{}
		for _, o := range nodes {
			if o.Role == model.RoleLanding {
				items = append(items, o.ID)
			}
		}
		if len(items) > 0 {
			i, err := a.choose("这个中转准备接哪个落地？测速时会接上它测整条链路", append(items, "先不指定，只测节点本身"), 0)
			if err != nil {
				return err
			}
			if i < len(items) {
				landing = items[i]
			}
		}
	}
	name, err := a.pickSubNode(ctx, subID, n.Role, landing)
	if err != nil {
		return err
	}
	if err := eng.NodeFromSub(ctx, n, subID, name); err != nil {
		return err
	}
	return a.reportNode(st, n.ID)
}

func (a *app) wizardNodeManual(ctx context.Context) error {
	eng, st, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	n := model.Node{}
	if n.ID, err = a.askRequired("节点的名字（小写字母、数字、连字符）"); err != nil {
		return err
	}
	if err := a.askRole(&n, true); err != nil {
		return err
	}
	types := []string{"socks5", "http"}
	i, err := a.choose("代理类型？（其他协议请用 node add --file 给出 Mihomo 片段）", types, 0)
	if err != nil {
		return err
	}
	server, err := a.askRequired("服务器地址")
	if err != nil {
		return err
	}
	portText, err := a.askRequired("端口")
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("端口 %q 不是数字", portText)
	}
	n.Proxy = map[string]any{"type": types[i], "server": server, "port": port}
	user, err := a.ask("用户名（没有就直接回车）", "")
	if err != nil {
		return err
	}
	if user != "" {
		n.Proxy["username"] = user
		pw, err := a.askSecret("口令")
		if err != nil {
			return err
		}
		n.Proxy["password"] = pw
	}
	if err := eng.AddNode(ctx, n); err != nil {
		return err
	}
	return a.reportNode(st, n.ID)
}
