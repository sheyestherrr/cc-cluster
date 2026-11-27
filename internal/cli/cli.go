// Package cli 实现 cc-cluster 的命令行。
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
	"github.com/sheyestherrr/cc-cluster/internal/update"
)

// EnvDataDir 是指定数据目录的环境变量。
const EnvDataDir = "CC_CLUSTER_DATA"

// Version 由构建时注入。
var Version = "dev"

const usage = `cc-cluster：为 Claude Code 管理相互独立的工作槽位。
每个槽位的 worker 容器默认没有网络，全部流量由网关容器透明转发到绑定的落地。

平时只需要两个：
  cc-cluster                在项目目录里运行：选一个槽位，进去启动 Claude Code 或打开终端
  cc-cluster web            打开网页界面（在后台运行）：增删槽位、节点、订阅都在这里；web stop 停止

下面是完整的命令，给脚本、排查和没有浏览器的机器用：
  cc-cluster [--data-dir 目录] <命令> [参数]

数据目录默认是 ~/.cc-cluster；用 --data-dir 或环境变量 CC_CLUSTER_DATA 可以换成别的。

日常使用：
  enter  [槽位] [目录]      进入槽位的 shell。目录默认是当前目录，不在挂载范围内时自动挂载
  claude [槽位] [目录]      同上，但直接启动 Claude Code（首次会提示在槽内安装）
  tmux   <槽位>             接入该槽的宿主 tmux；--on / --off 开关宿主桥接，--kill 结束它
  status                    显示网关与槽位的现状
  traffic [槽位]            各槽位的流量；给了槽位时列出它当前的连接和连过的目标
  web [stop]                网页界面，默认放在后台；运行期间做持续检测，并记录流量与连接的历史（--foreground 在前台运行）
  watch                     持续检测各槽的连通性与出口，出口变了就断网保持（--once 只测一遍）；同样记录历史
  panel                     文字菜单的交互面板

配置：
  init                      初始化数据目录
  doctor                    检查这台机器能否运行（不访问外部地址）
  sub add|list|nodes|note|set|test|refresh|rm
                            订阅：远程的订阅地址，或贴进来的订阅内容与分享链接
  node add|list|note|resolve|rm
                            代理节点（中转 / 落地），可取自订阅
  gateway add|list|note|set-transit|up|down|rm
                            网关容器
  slot add|list|note|up|stop|hold|release|rm|exec|enter|mounts|unmount
                            工作槽位
  render <网关>             预览网关的核心配置（默认隐藏凭据）
  exit-report [on|off]      启动槽位时是否顺带查询第三方对出口 IP 的评估
  images                    查看或更换网关镜像与默认的 worker 镜像
  update                    把 cc-cluster 换成最新发布的版本（--check 只查看；--images 顺带换镜像）；不改动数据目录里的槽位与设置
  version                   显示版本

每个命令和子命令都可以加 -h 查看参数，例如 cc-cluster slot add -h。
`

type app struct {
	dataDir string
	out     io.Writer
	errw    io.Writer
	// in 是交互输入；interactive 表示标准输入和标准输出都接在终端上，可以提问。
	in          *bufio.Reader
	interactive bool
}

// Run 执行命令行并返回进程退出码。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{out: stdout, errw: stderr, in: newReader(stdin)}
	if fi, ok := stdin.(*os.File); ok {
		if fo, ok := stdout.(*os.File); ok {
			a.interactive = isTerminal(fi) && isTerminal(fo)
		}
	}
	fs := flag.NewFlagSet("cc-cluster", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	fs.StringVar(&a.dataDir, "data-dir", os.Getenv(EnvDataDir), "数据目录")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if a.dataDir == "" {
		a.dataDir = store.DefaultDir()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch {
	case len(rest) == 0 && a.interactive:
		err = a.home(ctx)
	case len(rest) == 0:
		fmt.Fprint(stderr, usage)
		return 2
	default:
		err = a.dispatch(ctx, rest[0], rest[1:])
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	case errors.Is(err, errCancelled):
		fmt.Fprintln(stderr, "已取消")
		return 1
	}
	fmt.Fprintf(stderr, "错误：%v\n", err)
	return 1
}

var errUsage = errors.New("用法错误")

// opTimeout 是一条普通命令的时限；常驻的命令（面板、网页）不受它限制。
const opTimeout = 10 * time.Minute

func (a *app) dispatch(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "panel", "menu":
		return a.panel(ctx)
	case "web":
		return a.cmdWeb(ctx, args)
	case "watch":
		return a.cmdWatch(ctx, args)
	case "exit-report":
		return a.cmdExitReport(ctx, args)
	case "images":
		return a.cmdImages(ctx, args)
	case "update", "upgrade":
		return a.cmdUpdate(ctx, args)
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	switch cmd {
	case "init":
		return a.cmdInit(args)
	case "doctor":
		return a.cmdDoctor(ctx, args)
	case "status":
		return a.cmdStatus(ctx, args)
	case "traffic":
		return a.cmdTraffic(ctx, args)
	case "enter", "claude":
		return a.cmdEnter(ctx, cmd, args)
	case "tmux":
		return a.cmdTmux(ctx, args)
	case "node":
		return a.cmdNode(args)
	case "sub":
		return a.cmdSub(ctx, args)
	case "gateway":
		return a.cmdGateway(ctx, args)
	case "slot":
		return a.cmdSlot(ctx, args)
	case "render":
		return a.cmdRender(args)
	case "version":
		fmt.Fprintln(a.out, Version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(a.out, usage)
		return nil
	}
	fmt.Fprintf(a.errw, "未知命令 %q\n\n%s", cmd, usage)
	return errUsage
}

// newFlags 创建子命令的参数集；synopsis 是用法行。
func (a *app) newFlags(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.errw)
	fs.Usage = func() {
		fmt.Fprintf(a.errw, "用法：cc-cluster %s\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseWithID 解析形如 "<ID> [参数]" 的命令行：ID 必须在最前面，其后才是参数。
func parseWithID(fs *flag.FlagSet, args []string) (string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
			fs.Usage()
			return "", flag.ErrHelp
		}
		fs.Usage()
		return "", errUsage
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", err
		}
		return "", errUsage
	}
	return args[0], nil
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	return nil
}

// multi 是可以重复出现的字符串参数。
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func (a *app) requireDataDir() error {
	if a.dataDir == "" {
		return fmt.Errorf("找不到用户的 HOME，没法用默认的数据目录：用 --data-dir，或设置环境变量 %s", EnvDataDir)
	}
	return nil
}

// open 打开数据目录；withDocker 为 true 时同时连接 Docker。
func (a *app) open(ctx context.Context, withDocker bool) (*engine.Engine, *store.Store, error) {
	if err := a.requireDataDir(); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(a.dataDir)
	if err != nil {
		return nil, nil, err
	}
	var dk *dockerapi.Client
	if withDocker {
		if dk, err = dockerapi.New(ctx, ""); err != nil {
			return nil, nil, err
		}
	}
	eng, err := engine.New(st, dk, a.errw)
	return eng, st, err
}

func (a *app) table() *tabwriter.Writer {
	return tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
}

// defaultClusterName 返回默认的容器名前缀：cc-<宿主用户名>，这样同一台机器上不同用户的容器不会重名。
// 用户名取不到或整理后为空时退回 cc。
func defaultClusterName() string {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 20 {
		s = strings.TrimRight(s[:20], "-")
	}
	if s == "" {
		return model.DefaultClusterName
	}
	return model.DefaultClusterName + "-" + s
}

// defaultImages 返回 init 默认使用的两个镜像：发布版用与自己同版本的发布镜像，
// 从源码构建的用本机 make images 得到的那两个。
func defaultImages() (gateway, worker string) {
	if update.IsRelease(Version) {
		return update.Images(Version)
	}
	return model.DefaultGatewayImage, model.DefaultWorkerImage
}

// cmdImages 查看或更换网关镜像与默认的 worker 镜像。
func (a *app) cmdImages(ctx context.Context, args []string) error {
	fs := a.newFlags("images", "images [--gateway-image 镜像] [--worker-image 镜像]    不带参数显示现状")
	gateway := fs.String("gateway-image", "", "换成这个网关镜像")
	worker := fs.String("worker-image", "", "换成这个默认的 worker 镜像")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	eng, _, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	before := eng.Cluster()
	if *gateway != "" || *worker != "" {
		if err := eng.SetImages(*gateway, *worker); err != nil {
			return err
		}
	}
	c := eng.Cluster()
	fmt.Fprintf(a.out, "网关镜像：%s\nworker 镜像：%s\n", c.GatewayImage, c.WorkerImage)
	if c != before {
		fmt.Fprintln(a.out, "已经在运行的容器仍在用原来的镜像：网关停止后再启动、槽位重建 worker 之后换成新的（网页里各自的页面上有这两个操作）。")
	}
	return nil
}

func (a *app) cmdInit(args []string) error {
	fs := a.newFlags("init", "init [参数]")
	var c model.Cluster
	gwImage, wImage := defaultImages()
	fs.StringVar(&c.Name, "name", defaultClusterName(), "容器名前缀，用来区分同一台机器上的多个用户和数据目录")
	fs.StringVar(&c.GatewayImage, "gateway-image", gwImage, "网关镜像")
	fs.StringVar(&c.WorkerImage, "worker-image", wImage, "默认的 worker 镜像")
	fs.BoolVar(&c.NoExitReport, "no-exit-report", false, "启动槽位时不顺带查询第三方对出口 IP 的评估（之后可用 exit-report on|off 更改）")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := a.requireDataDir(); err != nil {
		return err
	}
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	st, err := store.Init(a.dataDir, c)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已初始化数据目录 %s（容器名前缀 %s）\n", st.Dir(), c.Name)
	return nil
}

// cmdExitReport 查看或更改“启动槽位时顺带查询出口评估”这个设置。
func (a *app) cmdExitReport(ctx context.Context, args []string) error {
	fs := a.newFlags("exit-report", "exit-report [on|off]    启动槽位时是否顺带查询第三方对出口 IP 的评估；不带参数显示现状")
	if err := parse(fs, args); err != nil {
		return err
	}
	eng, _, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	switch fs.Arg(0) {
	case "":
	case "on", "off":
		if err := eng.SetExitReportAuto(fs.Arg(0) == "on"); err != nil {
			return err
		}
	default:
		fs.Usage()
		return errUsage
	}
	if eng.ExitReportAuto() {
		fmt.Fprintln(a.out, "出口评估：开。启动槽位时经该槽的链路查询一次，只用于展示，取不到不影响使用")
	} else {
		fmt.Fprintln(a.out, "出口评估：关。启动槽位时不查询；node report <落地> 和网页里的“重新查询”仍可手动查")
	}
	return nil
}

func (a *app) cmdDoctor(ctx context.Context, args []string) error {
	if err := parse(a.newFlags("doctor", "doctor"), args); err != nil {
		return err
	}
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	failed := 0
	for _, c := range eng.Doctor(ctx) {
		mark := "通过"
		if !c.OK {
			mark = "失败"
			failed++
		}
		fmt.Fprintf(a.out, "[%s] %s：%s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		return fmt.Errorf("%d 项检查未通过", failed)
	}
	return nil
}

func (a *app) cmdStatus(ctx context.Context, args []string) error {
	if err := parse(a.newFlags("status", "status"), args); err != nil {
		return err
	}
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	if err := a.printGateways(ctx, eng); err != nil {
		return err
	}
	fmt.Fprintln(a.out)
	return a.printSlots(ctx, eng)
}

func (a *app) cmdRender(args []string) error {
	fs := a.newFlags("render", "render <网关> [参数]")
	show := fs.Bool("show-secrets", false, "原样输出，包括节点凭据")
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	eng, _, err := a.open(context.Background(), false)
	if err != nil {
		return err
	}
	b, err := eng.Render(id, !*show)
	if err != nil {
		return err
	}
	_, err = a.out.Write(b)
	return err
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
