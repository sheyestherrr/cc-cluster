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
}

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
		return nil, fmt.Errorf("监听 127.0.0.1:%d 失败：%w；可以用 --port 换一个端口", port, err)
	}
	return nil, fmt.Errorf("127.0.0.1 的 %d 到 %d 端口都无法监听：%w；可以用 --port 指定一个", port, port+tries-1, err)
}

func (a *app) printWebAddr(port int, token string) {
	// 令牌放在 # 之后：浏览器不会把它发给服务器，也不会出现在访问记录里。
	fmt.Fprintf(a.out, "  http://127.0.0.1:%d/#token=%s\n\n", port, token)
	fmt.Fprintf(a.out, "只监听本机。在别的机器上使用时先做端口转发：ssh -L %d:127.0.0.1:%d <这台机器>\n", port, port)
	fmt.Fprintln(a.out, "用这个链接打开一次之后，同一个浏览器直接输地址就能进。")
}

// webOptions 是启动网页界面时可以调的几项。
type webOptions struct {
	Port     int
	Fixed    bool // 使用者指定了端口：被占用就报错，不往后找
	Interval time.Duration
}

// cmdWeb 让网页界面在后台运行并打印它的地址；已经在运行时只打印地址。
// 网页界面运行期间顺带做持续检测、记录流量与连接、定时刷新订阅，所以默认放在后台，不随终端关闭而停。
func (a *app) cmdWeb(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "stop" {
		return a.cmdWebStop(args[1:])
	}
	fs := a.newFlags("web", "web [--port 端口] [--foreground]    启动（或找到已在运行的）网页界面并打印地址\n  cc-cluster web stop                         停止它")
	port := fs.Int("port", DefaultWebPort, "监听的本机端口；不指定时被占用会自动往后找空闲的；0 表示让系统挑一个")
	interval := fs.Duration("watch", engine.DefaultWatchInterval, "运行期间每隔这么久检测一遍各槽的连通性与出口，出口变了就给该槽断网并保持；0 表示不检测")
	foreground := fs.Bool("foreground", false, "在前台运行，按 Ctrl-C 停止（交给 systemd 之类的管理时用）")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	opt := webOptions{Port: *port, Interval: *interval}
	fs.Visit(func(f *flag.Flag) { opt.Fixed = opt.Fixed || f.Name == "port" })
	if !web.HasUI() {
		return errors.New("这个二进制不带网页界面：发布版自带；从源码构建时先执行 make web（需要 Node.js 与 pnpm），再 make build")
	}
	if err := a.requireDataDir(); err != nil {
		return err
	}
	if _, err := store.Open(a.dataDir); err != nil {
		return err
	}
	if r, ok := runningWeb(a.dataDir); ok {
		if r.Version == Version {
			fmt.Fprintf(a.out, "网页界面已在后台运行（进程 %d），在这台机器的浏览器里打开：\n\n", r.PID)
			a.printWebAddr(r.Port, r.Token)
			fmt.Fprintln(a.out, "停止：cc-cluster web stop")
			return nil
		}
		// 二进制升级过：在跑的还是旧版本，换成现在这个。
		fmt.Fprintf(a.out, "在运行的网页界面是 %s，现在的版本是 %s：重新启动它。\n", orDash(r.Version), Version)
		if err := stopWeb(a.dataDir, r); err != nil {
			return err
		}
		if !opt.Fixed {
			opt.Port, opt.Fixed = r.Port, false // 尽量沿用原来的端口，浏览器里记着的地址不用变
		}
	}
	if *foreground {
		return a.webServe(ctx, opt)
	}
	r, err := a.webSpawn(opt)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "网页界面已在后台运行（进程 %d），在这台机器的浏览器里打开：\n\n", r.PID)
	a.printWebAddr(r.Port, r.Token)
	fmt.Fprintln(a.out, "停止：cc-cluster web stop")
	return nil
}

// webServe 在前台提供网页界面，直到收到中断信号。
func (a *app) webServe(ctx context.Context, opt webOptions) error {
	token, err := webToken(a.dataDir)
	if err != nil {
		return err
	}
	srv, err := web.New(web.Options{
		DataDir: a.dataDir, Version: Version, NoWatch: opt.Interval <= 0, WatchInterval: opt.Interval, Token: token,
		NoUpdateCheck: os.Getenv(update.EnvNoCheck) != "",
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
	if b, err := json.Marshal(webRun{PID: os.Getpid(), Port: addr.Port, Token: token, Version: Version}); err == nil {
		if err := os.WriteFile(webRunPath(a.dataDir), b, 0o600); err == nil {
			defer os.Remove(webRunPath(a.dataDir))
		}
	}
	if !opt.Fixed && opt.Port != 0 && addr.Port != opt.Port {
		fmt.Fprintf(a.out, "端口 %d 已被占用，改用 %d。\n", opt.Port, addr.Port)
	}
	fmt.Fprint(a.out, "网页界面已启动，在这台机器的浏览器里打开：\n\n")
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
			return none, fmt.Errorf("网页界面没有启动起来：%s", lastLogLines(webLogPath(dir)))
		case <-deadline:
			_ = cmd.Process.Kill()
			return none, fmt.Errorf("网页界面 20 秒内没有就绪：%s", lastLogLines(webLogPath(dir)))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// lastLogLines 取日志的最后几行，用来说明后台进程为什么没起来。
func lastLogLines(path string) string {
	b, err := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if err != nil || lines[0] == "" {
		return "没有留下输出，日志在 " + path
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "；")
}

// stopWeb 让记录里的那个进程退出，并等它把端口放掉。
func stopWeb(dataDir string, r webRun) error {
	if err := syscall.Kill(r.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("停不掉进程 %d：%w", r.PID, err)
	}
	for i := 0; i < 100; i++ {
		if _, ok := runningWeb(dataDir); !ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("进程 %d 在 10 秒内没有退出", r.PID)
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
		fmt.Fprintln(a.out, "网页界面没有在运行。")
		return nil
	}
	if err := stopWeb(a.dataDir, r); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "网页界面已停止。持续检测、流量记录和订阅的自动刷新也随之停止；槽位和网关不受影响。")
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
		fmt.Fprintf(a.errw, "后台的持续检测没有启动起来（不影响进入槽位）：%v\n", err)
		return
	}
	fmt.Fprintf(a.errw, "已在后台启动持续检测与网页界面（端口 %d；要打开网页执行 cc-cluster web）\n", r.Port)
}

// cmdWatch 在前台做持续检测：给不开网页界面的人用，逻辑与网页运行期间的相同。
func (a *app) cmdWatch(ctx context.Context, args []string) error {
	fs := a.newFlags("watch", "watch [--interval 间隔] [--once]")
	interval := fs.Duration("interval", engine.DefaultWatchInterval, "两次检测之间的间隔")
	once := fs.Bool("once", false, "只检测一遍并列出结果")
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
			fmt.Fprintln(a.out, "没有已就绪的槽位可检测")
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
		return errors.New("间隔最短 30 秒")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(a.out, "每 %s 检测一遍已就绪槽位的连通性与出口；出口变了会给该槽断网并保持。运行期间同时记录各槽位的流量与连接。按 Ctrl-C 停止。\n", *interval)
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
	engine.WatchOK: "正常", engine.WatchUnreachable: "连不上", engine.WatchChanged: "出口变了，已断网",
}
