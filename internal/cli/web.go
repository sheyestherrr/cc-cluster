package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/sheyestherrr/cc-cluster/internal/store"
	"github.com/sheyestherrr/cc-cluster/internal/web"
)

// cmdWeb 启动网页界面：只监听本机，地址里带一次性的访问令牌。
func (a *app) cmdWeb(ctx context.Context, args []string) error {
	fs := a.newFlags("web", "web [--port 端口]")
	port := fs.Int("port", 7878, "监听的本机端口；0 表示让系统挑一个空闲端口")
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
	srv, err := web.New(web.Options{DataDir: a.dataDir, Version: Version})
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
