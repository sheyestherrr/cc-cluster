// Package cli 实现 cc-cluster 的命令行。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

// EnvDataDir 是指定数据目录的环境变量。
const EnvDataDir = "CC_CLUSTER_DATA"

// Version 由构建时注入。
var Version = "dev"

const usage = `cc-cluster：为 Claude Code 管理相互独立的工作槽位。
每个槽位的 worker 容器默认没有网络，全部流量由网关容器透明转发到绑定的落地。

用法：
  cc-cluster [--data-dir 目录] <命令> [参数]

数据目录必须显式给出：--data-dir，或环境变量 CC_CLUSTER_DATA。

命令：
  init                      初始化数据目录
  doctor                    检查这台机器能否运行（不访问外部地址）
  status                    显示网关与槽位的现状

  node add|list|rm          代理节点（中转 / 落地）
  gateway add|list|up|down|rm
                            网关容器
  slot add|list|up|stop|hold|release|rm|exec|enter
                            工作槽位
  render <网关>             预览网关的核心配置（默认隐藏凭据）
  version                   显示版本

每个子命令都可以加 -h 查看参数。
`

type app struct {
	dataDir string
	out     io.Writer
	errw    io.Writer
}

// Run 执行命令行并返回进程退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	a := &app{out: stdout, errw: stderr}
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
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	err := a.dispatch(ctx, rest[0], rest[1:])
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	}
	fmt.Fprintf(stderr, "错误：%v\n", err)
	return 1
}

var errUsage = errors.New("用法错误")

func (a *app) dispatch(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "init":
		return a.cmdInit(args)
	case "doctor":
		return a.cmdDoctor(ctx, args)
	case "status":
		return a.cmdStatus(ctx, args)
	case "node":
		return a.cmdNode(args)
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
		return fmt.Errorf("没有指定数据目录：用 --data-dir，或设置环境变量 %s", EnvDataDir)
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

func (a *app) cmdInit(args []string) error {
	fs := a.newFlags("init", "init [参数]")
	var c model.Cluster
	fs.StringVar(&c.Name, "name", model.DefaultClusterName, "容器名前缀，用来区分同一台机器上的多个数据目录")
	fs.StringVar(&c.GatewayImage, "gateway-image", model.DefaultGatewayImage, "网关镜像")
	fs.StringVar(&c.WorkerImage, "worker-image", model.DefaultWorkerImage, "默认的 worker 镜像")
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
