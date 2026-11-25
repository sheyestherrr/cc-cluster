package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/store"
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
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func webRunPath(dataDir string) string { return filepath.Join(dataDir, "web.run.json") }

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
}

// cmdWeb 启动网页界面：只监听本机，地址里带一次性的访问令牌。
func (a *app) cmdWeb(ctx context.Context, args []string) error {
	fs := a.newFlags("web", "web [--port 端口]")
	port := fs.Int("port", DefaultWebPort, "监听的本机端口；不指定时被占用会自动往后找空闲的；0 表示让系统挑一个")
	interval := fs.Duration("watch", engine.DefaultWatchInterval, "运行期间每隔这么久检测一遍各槽的连通性与出口，出口变了就给该槽断网并保持；0 表示不检测")
	if err := parse(fs, args); err != nil {
		return err
	}
	fixed := false
	fs.Visit(func(f *flag.Flag) { fixed = fixed || f.Name == "port" })
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
		fmt.Fprintf(a.out, "网页界面已经在运行（进程 %d），没有再启动一个。在这台机器的浏览器里打开：\n\n", r.PID)
		a.printWebAddr(r.Port, r.Token)
		fmt.Fprintf(a.out, "要重新启动，先停掉它：kill %d\n", r.PID)
		return nil
	}
	srv, err := web.New(web.Options{DataDir: a.dataDir, Version: Version, NoWatch: *interval <= 0, WatchInterval: *interval})
	if err != nil {
		return err
	}
	ln, err := listenWeb(*port, fixed)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := ln.Addr().(*net.TCPAddr)
	if b, err := json.Marshal(webRun{PID: os.Getpid(), Port: addr.Port, Token: srv.Token()}); err == nil {
		if err := os.WriteFile(webRunPath(a.dataDir), b, 0o600); err == nil {
			defer os.Remove(webRunPath(a.dataDir))
		}
	}
	if !fixed && *port != 0 && addr.Port != *port {
		fmt.Fprintf(a.out, "端口 %d 已被占用，改用 %d。\n", *port, addr.Port)
	}
	fmt.Fprint(a.out, "网页界面已启动，在这台机器的浏览器里打开：\n\n")
	a.printWebAddr(addr.Port, srv.Token())
	fmt.Fprintln(a.out, "按 Ctrl-C 停止。")
	return srv.Serve(ctx, ln)
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
