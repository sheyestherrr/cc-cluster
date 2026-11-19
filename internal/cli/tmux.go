package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// cmdTmux 管理槽位的宿主 tmux 桥接：开关、接入观察、结束。
func (a *app) cmdTmux(ctx context.Context, args []string) error {
	fs := a.newFlags("tmux", "tmux <槽位> [--on|--off|--kill|--socket] [参数]")
	on := fs.Bool("on", false, "开启该槽的宿主 tmux 桥接（下次 slot up --recreate 时生效）")
	off := fs.Bool("off", false, "关闭桥接（下次 slot up --recreate 时生效；不结束宿主上的 tmux）")
	kill := fs.Bool("kill", false, "结束该槽的宿主 tmux，连同其中所有 pane 里的程序")
	sock := fs.Bool("socket", false, "只打印该槽的 tmux socket 在宿主上的路径")
	tools := fs.String("tools", "", "与 --on 一起用：开放给 Claude 的工具范围，deny:项,项 或 allow:项,项；不填为 "+model.DefaultTmuxTools)
	shell := fs.String("shell", "", "与 --on 一起用：宿主 pane 里的 shell 类型 bash|zsh|fish；不填按当前的 SHELL")
	yes := fs.Bool("yes", false, "与 --kill 一起用：不询问")
	usage := fs.Usage
	fs.Usage = func() {
		usage()
		fmt.Fprint(a.errw, `
不带参数时接入该槽的宿主 tmux（没有在运行就先启动），用来观察或接管；按 tmux 的方式分离（默认 Ctrl-b d）。

桥接让槽内的 Claude 经 tmux-mcp 操作宿主上这一个专用的 tmux：在其中执行的命令是宿主上的命令，
用的是你的权限和宿主网络，不受该槽的网络约束，手动断网（hold）也拦不住它。默认关闭，按槽位开启。
工具范围只约束 tmux-mcp 提供的工具；槽内的程序仍然可以直接用 tmux 命令连这个 socket。
`)
	}
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	n := 0
	for _, b := range []bool{*on, *off, *kill, *sock} {
		if b {
			n++
		}
	}
	if n > 1 || fs.NArg() > 0 || ((*tools != "" || *shell != "") && !*on) {
		fs.Usage()
		return errUsage
	}
	eng, st, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	sl, err := st.Slot(id)
	if err != nil {
		return err
	}

	switch {
	case *sock:
		fmt.Fprintln(a.out, eng.TmuxSocket(id))
		return nil
	case *on, *off:
		out, err := eng.SetTmuxBridge(id, engine.TmuxSettings{On: *on, Tools: *tools, Shell: *shell})
		if err != nil {
			return err
		}
		if out.TmuxBridge {
			fmt.Fprintf(a.out, "槽位 %s 已开启宿主 tmux 桥接（工具范围 %s，shell %s）；执行 slot up %s --recreate 后生效\n",
				id, out.EffectiveTmuxTools(), out.TmuxShell, id)
			fmt.Fprintln(a.errw, bridgeNotice)
		} else {
			fmt.Fprintf(a.out, "槽位 %s 已关闭宿主 tmux 桥接；执行 slot up %s --recreate 后生效\n", id, id)
			if eng.TmuxAlive(ctx, id) {
				fmt.Fprintf(a.errw, "提醒：该槽的宿主 tmux 仍在运行；不再需要时执行 cc-cluster tmux %s --kill\n", id)
			}
		}
		return nil
	case *kill:
		if !eng.TmuxAlive(ctx, id) {
			fmt.Fprintf(a.out, "槽位 %s 的宿主 tmux 没有在运行\n", id)
			return nil
		}
		if !*yes {
			if !a.interactive {
				return errors.New("结束宿主 tmux 会终止其中所有 pane 里的程序；确认后加上 --yes")
			}
			ok, err := a.confirm(fmt.Sprintf("结束槽位 %s 的宿主 tmux？其中所有 pane 里正在运行的程序都会被终止。", id), false)
			if err != nil {
				return err
			}
			if !ok {
				return errCancelled
			}
		}
		if err := eng.TmuxKill(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已结束槽位 %s 的宿主 tmux\n", id)
		return nil
	}

	if !sl.TmuxBridge {
		return fmt.Errorf("槽位 %s 没有开启宿主 tmux 桥接；用 cc-cluster tmux %s --on 开启", id, id)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := eng.TmuxEnsure(ctx, id, cwd); err != nil {
		return err
	}
	bin, argv, env, err := eng.TmuxAttach(id)
	if err != nil {
		return err
	}
	if !a.interactive {
		fmt.Fprintf(a.out, "宿主 tmux 在运行。在终端里接入：%s %s\n", bin, strings.Join(argv[1:], " "))
		return nil
	}
	// 终端交给 tmux：本进程被它替换。
	return syscall.Exec(bin, argv, env)
}
