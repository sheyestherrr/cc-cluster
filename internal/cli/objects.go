package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

func (a *app) subcommand(group, list string, args []string) (string, []string, error) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(a.errw, "用法：cc-cluster %s <%s> [参数]\n", group, list)
		if len(args) == 0 {
			return "", nil, errUsage
		}
		return "", nil, flag.ErrHelp
	}
	return args[0], args[1:], nil
}

// noteHelp 是各个 add 命令里 --note 的说明。
const noteHelp = "备注：用于显示的名称或说明，可使用中文、下划线与空格（ID 用于容器名和文件名，字符受限）"

// setNote 实现各对象的 note 子命令：<对象> note <ID> [备注…]；不给备注就清掉。
func (a *app) setNote(kind, group, what string, args []string) error {
	fs := a.newFlags(group+" note", group+" note <"+what+"> [备注]    设置备注（可使用中文、下划线与空格，仅用于显示）；省略备注则清除")
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	note := strings.TrimSpace(strings.Join(fs.Args(), " "))
	eng, _, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	if err := eng.SetNote(kind, id, note); err != nil {
		return err
	}
	if note == "" {
		fmt.Fprintf(a.out, "已清除%s %s 的备注\n", what, id)
	} else {
		fmt.Fprintf(a.out, "%s %s 的备注：%s\n", what, id, note)
	}
	return nil
}

// ---- node ----

func (a *app) cmdNode(args []string) error {
	sub, rest, err := a.subcommand("node", "add|list|note|report|resolve|rm", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.nodeAdd(rest)
	case "list", "ls":
		return a.nodeList(rest)
	case "note":
		return a.setNote(engine.KindNode, "node", "节点", rest)
	case "rm":
		fs := a.newFlags("node rm", "node rm <节点>")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(context.Background(), false)
		if err != nil {
			return err
		}
		return eng.RemoveNode(id)
	case "resolve":
		id, err := parseWithID(a.newFlags("node resolve", "node resolve <节点>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(context.Background(), false)
		if err != nil {
			return err
		}
		ip, err := eng.ResolveNode(context.Background(), id)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "节点 %s 的首跳地址：%s；使用该节点的网关将在下次 gateway up 或 slot up 时应用\n", id, ip)
		return nil
	case "report":
		id, err := parseWithID(a.newFlags("node report", "node report <落地>    重新查询出口 IP 的评估（经链路查询，使用该落地的网关须在运行）"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(context.Background(), true)
		if err != nil {
			return err
		}
		rep, err := eng.RefreshExitReport(context.Background(), id)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "落地 %s 的出口 %s：%s\n", id, rep.IP, engine.ExitReportSummary(*rep))
		return nil
	}
	fmt.Fprintf(a.errw, "未知的 node 子命令 %q\n", sub)
	return errUsage
}

func (a *app) nodeAdd(args []string) error {
	fs := a.newFlags("node add", "node add <节点> --role transit|landing (--sub 订阅 [--name 名称] | --file 片段.json | --type … --server … --port …)")
	n := model.Node{}
	fs.StringVar(&n.Role, "role", "", "角色：transit（中转）或 landing（落地）")
	file := fs.String("file", "", "Mihomo 原生 proxy 片段（JSON 对象）；- 表示从标准输入读取")
	typ := fs.String("type", "", "未使用 --file 时：代理类型，例如 socks5、http")
	server := fs.String("server", "", "未使用 --file 时：服务器地址")
	port := fs.Int("port", 0, "未使用 --file 时：端口")
	username := fs.String("username", "", "未使用 --file 时：用户名")
	passwordFile := fs.String("password-file", "", "未使用 --file 时：保存密码的文件（密码不通过命令行参数传递，以免出现在进程列表和 shell 历史中）")
	fs.StringVar(&n.ConnectIPv4, "connect-ipv4", "", "该节点用作首跳时网关直连的 IPv4 地址；server 为域名且未指定时，在本机解析并保存")
	fs.StringVar(&n.ExpectExitIP, "expect-exit-ip", "", "落地的预期出口 IPv4；指定后必须与实测出口一致")
	fs.StringVar(&n.TZ, "tz", "", "落地所在地区的时区，例如 Asia/Tokyo；绑定该落地的槽位默认沿用")
	fs.StringVar(&n.Lang, "lang", "", "落地所在地区的语言，例如 en_US.UTF-8；绑定该落地的槽位默认沿用")
	sub := fs.String("sub", "", "从该订阅中选取节点（须先用 sub add 添加订阅）")
	subName := fs.String("name", "", "与 --sub 配合：订阅中的节点名称；未指定时先测试延迟，中转默认选用延迟最低的节点，落地则列出节点供选择")
	forLanding := fs.String("landing", "", "与 --sub 配合选择中转：经该落地测试整条链路，仅从可连通该落地的节点中选择")
	fs.StringVar(&n.Note, "note", "", noteHelp)
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	n.ID, n.Note = id, strings.TrimSpace(n.Note)

	ctx := context.Background()
	if *sub != "" {
		if *file != "" || *typ != "" || *server != "" || *port != 0 {
			return errors.New("--sub、--file、--type/--server/--port 三种方式只能使用一种")
		}
		eng, st, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		name := *subName
		if *forLanding != "" && (name != "" || n.Role != model.RoleTransit) {
			return errors.New("--landing 仅用于从订阅中选择中转（--role transit 且未指定 --name）")
		}
		if name == "" {
			if name, err = a.pickSubNode(ctx, *sub, n.Role, *forLanding); err != nil {
				return err
			}
		}
		if err := eng.NodeFromSub(ctx, n, *sub, name); err != nil {
			return err
		}
		return a.reportNode(st, n.ID)
	}

	switch {
	case *file != "" && (*typ != "" || *server != "" || *port != 0):
		return errors.New("--file 与 --type/--server/--port 不能同时使用")
	case *file != "":
		var r io.Reader = os.Stdin
		if *file != "-" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		if err := json.NewDecoder(r).Decode(&n.Proxy); err != nil {
			return fmt.Errorf("片段不是有效的 JSON 对象：%w", err)
		}
	default:
		n.Proxy = map[string]any{"type": *typ, "server": *server, "port": *port}
		if *username != "" {
			n.Proxy["username"] = *username
		}
		if *passwordFile != "" {
			b, err := os.ReadFile(*passwordFile)
			if err != nil {
				return err
			}
			n.Proxy["password"] = strings.TrimRight(string(b), "\r\n")
		}
	}
	// name 与 udp 由管理器决定；从现成配置里粘贴来的片段常带着它们，直接去掉。
	delete(n.Proxy, "name")
	delete(n.Proxy, "udp")

	eng, st, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	if err := eng.AddNode(ctx, n); err != nil {
		return err
	}
	return a.reportNode(st, n.ID)
}

// reportNode 打印刚登记的节点；首跳地址是自动解析出来的也一并说明。
func (a *app) reportNode(st *store.Store, id string) error {
	n, err := st.Node(id)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已添加节点 %s（%s，%s:%d）\n", n.ID, n.Role, n.Server(), n.Port())
	if n.ConnectAuto {
		fmt.Fprintf(a.out, "服务器地址为域名，已在本机解析为 %s 并保存；地址变更后请执行 node resolve %s\n", n.ConnectIPv4, n.ID)
	}
	return nil
}

// pickSubNode 从订阅里定下一个节点，返回它的名称。先测一遍延迟：
// 中转默认用延迟最低的那个（终端里可以改选，非交互时直接采用）；落地决定出口，必须由人来选。
// landing 不为空时按"经节点再到这个落地"的整条链路测。
func (a *app) pickSubNode(ctx context.Context, subID, role, landing string) (string, error) {
	_, st, err := a.open(ctx, false)
	if err != nil {
		return "", err
	}
	sub, err := st.Sub(subID)
	if err != nil {
		return "", err
	}
	auto := role == model.RoleTransit
	if !a.interactive && !auto {
		return "", fmt.Errorf("请用 --name 指定订阅中的节点名称；可用 sub nodes %s 查看", subID)
	}
	delays, terr := a.testSub(ctx, subID, engine.DelayOptions{Landing: landing})
	if terr != nil {
		if !a.interactive {
			return "", fmt.Errorf("延迟测试失败，无法自动选择：%w；可用 --name 直接指定节点", terr)
		}
		fmt.Fprintf(a.errw, "延迟测试失败（%v），按已有记录列出\n", terr)
		delays = engine.SubDelays(sub)
	}
	what := "延迟"
	if landing != "" {
		what = "经落地 " + landing + " 的链路延迟"
	}
	best, hasBest := engine.Fastest(delays)
	if !a.interactive {
		if !hasBest {
			return "", fmt.Errorf("订阅 %s 中没有可用的节点（按%s测试）；请用 sub refresh %s 刷新后重试，或用 --name 直接指定", subID, what, subID)
		}
		fmt.Fprintf(a.out, "按%s选用订阅 %s 的节点 %s（%s）\n", what, subID, best.Name, best.DelayText())
		return best.Name, nil
	}
	items, def := make([]string, len(delays)), -1
	for i, d := range delays {
		items[i] = fmt.Sprintf("%-8s %s  (%s, %s)", d.DelayText(), d.Name, d.Type, d.Server)
		if auto && hasBest && def < 0 && d.Name == best.Name {
			def = i
		}
	}
	title := fmt.Sprintf("订阅 %s 的节点（按%s排序）：", subID, what)
	if def >= 0 {
		title = fmt.Sprintf("订阅 %s 的节点（按%s排序，直接回车选用延迟最低的节点）：", subID, what)
	}
	i, err := a.choose(title, items, def)
	if err != nil {
		return "", err
	}
	return delays[i].Name, nil
}

// testSub 测一遍订阅里各节点的延迟。
func (a *app) testSub(ctx context.Context, subID string, opt engine.DelayOptions) ([]engine.NodeDelay, error) {
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return nil, err
	}
	return eng.SubTest(ctx, subID, opt)
}

func (a *app) nodeList(args []string) error {
	if err := parse(a.newFlags("node list", "node list"), args); err != nil {
		return err
	}
	_, st, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	nodes, err := st.Nodes()
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "节点\t角色\t类型\t服务器\t首跳地址\t预期出口\t时区\t来源\t备注")
	for _, n := range nodes {
		typ, _ := n.Proxy["type"].(string)
		src := "-"
		if n.Source != nil {
			src = n.Source.Sub + " / " + n.Source.Name
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s:%d\t%s\t%s\t%s\t%s\t%s\n", n.ID, n.Role, typ, n.Server(), n.Port(),
			orDash(n.ConnectIPv4), orDash(n.ExpectExitIP), orDash(n.TZ), src, orDash(n.Note))
	}
	return w.Flush()
}

// ---- gateway ----

func (a *app) cmdGateway(ctx context.Context, args []string) error {
	sub, rest, err := a.subcommand("gateway", "add|list|note|set-transit|up|down|rm", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.gatewayAdd(rest)
	case "note":
		return a.setNote(engine.KindGateway, "gateway", "网关", rest)
	case "set-transit":
		fs := a.newFlags("gateway set-transit", "gateway set-transit <网关> <主用中转> [备用中转…]")
		var topt engine.TransitOptions
		fs.BoolVar(&topt.SkipChainCheck, "skip-check", false, "跳过中转到各槽位落地的连通性验证（默认验证，任一无法连通则不更换）")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		if fs.NArg() == 0 {
			fs.Usage()
			return errUsage
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		res, err := eng.SetGatewayTransits(ctx, id, fs.Args(), topt)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "网关 %s 的中转已更换为 %s", id, fs.Arg(0))
		if fs.NArg() > 1 {
			fmt.Fprintf(a.out, "（备用：%s；主用不可用时自动切换，出口不变）", strings.Join(fs.Args()[1:], "、"))
		}
		switch {
		case !res.Reloaded:
			fmt.Fprintln(a.out, "；网关未运行，下次启动时生效")
		case len(res.Failed) > 0:
			fmt.Fprintf(a.out, "；已生效，但 %d 个槽位更换后未通过出口核对（见上方提醒），请排查后重新执行 slot up\n", len(res.Failed))
		default:
			fmt.Fprintf(a.out, "；已生效，已重新核对 %d 个槽位的出口\n", len(res.Rechecked))
		}
		return nil
	case "list", "ls":
		if err := parse(a.newFlags("gateway list", "gateway list"), rest); err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		return a.printGateways(ctx, eng)
	case "up":
		id, err := parseWithID(a.newFlags("gateway up", "gateway up <网关>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		if err := eng.GatewayUp(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "网关 %s 已就绪\n", id)
		return nil
	case "down":
		fs := a.newFlags("gateway down", "gateway down <网关> [--force]")
		force := fs.Bool("force", false, "即使仍有运行中的槽位也停止（这些槽位将断网）")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		return eng.GatewayDown(ctx, id, *force)
	case "rm":
		id, err := parseWithID(a.newFlags("gateway rm", "gateway rm <网关>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		return eng.RemoveGateway(ctx, id)
	}
	fmt.Fprintf(a.errw, "未知的 gateway 子命令 %q\n", sub)
	return errUsage
}

func (a *app) gatewayAdd(args []string) error {
	fs := a.newFlags("gateway add", "gateway add <网关> [--transit 节点] [参数]")
	g := model.Gateway{}
	var dns, echo, connect, backups multi
	fs.Var(&backups, "backup", "备用中转，可重复指定，按顺序使用：主用不可用时核心自动切换，恢复后切回；出口由落地决定，不随之改变")
	fs.Var(&connect, "connect-url", "连通性检查使用的地址，可重复指定（默认为 Google 与 Cloudflare 的 204 地址）；出口探测前用于分段检查链路")
	fs.StringVar(&g.Transit, "transit", "", "中转节点；未指定时不经中转，各槽位直连自己的落地（或直出节点）")
	fs.StringVar(&g.EgressNetwork, "egress-network", "", "出口所在的 Docker 网络（默认 <容器名前缀>-egress）；不存在时创建为普通 bridge 网络")
	fs.StringVar(&g.Pool, "pool", model.DefaultPool, "为各槽位分配 /30 网段的私有地址池")
	fs.Var(&dns, "dns", "核心使用的 DNS 服务器，可重复；主机部分必须是 IP（默认 "+model.DefaultDNS+"）")
	fs.Var(&echo, "echo-url", "出口 IP 探测地址，可重复（默认使用内置的几个公共地址）")
	fs.StringVar(&g.Image, "image", "", "网关镜像；默认使用集群的镜像设置")
	fs.StringVar(&g.LogLevel, "log-level", model.DefaultLogLevel, "核心日志级别：silent|error|warning|info|debug")
	fs.StringVar(&g.Note, "note", "", noteHelp)
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	g.ID, g.DNS, g.EchoURLs, g.ConnectURLs, g.Backups = id, dns, echo, connect, backups
	g.Note = strings.TrimSpace(g.Note)
	eng, _, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	if err := eng.AddGateway(g); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已创建网关 %s\n", g.ID)
	return nil
}

func (a *app) printGateways(ctx context.Context, eng *engine.Engine) error {
	sts, err := eng.GatewayStatuses(ctx)
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "网关\t中转\t出口网络\t容器\t核心就绪\t配置最新\t槽位\t备注")
	for _, s := range sts {
		transit := orDash(s.Gateway.Transit)
		if n := len(s.Gateway.Backups); n > 0 {
			transit += fmt.Sprintf(" +%d 备用", n)
			if s.ActiveTransit != "" && s.ActiveTransit != s.Gateway.Transit {
				transit += "（当前 " + s.ActiveTransit + "）"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Gateway.ID, transit, s.Gateway.EgressNetwork,
			s.Container, yesNo(s.CoreReady), yesNo(s.ConfigCurrent), orDash(strings.Join(s.Slots, ",")), orDash(s.Gateway.Note))
	}
	return w.Flush()
}

// ---- slot ----

func (a *app) cmdSlot(ctx context.Context, args []string) error {
	sub, rest, err := a.subcommand("slot", "add|list|note|up|stop|hold|release|rm|exec|enter", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.slotAdd(rest)
	case "note":
		return a.setNote(engine.KindSlot, "slot", "槽位", rest)
	case "list", "ls", "status":
		if err := parse(a.newFlags("slot list", "slot list"), rest); err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		return a.printSlots(ctx, eng)
	case "up":
		fs := a.newFlags("slot up", "slot up <槽位> [--recreate]")
		recreate := fs.Bool("recreate", false, "worker 容器与当前配置不一致时删除并重建（容器内的进程将结束）")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		res, err := eng.SlotUp(ctx, id, engine.UpOptions{Recreate: *recreate})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "槽位 %s 已就绪：出口 %s（已在 worker 侧复测）\n", id, res.ExitSummary())
		return nil
	case "stop":
		id, err := parseWithID(a.newFlags("slot stop", "slot stop <槽位>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		return eng.SlotStop(ctx, id)
	case "hold":
		fs := a.newFlags("slot hold", "slot hold <槽位> [--reason 原因]")
		reason := fs.String("reason", "", "断网原因，记入状态")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		if err := eng.SlotHold(ctx, id, *reason); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "槽位 %s 已断网；恢复时请先执行 slot release，再执行 slot up\n", id)
		return nil
	case "release":
		id, err := parseWithID(a.newFlags("slot release", "slot release <槽位>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		if err := eng.SlotRelease(id); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已解除槽位 %s 的手动断网；请执行 slot up %s 重新接线\n", id, id)
		return nil
	case "rm":
		fs := a.newFlags("slot rm", "slot rm <槽位> [--purge]")
		purge := fs.Bool("purge", false, "同时删除该槽位的 HOME 与身份（不可恢复）")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		return eng.SlotRemove(ctx, id, *purge)
	case "exec":
		return a.slotExec(ctx, rest)
	case "enter":
		return a.cmdEnter(ctx, "enter", rest)
	case "mounts":
		return a.slotMounts(rest)
	case "unmount":
		return a.slotUnmount(rest)
	}
	fmt.Fprintf(a.errw, "未知的 slot 子命令 %q\n", sub)
	return errUsage
}

// slotMounts 列出槽位的挂载。
func (a *app) slotMounts(args []string) error {
	id, err := parseWithID(a.newFlags("slot mounts", "slot mounts <槽位>"), args)
	if err != nil {
		return err
	}
	_, st, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	sl, err := st.Slot(id)
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "宿主目录\tworker 内路径\t只读")
	for _, m := range sl.Mounts {
		fmt.Fprintf(w, "%s\t%s\t%s\n", m.Source, m.Target, yesNo(m.ReadOnly))
	}
	return w.Flush()
}

// slotUnmount 从槽位的挂载里去掉一项。
func (a *app) slotUnmount(args []string) error {
	fs := a.newFlags("slot unmount", "slot unmount <槽位> <worker 内路径>")
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}
	eng, _, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	if err := eng.SlotRemoveMount(id, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已移除槽位 %s 的挂载 %s；运行中的 worker 需执行 slot up %s --recreate 后生效\n", id, fs.Arg(0), id)
	return nil
}

func (a *app) slotAdd(args []string) error {
	fs := a.newFlags("slot add", "slot add <槽位> (--gateway 网关 --landing 节点 | --direct 订阅:节点名) [参数]")
	s := model.Slot{UID: os.Getuid(), GID: os.Getgid()}
	var mounts, envs multi
	direct := fs.String("direct", "", "直出：不使用落地，直接以订阅中的该节点作为出口（格式为 订阅:节点名）。出口地址为多人共用；节点固定，不自动更换")
	fs.StringVar(&s.Gateway, "gateway", "", "所属网关")
	fs.StringVar(&s.Landing, "landing", "", "绑定的落地节点")
	fs.StringVar(&s.Image, "image", "", "worker 镜像；默认使用集群的镜像设置")
	fs.StringVar(&s.Shell, "shell", model.DefaultShell, "worker 内的登录 shell")
	fs.Var(&mounts, "mount", "挂载，格式为 宿主目录[:容器路径[:ro]]，可重复指定；仅指定宿主目录时 worker 内使用相同路径，相对路径按当前目录解析")
	fs.Var(&envs, "env", "额外的环境变量，格式为 名=值，可重复指定；不允许设置代理变量")
	fs.StringVar(&s.TZ, "tz", "", "时区，例如 Asia/Shanghai；未指定时沿用落地的设置")
	fs.StringVar(&s.Lang, "lang", "", "LANG；未指定时沿用落地的设置，均未设置时为 "+model.DefaultLang)
	memory := fs.String("memory", "", "内存上限，例如 8g")
	fs.Float64Var(&s.CPUs, "cpus", 0, "CPU 上限（核数）")
	fs.Int64Var(&s.PidsLimit, "pids", model.DefaultPidsLimit, "进程数上限")
	var opt engine.SlotOptions
	fs.StringVar(&opt.Hostname, "hostname", "", "hostname；未指定时按“用户名-机型”生成并保存")
	fs.StringVar(&opt.User, "user", "", "worker 内的用户名；默认为 user")
	fs.BoolVar(&s.FakeDMI, "fake-dmi", false, "以该槽位身份中的机型信息覆盖 DMI（默认将 DMI 屏蔽为空）")
	fs.BoolVar(&s.FakeProc, "fake-proc", false, "使 /proc/cpuinfo 与 /proc/meminfo 按 --cpus、--memory 的限额显示（静态内容，不随用量变化）；指定 --cpus 时同时将容器绑定到前 N 个核")
	fs.BoolVar(&s.TmuxBridge, "tmux-bridge", false, "开启宿主 tmux 桥接：槽内的 Claude 可经 tmux-mcp 操作宿主上该槽位专用的 tmux（宿主上的命令不受该槽位的网络限制）")
	fs.StringVar(&s.TmuxTools, "tmux-tools", "", "桥接开放的工具范围，格式为 deny:项,项 或 allow:项,项；默认为 "+model.DefaultTmuxTools)
	fs.StringVar(&s.Note, "note", "", noteHelp)
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	s.ID, s.Note = id, strings.TrimSpace(s.Note)
	if *direct != "" && (s.Gateway != "" || s.Landing != "") {
		return errors.New("--direct 不能与 --gateway、--landing 同时使用：直出的网关与出口节点由管理器创建")
	}
	if *direct == "" && (s.Gateway == "" || s.Landing == "") {
		fs.Usage()
		return errUsage
	}
	if s.MemoryBytes, err = model.ParseBytes(*memory); err != nil {
		return err
	}
	for _, m := range mounts {
		parts := strings.Split(m, ":")
		if len(parts) > 3 || (len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw") {
			return fmt.Errorf("挂载 %q 的格式应为 宿主目录[:容器路径[:ro]]", m)
		}
		src, err := filepath.Abs(parts[0])
		if err != nil {
			return err
		}
		// 只给宿主目录时，worker 内使用相同的路径。
		target := src
		if len(parts) >= 2 {
			target = parts[1]
		}
		s.Mounts = append(s.Mounts, model.Mount{Source: src, Target: target, ReadOnly: len(parts) == 3 && parts[2] == "ro"})
	}
	for _, kv := range envs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("环境变量 %q 的格式应为 名=值", kv)
		}
		if s.Env == nil {
			s.Env = map[string]string{}
		}
		s.Env[k] = v
	}

	eng, _, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if *direct != "" {
		subID, name, ok := strings.Cut(*direct, ":")
		if !ok || subID == "" || name == "" {
			return errors.New("--direct 的格式应为 订阅:节点名；节点名可用 sub test <订阅> 查看（按延迟排序）")
		}
		if s.Gateway, s.Landing, err = eng.PrepareDirect(ctx, s.ID, subID, name); err != nil {
			return err
		}
	}
	out, warnings, err := eng.AddSlot(ctx, s, opt)
	if err != nil {
		if *direct != "" {
			_ = eng.RemoveNode(s.Landing)
		}
		return err
	}
	if *direct != "" {
		warnings = append(warnings, engine.DirectNotice)
	}
	for _, w := range warnings {
		fmt.Fprintf(a.errw, "提醒：%s\n", w)
	}
	fmt.Fprintf(a.out, "已创建槽位 %s（网关 %s，落地 %s，网段 %s）；请执行 slot up %s 启动\n", out.ID, out.Gateway, out.Landing, out.Subnet, out.ID)
	if out.TmuxBridge {
		fmt.Fprintln(a.errw, bridgeNotice)
	}
	return nil
}

// bridgeNotice 在开启桥接时给出：这是一条信任边界，要让用户每次都看到。
const bridgeNotice = "提醒：已开启宿主 tmux 桥接。槽内经其执行的命令在宿主上运行，使用当前用户的权限与宿主网络，不受该槽位的网络限制，手动断网（hold）对其无效。"

func (a *app) printSlots(ctx context.Context, eng *engine.Engine) error {
	sts, err := eng.SlotStatuses(ctx)
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "槽位\t状态\t网关\t落地\t已验证出口\t出口归属\t验证时间\thostname\t备注")
	for _, s := range sts {
		at := "-"
		if !s.AppliedAt.IsZero() {
			at = s.AppliedAt.Local().Format("2006-01-02 15:04:05")
		}
		// 备注列先放用户写的备注，再放状态说明（断网原因、宿主桥接）。
		var notes []string
		for _, v := range []string{s.Slot.Note, s.HoldReason} {
			if v != "" {
				notes = append(notes, v)
			}
		}
		if s.Slot.TmuxBridge {
			b := "宿主桥接"
			if !s.TmuxAlive {
				b += "（tmux 未运行）"
			}
			notes = append(notes, b)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Slot.ID, s.State, s.Slot.Gateway, s.Slot.Landing,
			orDash(s.ExitIP), orDash(s.ExitWhere), at, s.Identity.Hostname, orDash(strings.Join(notes, "；")))
	}
	return w.Flush()
}

func (a *app) slotExec(ctx context.Context, args []string) error {
	fs := a.newFlags("slot exec", "slot exec <槽位> -- 命令 [参数…]")
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		fs.Usage()
		return errUsage
	}
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	if err := eng.RequireReady(ctx, id); err != nil {
		return err
	}
	res, err := eng.SlotExec(ctx, id, cmd)
	if err != nil {
		return err
	}
	fmt.Fprint(a.out, res.Stdout)
	fmt.Fprint(a.errw, res.Stderr)
	if res.ExitCode != 0 {
		return fmt.Errorf("命令退出码 %d", res.ExitCode)
	}
	return nil
}
