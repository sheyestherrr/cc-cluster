package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/store"
	"github.com/sheyestherrr/cc-cluster/internal/update"
	"github.com/sheyestherrr/cc-cluster/internal/web"
)

// DefaultWebPort 是网页界面默认的端口；被占用时依次往后试 webPortTries 个。
const (
	DefaultWebPort = 7878
	webPortTries   = 50
)

// webRun 是正在运行的网页界面留在数据目录里的记录，用来避免同一个数据目录开出两个。
// 里面有访问令牌，文件权限是 600。
type webRun struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Version string `json:"version,omitempty"`
	// 启动时的选项：换成新版本重新启动时沿用。旧版本留下的记录里没有这几项。
	Watch         string `json:"watch,omitempty"` // 持续检测的间隔，0s 表示不检测
	NoUpdateCheck bool   `json:"no_update_check,omitempty"`
	// Foreground 表示它是使用者用 --foreground 启动的（多半由 systemd 之类管理），更新时不替它重启。
	Foreground bool `json:"foreground,omitempty"`
}

// envWebChild 由 webSpawn 设给它启动的后台进程，用来和使用者自己执行的 web --foreground 区分开。
const envWebChild = "CC_CLUSTER_WEB_CHILD"

func webRunPath(dataDir string) string   { return filepath.Join(dataDir, "web.run.json") }
func webTokenPath(dataDir string) string { return filepath.Join(dataDir, "web.token") }
func webLogPath(dataDir string) string   { return filepath.Join(dataDir, "web.log") }

var tokenRe = regexp.MustCompile(`^[0-9a-f]{32,64}$`)

// webToken 返回这个数据目录的访问令牌：第一次生成后存在数据目录里（权限 600），之后一直用它。
// 令牌不变，浏览器记住一次之后直接打开地址就能进；想换掉就删除这个文件再重启网页界面。
func webToken(dataDir string) (string, error) {
	p := webTokenPath(dataDir)
	if b, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(b)); tokenRe.MatchString(t) {
			return t, nil
		}
	}
	t, err := web.NewToken()
	if err != nil {
		return "", err
	}
	return t, os.WriteFile(p, []byte(t+"\n"), 0o600)
}

// runningWeb 读出记录并确认那个进程还在、端口还在监听；否则当作没有在运行。
func runningWeb(dataDir string) (webRun, bool) {
	var r webRun
	b, err := os.ReadFile(webRunPath(dataDir))
	if err != nil || json.Unmarshal(b, &r) != nil || r.PID <= 0 || r.Port <= 0 {
		return r, false
	}
	if r.PID == os.Getpid() || syscall.Kill(r.PID, 0) != nil {
		return r, false
	}
	c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", r.Port), time.Second)
	if err != nil {
		return r, false
	}
	_ = c.Close()
	return r, true
}

// listenWeb 监听本机端口。fixed 为 true（使用者指定了端口）时只试这一个；
// 否则从 port 起往后找第一个空闲的，这样多个用户同时使用时各得一个。
func listenWeb(port int, fixed bool) (net.Listener, error) {
	tries := webPortTries
	if fixed || port == 0 {
		tries = 1
	}
	var err error
	for i := 0; i < tries; i++ {
		var ln net.Listener
		if ln, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port+i)); err == nil {
			return ln, nil
		}
	}
	if fixed {
		return nil, fmt.Errorf("监听 127.0.0.1:%d 失败：%w；可用 --port 指定其他端口", port, err)
	}
	return nil, fmt.Errorf("127.0.0.1 的 %d 至 %d 端口均无法监听：%w；可用 --port 指定其他端口", port, port+tries-1, err)
}

func (a *app) printWebAddr(port int, token string) {
	// 令牌放在 # 之后：浏览器不会把它发给服务器，也不会出现在访问记录里。
	fmt.Fprintf(a.out, "  http://127.0.0.1:%d/#token=%s\n\n", port, token)
	fmt.Fprintf(a.out, "仅监听本机地址。从其他机器访问时请先建立端口转发：ssh -L %d:127.0.0.1:%d <本机地址>\n", port, port)
	fmt.Fprintln(a.out, "使用该链接打开一次后，同一浏览器可直接访问，无需再带令牌。")
}

// webOptions 是启动网页界面时可以调的几项。
type webOptions struct {
	Port     int
	Fixed    bool // 使用者指定了端口：被占用就报错，不往后找
	Interval time.Duration
	// NoUpdateCheck 为真时不自动检查新版本。
	NoUpdateCheck bool
}

// cmdWeb 让网页界面在后台运行并打印它的地址；已经在运行时只打印地址。
// 网页界面运行期间顺带做持续检测、记录流量与连接、定时刷新订阅，所以默认放在后台，不随终端关闭而停。
func (a *app) cmdWeb(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "stop" {
		return a.cmdWebStop(args[1:])
	}
	fs := a.newFlags("web", "web [--port 端口] [--foreground]    启动网页界面（已在运行时直接使用）并输出访问地址\n  cc-cluster web stop                         停止网页界面")
	port := fs.Int("port", DefaultWebPort, "监听的本机端口；未指定且默认端口被占用时，自动改用后续的空闲端口；0 表示由系统分配")
	interval := fs.Duration("watch", engine.DefaultWatchInterval, "持续检测的间隔：定期检测各槽位的连通性与出口，出口变更时为该槽位断网并保持；0 表示不检测")
	foreground := fs.Bool("foreground", false, "在前台运行，按 Ctrl-C 停止（适用于由 systemd 等管理的场景）")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	opt := webOptions{Port: *port, Interval: *interval, NoUpdateCheck: os.Getenv(update.EnvNoCheck) != ""}
	watchSet := false
	fs.Visit(func(f *flag.Flag) {
		opt.Fixed = opt.Fixed || f.Name == "port"
		watchSet = watchSet || f.Name == "watch"
	})
	if !web.HasUI() {
		return errors.New("当前程序不含网页界面。发布版已内置；从源码构建时请先执行 make web（需要 Node.js 与 pnpm），再执行 make build")
	}
	if err := a.requireDataDir(); err != nil {
		return err
	}
	if _, err := store.Open(a.dataDir); err != nil {
		return err
	}
	if r, ok := runningWeb(a.dataDir); ok {
		if r.Version == Version {
			fmt.Fprintf(a.out, "网页界面已在后台运行（进程 %d），请在本机浏览器中打开：\n\n", r.PID)
			a.printWebAddr(r.Port, r.Token)
			fmt.Fprintln(a.out, "停止：cc-cluster web stop")
			return nil
		}
		// 二进制升级过，在运行的还是旧版本。以前台方式运行的由启动它的一方（systemd 之类）负责重启，这里不抢。
		if r.Foreground && !*foreground {
			fmt.Fprintf(a.out, "网页界面以前台方式运行（进程 %d，版本 %s），请手动重启以使用 %s。当前地址：\n\n", r.PID, orDash(r.Version), Version)
			a.printWebAddr(r.Port, r.Token)
			return nil
		}
		fmt.Fprintf(a.out, "网页界面版本为 %s，正在重启为 %s …\n", orDash(r.Version), Version)
		if err := stopWeb(a.dataDir, r); err != nil {
			return err
		}
		opt = inheritWebOptions(opt, watchSet, r)
	}
	if *foreground {
		return a.webServe(ctx, opt)
	}
	r, err := a.webSpawn(opt)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "网页界面已在后台运行（进程 %d），请在本机浏览器中打开：\n\n", r.PID)
	a.printWebAddr(r.Port, r.Token)
	fmt.Fprintln(a.out, "停止：cc-cluster web stop")
	return nil
}

// inheritWebOptions 让换了版本之后重新启动的网页界面沿用原来那一份的选项；这次明确指定了的以这次的为准。
// 不能因为换了版本就把关掉的持续检测打开。旧版本留下的记录里没有这些选项，那时用这次的。
func inheritWebOptions(opt webOptions, watchSet bool, r webRun) webOptions {
	if !opt.Fixed {
		opt.Port = r.Port // 尽量沿用原来的端口，浏览器里记着的地址不用变；被占用了仍会往后找
	}
	if d, err := time.ParseDuration(r.Watch); err == nil && !watchSet {
		opt.Interval = d
	}
	opt.NoUpdateCheck = opt.NoUpdateCheck || r.NoUpdateCheck
	return opt
}

// webServe 在前台提供网页界面，直到收到中断信号。
func (a *app) webServe(ctx context.Context, opt webOptions) error {
	token, err := webToken(a.dataDir)
	if err != nil {
		return err
	}
	// 由 webSpawn 启动的不算“在前台运行”。这个标记只给本进程看，不传给它再启动的进程。
	foreground := os.Getenv(envWebChild) == ""
	os.Unsetenv(envWebChild)
	srv, err := web.New(web.Options{
		DataDir: a.dataDir, Version: Version, NoWatch: opt.Interval <= 0, WatchInterval: opt.Interval, Token: token,
		NoUpdateCheck: opt.NoUpdateCheck,
	})
	if err != nil {
		return err
	}
	ln, err := listenWeb(opt.Port, opt.Fixed)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := ln.Addr().(*net.TCPAddr)
	run := webRun{PID: os.Getpid(), Port: addr.Port, Token: token, Version: Version, Watch: max(opt.Interval, 0).String(), NoUpdateCheck: opt.NoUpdateCheck, Foreground: foreground}
	if b, err := json.Marshal(run); err == nil {
		if err := os.WriteFile(webRunPath(a.dataDir), b, 0o600); err == nil {
			defer os.Remove(webRunPath(a.dataDir))
		}
	}
	if !opt.Fixed && opt.Port != 0 && addr.Port != opt.Port {
		fmt.Fprintf(a.out, "端口 %d 已被占用，改用 %d。\n", opt.Port, addr.Port)
	}
	fmt.Fprint(a.out, "网页界面已启动，请在本机浏览器中打开：\n\n")
	a.printWebAddr(addr.Port, token)
	fmt.Fprintln(a.out, "按 Ctrl-C 停止。")
	return srv.Serve(ctx, ln)
}

// webSpawn 把自己再启动一份放到后台（web --foreground，脱离当前终端），等它就绪后返回它的记录。
// 它的输出写到数据目录的 web.log。
func (a *app) webSpawn(opt webOptions) (webRun, error) {
	var none webRun
	exe, err := os.Executable()
	if err != nil {
		return none, err
	}
	dir, err := filepath.Abs(a.dataDir)
	if err != nil {
		return none, err
	}
	logf, err := os.OpenFile(webLogPath(dir), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return none, err
	}
	defer logf.Close()
	argv := []string{"--data-dir", dir, "web", "--foreground", "--watch", opt.Interval.String()}
	if opt.Fixed || opt.Port != DefaultWebPort {
		argv = append(argv, "--port", strconv.Itoa(opt.Port))
	}
	cmd := exec.Command(exe, argv...)
	cmd.Env = append(os.Environ(), envWebChild+"=1")
	if opt.NoUpdateCheck {
		cmd.Env = append(cmd.Env, update.EnvNoCheck+"=1")
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // 自成一个会话：关掉终端不会带走它
	if err := cmd.Start(); err != nil {
		return none, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(20 * time.Second)
	for {
		if r, ok := runningWeb(dir); ok && r.PID == cmd.Process.Pid {
			return r, nil
		}
		select {
		case <-exited:
			return none, fmt.Errorf("网页界面启动失败：%s", lastLogLines(webLogPath(dir)))
		case <-deadline:
			_ = cmd.Process.Kill()
			return none, fmt.Errorf("网页界面在 20 秒内未就绪：%s", lastLogLines(webLogPath(dir)))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// lastLogLines 取日志的最后几行，用来说明后台进程为什么没起来。
func lastLogLines(path string) string {
	b, err := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if err != nil || lines[0] == "" {
		return "无输出，日志位于 " + path
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "；")
}

// stopWeb 让记录里的那个进程退出，并等它把端口放掉。
func stopWeb(dataDir string, r webRun) error {
	if err := syscall.Kill(r.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("无法停止进程 %d：%w", r.PID, err)
	}
	for i := 0; i < 100; i++ {
		if _, ok := runningWeb(dataDir); !ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("进程 %d 在 10 秒内未退出", r.PID)
}

// cmdWebStop 停止在后台运行的网页界面。
func (a *app) cmdWebStop(args []string) error {
	fs := a.newFlags("web stop", "web stop")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := a.requireDataDir(); err != nil {
		return err
	}
	r, ok := runningWeb(a.dataDir)
	if !ok {
		fmt.Fprintln(a.out, "网页界面未运行。")
		return nil
	}
	if err := stopWeb(a.dataDir, r); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "网页界面已停止。持续检测、流量记录与订阅的自动刷新同时停止；槽位与网关不受影响。")
	return nil
}

// ensureWeb 在进入槽位前确认后台的网页界面（连同持续检测）在运行，没有就启动它。
// 只是顺手的事：做不到时说一声，不妨碍进入槽位。
func (a *app) ensureWeb() {
	if !a.interactive || !web.HasUI() {
		return
	}
	if _, ok := runningWeb(a.dataDir); ok {
		return
	}
	r, err := a.webSpawn(webOptions{Port: DefaultWebPort, Interval: engine.DefaultWatchInterval})
	if err != nil {
		fmt.Fprintf(a.errw, "后台的持续检测启动失败（不影响进入槽位）：%v\n", err)
		return
	}
	fmt.Fprintf(a.errw, "已在后台启动持续检测与网页界面（端口 %d；执行 cc-cluster web 可获取访问地址）\n", r.Port)
}

// cmdWatch 在前台做持续检测：给不开网页界面的人用，逻辑与网页运行期间的相同。
func (a *app) cmdWatch(ctx context.Context, args []string) error {
	fs := a.newFlags("watch", "watch [--interval 间隔] [--once]")
	interval := fs.Duration("interval", engine.DefaultWatchInterval, "两次检测之间的间隔")
	once := fs.Bool("once", false, "仅检测一次并列出结果")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *once {
		eng, _, err := a.open(ctx, true)
		if err != nil {
			return err
		}
		res, err := eng.WatchOnce(ctx)
		if err != nil {
			return err
		}
		if len(res) == 0 {
			fmt.Fprintln(a.out, "没有已就绪的槽位")
			return nil
		}
		w := a.table()
		fmt.Fprintln(w, "槽位\t结果\t出口\t说明")
		for _, r := range res {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Slot, watchText[r.Status], orDash(r.ExitIP), r.Detail)
		}
		return w.Flush()
	}
	if *interval < 30*time.Second {
		return errors.New("间隔不能短于 30 秒")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(a.out, "每 %s 检测一次已就绪槽位的连通性与出口；出口变更时为该槽位断网并保持。运行期间同时记录各槽位的流量与连接。按 Ctrl-C 停止。\n", *interval)
	open := func(ctx context.Context) (*engine.Engine, error) {
		eng, _, err := a.open(ctx, true)
		return eng, err
	}
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		engine.SampleLoop(ctx, engine.NewTraffic(), 0, open)
	}()
	engine.Watch(ctx, *interval, open)
	<-sampled // 等采样循环把还没落盘的那一段写完
	return nil
}

var watchText = map[string]string{
	engine.WatchOK: "正常", engine.WatchUnreachable: "无法连接", engine.WatchChanged: "出口已变更，已断网",
}
