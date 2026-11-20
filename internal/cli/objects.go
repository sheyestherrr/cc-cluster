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

// ---- node ----

func (a *app) cmdNode(args []string) error {
	sub, rest, err := a.subcommand("node", "add|list|report|resolve|rm", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.nodeAdd(rest)
	case "list", "ls":
		return a.nodeList(rest)
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
		fmt.Fprintf(a.out, "节点 %s 的首跳地址：%s；用到它的网关在下次 gateway up / slot up 时应用\n", id, ip)
		return nil
	case "report":
		id, err := parseWithID(a.newFlags("node report", "node report <落地>    重新查询出口 IP 的评估（经链路查询，需要用到它的网关在运行）"), rest)
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
	file := fs.String("file", "", "Mihomo 原生 proxy 片段（JSON 对象）；- 表示从标准输入读")
	typ := fs.String("type", "", "不用 --file 时：代理类型，例如 socks5、http")
	server := fs.String("server", "", "不用 --file 时：服务器地址")
	port := fs.Int("port", 0, "不用 --file 时：端口")
	username := fs.String("username", "", "不用 --file 时：用户名")
	passwordFile := fs.String("password-file", "", "不用 --file 时：存放口令的文件（口令不从命令行传，避免留在进程列表和 shell 历史里）")
	fs.StringVar(&n.ConnectIPv4, "connect-ipv4", "", "该节点作首跳时网关直连的 IPv4 地址；server 是域名而不填时，在本机解析一次并记下")
	fs.StringVar(&n.ExpectExitIP, "expect-exit-ip", "", "落地的预期出口 IPv4；填了就必须与实测一致")
	fs.StringVar(&n.TZ, "tz", "", "落地所在地区的时区，例如 Asia/Tokyo；绑定它的槽位默认沿用")
	fs.StringVar(&n.Lang, "lang", "", "落地所在地区的语言，例如 en_US.UTF-8；绑定它的槽位默认沿用")
	sub := fs.String("sub", "", "从这个订阅里取节点（先用 sub add 登记订阅）")
	subName := fs.String("name", "", "配合 --sub：订阅里的节点名称；不填时先测延迟，中转默认用延迟最低的，落地列出来让你选")
	forLanding := fs.String("landing", "", "配合 --sub 选中转：接上这个落地节点按整条链路测速，只在能接通它的节点里挑")
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	n.ID = id

	ctx := context.Background()
	if *sub != "" {
		if *file != "" || *typ != "" || *server != "" || *port != 0 {
			return errors.New("--sub 与 --file、--type/--server/--port 只能选一种")
		}
		eng, st, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		name := *subName
		if *forLanding != "" && (name != "" || n.Role != model.RoleTransit) {
			return errors.New("--landing 只在从订阅里挑中转（--role transit、不带 --name）时使用")
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
		return errors.New("--file 与 --type/--server/--port 只能二选一")
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
			return fmt.Errorf("片段不是合法的 JSON 对象：%w", err)
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
	fmt.Fprintf(a.out, "已登记节点 %s（%s，%s:%d）\n", n.ID, n.Role, n.Server(), n.Port())
	if n.ConnectAuto {
		fmt.Fprintf(a.out, "服务器地址是域名，已在本机解析为 %s 并记下；地址变了以后执行 node resolve %s\n", n.ConnectIPv4, n.ID)
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
		return "", fmt.Errorf("需要用 --name 指定订阅里的节点名称；用 sub nodes %s 查看", subID)
	}
	delays, terr := a.testSub(ctx, subID, engine.DelayOptions{Landing: landing})
	if terr != nil {
		if !a.interactive {
			return "", fmt.Errorf("测延迟没有成功，无法自动挑选：%w；可以用 --name 直接指定节点", terr)
		}
		fmt.Fprintf(a.errw, "测延迟没有成功（%v），按已有的记录列出\n", terr)
		delays = engine.SubDelays(sub)
	}
	what := "延迟"
	if landing != "" {
		what = "接落地 " + landing + " 的整条链路"
	}
	best, hasBest := engine.Fastest(delays)
	if !a.interactive {
		if !hasBest {
			return "", fmt.Errorf("订阅 %s 里没有测通的节点（%s）；用 sub refresh %s 刷新后再试，或用 --name 直接指定", subID, what, subID)
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
		title = fmt.Sprintf("订阅 %s 的节点（按%s排序，直接回车用最快的）：", subID, what)
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
	fmt.Fprintln(w, "节点\t角色\t类型\t服务器\t首跳地址\t预期出口\t时区\t来源")
	for _, n := range nodes {
		typ, _ := n.Proxy["type"].(string)
		src := "-"
		if n.Source != nil {
			src = n.Source.Sub + " / " + n.Source.Name
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s:%d\t%s\t%s\t%s\t%s\n", n.ID, n.Role, typ, n.Server(), n.Port(),
			orDash(n.ConnectIPv4), orDash(n.ExpectExitIP), orDash(n.TZ), src)
	}
	return w.Flush()
}

// ---- gateway ----

func (a *app) cmdGateway(ctx context.Context, args []string) error {
	sub, rest, err := a.subcommand("gateway", "add|list|set-transit|up|down|rm", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.gatewayAdd(rest)
	case "set-transit":
		fs := a.newFlags("gateway set-transit", "gateway set-transit <网关> <主用中转> [候选中转…]")
		var topt engine.TransitOptions
		fs.BoolVar(&topt.SkipChainCheck, "skip-check", false, "不验证这些中转能否接通网关上各槽位的落地（默认会验证，接不通就不更换）")
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
		fmt.Fprintf(a.out, "网关 %s 的中转已改为 %s", id, fs.Arg(0))
		if fs.NArg() > 1 {
			fmt.Fprintf(a.out, "（候选：%s；主用的不通时自动改用，出口不变）", strings.Join(fs.Args()[1:], "、"))
		}
		switch {
		case !res.Reloaded:
			fmt.Fprintln(a.out, "；网关没有在运行，下次启动时生效")
		case len(res.Failed) > 0:
			fmt.Fprintf(a.out, "；已生效，但 %d 个槽位换完后没有通过核对（见上面的提醒），排查后重新 slot up\n", len(res.Failed))
		default:
			fmt.Fprintf(a.out, "；已生效，重新核对了 %d 个槽位的出口\n", len(res.Rechecked))
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
		force := fs.Bool("force", false, "即使它上面还有运行中的槽位也停止（这些槽会断网）")
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
	fs.Var(&backups, "backup", "候选中转，可重复、按顺序：主用的不通时核心自动改用，恢复后切回；出口由落地决定，不随之改变")
	fs.Var(&connect, "connect-url", "连通性检查访问的地址，可重复（默认是 Google 和 Cloudflare 的 204 地址）；出口探测之前先用它分段检查链路")
	fs.StringVar(&g.Transit, "transit", "", "中转节点；不填表示不经中转，各槽位直连自己的落地（或直出的节点）")
	fs.StringVar(&g.EgressNetwork, "egress-network", model.DefaultEgressNetwork, "出口所在的 Docker 网络；不存在时创建为普通 bridge 网络")
	fs.StringVar(&g.Pool, "pool", model.DefaultPool, "分给各槽 /30 的私网地址池")
	fs.Var(&dns, "dns", "核心使用的 DNS 服务器，可重复；主机部分必须是 IP（默认 "+model.DefaultDNS+"）")
	fs.Var(&echo, "echo-url", "出口 IP 探测地址，可重复（默认使用内置的几个公共地址）")
	fs.StringVar(&g.Image, "image", "", "网关镜像；不填用 init 时设定的")
	fs.StringVar(&g.LogLevel, "log-level", model.DefaultLogLevel, "核心日志级别：silent|error|warning|info|debug")
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	g.ID, g.DNS, g.EchoURLs, g.ConnectURLs, g.Backups = id, dns, echo, connect, backups
	eng, _, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	if err := eng.AddGateway(g); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已登记网关 %s\n", g.ID)
	return nil
}

func (a *app) printGateways(ctx context.Context, eng *engine.Engine) error {
	sts, err := eng.GatewayStatuses(ctx)
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "网关\t中转\t出口网络\t容器\t核心就绪\t配置最新\t槽位")
	for _, s := range sts {
		transit := orDash(s.Gateway.Transit)
		if n := len(s.Gateway.Backups); n > 0 {
			transit += fmt.Sprintf(" +%d 候选", n)
			if s.ActiveTransit != "" && s.ActiveTransit != s.Gateway.Transit {
				transit += "（在用 " + s.ActiveTransit + "）"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Gateway.ID, transit, s.Gateway.EgressNetwork,
			s.Container, yesNo(s.CoreReady), yesNo(s.ConfigCurrent), orDash(strings.Join(s.Slots, ",")))
	}
	return w.Flush()
}

// ---- slot ----

func (a *app) cmdSlot(ctx context.Context, args []string) error {
	sub, rest, err := a.subcommand("slot", "add|list|up|stop|hold|release|rm|exec|enter", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.slotAdd(rest)
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
		recreate := fs.Bool("recreate", false, "worker 容器与期望配置不一致时删掉重建（容器内的进程会结束）")
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
		fmt.Fprintf(a.out, "槽位 %s 已就绪：出口 %s（已从 worker 的位置复测）\n", id, res.ExitSummary())
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
		fmt.Fprintf(a.out, "槽位 %s 已断网；恢复前需要先 slot release 再 slot up\n", id)
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
		fmt.Fprintf(a.out, "已解除槽位 %s 的手动断网标记；执行 slot up %s 重新接线\n", id, id)
		return nil
	case "rm":
		fs := a.newFlags("slot rm", "slot rm <槽位> [--purge]")
		purge := fs.Bool("purge", false, "连同该槽的 HOME 与身份一起删除（不可恢复）")
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
	fmt.Fprintf(a.out, "已从槽位 %s 的挂载里去掉 %s；运行中的 worker 要 slot up %s --recreate 之后才生效\n", id, fs.Arg(0), id)
	return nil
}

func (a *app) slotAdd(args []string) error {
	fs := a.newFlags("slot add", "slot add <槽位> (--gateway 网关 --landing 节点 | --direct 订阅:节点名) [参数]")
	s := model.Slot{UID: os.Getuid(), GID: os.Getgid()}
	var mounts, envs multi
	direct := fs.String("direct", "", "直出：不用落地，直接用订阅里的这个节点当出口（形如 订阅:节点名）。出口是多人共用的地址；节点固定，不自动更换")
	fs.StringVar(&s.Gateway, "gateway", "", "所属网关")
	fs.StringVar(&s.Landing, "landing", "", "绑定的落地节点")
	fs.StringVar(&s.Image, "image", "", "worker 镜像；不填用 init 时设定的")
	fs.StringVar(&s.Shell, "shell", model.DefaultShell, "worker 内的登录 shell")
	fs.Var(&mounts, "mount", "挂载，形如 宿主目录[:容器路径[:ro]]，可重复；只给宿主目录时 worker 内用相同的路径，相对路径按当前目录解析")
	fs.Var(&envs, "env", "额外的环境变量 名=值，可重复；不允许代理变量")
	fs.StringVar(&s.TZ, "tz", "", "时区，例如 Asia/Shanghai；不填沿用落地的设置")
	fs.StringVar(&s.Lang, "lang", "", "LANG；不填沿用落地的设置，都没有则为 "+model.DefaultLang)
	memory := fs.String("memory", "", "内存上限，例如 8g")
	fs.Float64Var(&s.CPUs, "cpus", 0, "CPU 上限（核数）")
	fs.Int64Var(&s.PidsLimit, "pids", model.DefaultPidsLimit, "进程数上限")
	var opt engine.SlotOptions
	fs.StringVar(&opt.Hostname, "hostname", "", "指定 hostname；不填则按“用户名-机型”生成并持久化")
	fs.StringVar(&opt.User, "user", "", "指定 worker 内的用户名；不填则随机挑一个并持久化")
	fs.BoolVar(&s.FakeDMI, "fake-dmi", false, "用该槽身份里的机型信息覆盖 DMI（默认是把 DMI 遮成空的）")
	fs.BoolVar(&s.FakeProc, "fake-proc", false, "让 /proc/cpuinfo 与 /proc/meminfo 按 --cpus / --memory 的限额显示（静态内容，不随用量变化）；设了 --cpus 时还把容器绑到前 N 个核上")
	fs.BoolVar(&s.TmuxBridge, "tmux-bridge", false, "开启宿主 tmux 桥接：槽内的 Claude 可以经 tmux-mcp 操作宿主上该槽专用的 tmux（宿主命令不受该槽的网络约束）")
	fs.StringVar(&s.TmuxTools, "tmux-tools", "", "桥接开放的工具范围，deny:项,项 或 allow:项,项；不填为 "+model.DefaultTmuxTools)
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	s.ID = id
	if *direct != "" && (s.Gateway != "" || s.Landing != "") {
		return errors.New("--direct 不与 --gateway、--landing 同用：直出的网关和出口节点由管理器准备")
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
			return errors.New("--direct 的格式应为 订阅:节点名；节点名用 sub test <订阅> 查看（已按延迟排序）")
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
	fmt.Fprintf(a.out, "已登记槽位 %s（网关 %s，落地 %s，网段 %s）；执行 slot up %s 启动\n", out.ID, out.Gateway, out.Landing, out.Subnet, out.ID)
	if out.TmuxBridge {
		fmt.Fprintln(a.errw, bridgeNotice)
	}
	return nil
}

// bridgeNotice 在开启桥接时给出：这是一条信任边界，要让用户每次都看到。
const bridgeNotice = "提醒：已开启宿主 tmux 桥接。槽内经它执行的命令在宿主上运行，用的是你的权限和宿主网络，不受该槽的网络约束；断网（hold）也拦不住它。"

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
		note := s.HoldReason
		if s.Slot.TmuxBridge {
			b := "宿主桥接"
			if !s.TmuxAlive {
				b += "（tmux 未运行）"
			}
			if note != "" {
				note += "；"
			}
			note += b
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Slot.ID, s.State, s.Slot.Gateway, s.Slot.Landing,
			orDash(s.ExitIP), orDash(s.ExitWhere), at, s.Identity.Hostname, orDash(note))
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
