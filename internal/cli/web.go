package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/store"
	"github.com/sheyestherrr/cc-cluster/internal/web"
)

// cmdWeb 启动网页界面：只监听本机，地址里带一次性的访问令牌。
func (a *app) cmdWeb(ctx context.Context, args []string) error {
	fs := a.newFlags("web", "web [--port 端口]")
	port := fs.Int("port", 7878, "监听的本机端口；0 表示让系统挑一个空闲端口")
	interval := fs.Duration("watch", engine.DefaultWatchInterval, "运行期间每隔这么久检测一遍各槽的连通性与出口，出口变了就给该槽断网并保持；0 表示不检测")
	if err := parse(fs, args); err != nil {
		return err
	}
	if !web.HasUI() {
		return errors.New("这个二进制不带网页界面：发布版自带；从源码构建时先执行 make web（需要 Node.js 与 pnpm），再 make build")
	}
	if err := a.requireDataDir(); err != nil {
		return err
	}
	if _, err := store.Open(a.dataDir); err != nil {
		return err
	}
	srv, err := web.New(web.Options{DataDir: a.dataDir, Version: Version, NoWatch: *interval <= 0, WatchInterval: *interval})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return fmt.Errorf("监听 127.0.0.1:%d 失败：%w；可以用 --port 换一个端口", *port, err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := ln.Addr().(*net.TCPAddr)
	// 令牌放在 # 之后：浏览器不会把它发给服务器，也不会出现在访问记录里。
	fmt.Fprintf(a.out, "网页界面已启动，在这台机器的浏览器里打开：\n\n  http://127.0.0.1:%d/#token=%s\n\n", addr.Port, srv.Token())
	fmt.Fprintf(a.out, "只监听本机。在别的机器上使用时先做端口转发：ssh -L %d:127.0.0.1:%d <这台机器>\n按 Ctrl-C 停止。\n", addr.Port, addr.Port)
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
	fmt.Fprintf(a.out, "每 %s 检测一遍已就绪槽位的连通性与出口；出口变了会给该槽断网并保持。按 Ctrl-C 停止。\n", *interval)
	engine.Watch(ctx, *interval, func(ctx context.Context) (*engine.Engine, error) {
		eng, _, err := a.open(ctx, true)
		return eng, err
	})
	return nil
}

var watchText = map[string]string{
	engine.WatchOK: "正常", engine.WatchUnreachable: "连不上", engine.WatchChanged: "出口变了，已断网",
}
