package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

// claudeInstall 是 Claude Code 官方的原生安装方式：装进当前用户的 HOME，之后自行更新。
const claudeInstall = "curl -fsSL https://claude.ai/install.sh | bash"

// enterRequest 是一次"进入槽位"的全部输入。
type enterRequest struct {
	Slot string
	// Dir 是宿主上的目录，已经是绝对路径。
	Dir string
	// MountRoot 非空时，目录不在挂载范围内就挂这一级；为空则询问（或在 Yes 时挂 Dir 本身）。
	MountRoot string
	Yes       bool
	// Claude 为 true 时启动 claude，否则打开登录 shell；Command 非空时执行它。
	Claude  bool
	Command []string
}

// splitEnterArgs 拆出 enter / claude 命令开头的位置参数：[槽位] [目录]。
// 槽位 ID 里不会有斜杠和点，所以带斜杠或以点开头的那个是目录。
func splitEnterArgs(args []string) (slot, dir string, rest []string, err error) {
	i := 0
	for ; i < len(args) && i < 2 && !strings.HasPrefix(args[i], "-"); i++ {
		v := args[i]
		isPath := strings.ContainsRune(v, filepath.Separator) || strings.HasPrefix(v, ".") || strings.HasPrefix(v, "~")
		switch {
		case isPath && dir == "":
			dir = v
		case !isPath && slot == "" && dir == "":
			slot = v
		default:
			return "", "", nil, fmt.Errorf("无法理解参数 %q：用法是 [槽位] [目录]", v)
		}
	}
	return slot, dir, args[i:], nil
}

func (a *app) cmdEnter(ctx context.Context, name string, args []string) error {
	synopsis := name + " [槽位] [目录] [参数] [-- 命令 …]"
	if name == "claude" {
		synopsis = name + " [槽位] [目录] [参数] [-- claude 的参数 …]"
	}
	fs := a.newFlags(name, synopsis)
	req := enterRequest{Claude: name == "claude"}
	fs.StringVar(&req.MountRoot, "mount", "", "目录不在该槽的挂载范围内时，挂载这一级目录（该目录本身或它的上级）；不填则询问")
	fs.BoolVar(&req.Yes, "yes", false, "不询问：需要时直接挂载并重建 worker")
	usage := fs.Usage
	fs.Usage = func() {
		usage()
		fmt.Fprint(a.errw, `
槽位省略时：只有一个槽位就用它，否则列出来让你选。
目录省略时用当前目录；相对路径按当前目录解析。目录在 worker 内的路径与宿主相同。
目录已经在该槽的挂载范围内（包括是已挂载目录的子目录）时直接进入；
否则先把它（或你选的上级目录）加进挂载并重建 worker，HOME 保留，容器内正在运行的进程会结束。
`)
	}
	slot, dir, rest, err := splitEnterArgs(args)
	if err != nil {
		fs.Usage()
		return errUsage
	}
	if err := parse(fs, rest); err != nil {
		return err
	}
	req.Slot, req.Command = slot, fs.Args()

	if dir == "" {
		dir = "."
	}
	if req.Dir, err = filepath.Abs(dir); err != nil {
		return err
	}
	if req.MountRoot != "" {
		if req.MountRoot, err = filepath.Abs(req.MountRoot); err != nil {
			return err
		}
	}
	return a.enter(ctx, req)
}

// pickSlot 在没有指定槽位时决定用哪一个。
func (a *app) pickSlot(ctx context.Context, eng *engine.Engine, title string) (string, error) {
	sts, err := eng.SlotStatuses(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case len(sts) == 0:
		return "", errors.New("还没有槽位；先用 slot add 登记一个（或运行不带参数的 cc-cluster 按提示操作）")
	case len(sts) == 1:
		return sts[0].Slot.ID, nil
	case !a.interactive:
		return "", errors.New("有多个槽位，需要指定用哪一个")
	}
	items := make([]string, len(sts))
	for i, s := range sts {
		items[i] = fmt.Sprintf("%-12s %-8s 落地 %s  出口 %s", s.Slot.ID, s.State, s.Slot.Landing, orDash(s.ExitIP))
	}
	i, err := a.choose(title, items, -1)
	if err != nil {
		return "", err
	}
	return sts[i].Slot.ID, nil
}

// enter 进入槽位：必要时挂载目录、重建并启动 worker，然后把终端交给容器里的程序。
func (a *app) enter(ctx context.Context, req enterRequest) error {
	eng, st, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	if req.Slot == "" {
		if req.Slot, err = a.pickSlot(ctx, eng, "进入哪个槽位？"); err != nil {
			return err
		}
	}
	sl, err := st.Slot(req.Slot)
	if err != nil {
		return err
	}
	ident, _, err := st.Identity(req.Slot)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(req.Dir); err != nil {
		return fmt.Errorf("目录 %s 不可用：%w", req.Dir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s 不是目录", req.Dir)
	}
	status, err := eng.SlotStatus(ctx, req.Slot)
	if err != nil {
		return err
	}

	inside, mounted := model.ContainerPath(sl.Mounts, req.Dir)
	remounted := false
	if !mounted {
		root, err := a.pickMountRoot(req, ident.Home())
		if err != nil {
			return err
		}
		exists := status.State != engine.StateAbsent
		if exists && !req.Yes {
			if !a.interactive {
				return fmt.Errorf("目录 %s 不在槽位 %s 的挂载范围内；加上 --yes 以挂载并重建 worker（容器内正在运行的进程会结束）", req.Dir, req.Slot)
			}
			ok, err := a.confirm(fmt.Sprintf("把 %s 挂进槽位 %s 需要重建它的 worker：HOME 保留，容器内正在运行的进程会结束。继续吗？", root, req.Slot), false)
			if err != nil {
				return err
			}
			if !ok {
				return errCancelled
			}
		}
		if err := eng.SlotAddWorkspace(req.Slot, root); err != nil {
			return err
		}
		fmt.Fprintf(a.errw, "已把 %s 加进槽位 %s 的挂载\n", root, req.Slot)
		remounted = true
		inside, _ = model.ContainerPath(model.AddWorkspace(sl.Mounts, root), req.Dir)
	}

	if remounted || status.State != engine.StateReady {
		if status.State == engine.StateHold {
			return fmt.Errorf("槽位 %s 处于手动断网（%s）；确认后先执行 slot release %s", req.Slot, orDash(status.HoldReason), req.Slot)
		}
		res, err := eng.SlotUp(ctx, req.Slot, engine.UpOptions{Recreate: remounted})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.errw, "槽位 %s 已就绪：出口 %s\n", req.Slot, res.ExitSummary())
	}

	docker, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("进入槽位需要 docker 命令行，PATH 里没有找到")
	}
	worker := eng.WorkerContainer(req.Slot)
	if err := a.offerClaudeInstall(docker, worker, st, req); err != nil {
		return err
	}

	argv := []string{"docker", "exec", "-i"}
	if a.interactive {
		argv = append(argv, "-t")
	}
	argv = append(argv, "-w", inside, worker)
	switch {
	case req.Claude:
		argv = append(append(argv, "claude"), req.Command...)
	case len(req.Command) > 0:
		argv = append(argv, req.Command...)
	default:
		argv = append(argv, sl.Shell, "-l")
	}
	if a.interactive {
		// 终端交给 docker 命令行处理：本进程被它替换。整个程序只在这里和下面用到 docker 命令行。
		return syscall.Exec(docker, argv, os.Environ())
	}
	// 没有终端时（脚本、管道）作为子进程运行，输出照常转交。
	cmd := exec.CommandContext(ctx, docker, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.out, a.errw
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("命令退出码 %d", ee.ExitCode())
		}
		return err
	}
	return nil
}

// pickMountRoot 决定把哪一级目录挂进槽位。
func (a *app) pickMountRoot(req enterRequest, home string) (string, error) {
	candidates := model.WorkspaceCandidates(req.Dir, home)
	if len(candidates) == 0 {
		return "", fmt.Errorf("目录 %s 不能挂进槽位：它在系统目录里，或会盖住槽内的 HOME（%s）", req.Dir, home)
	}
	if req.MountRoot != "" {
		for _, c := range candidates {
			if c == filepath.Clean(req.MountRoot) {
				return c, nil
			}
		}
		return "", fmt.Errorf("--mount 给出的 %s 不是 %s 本身或它的上级目录，或者不允许挂载", req.MountRoot, req.Dir)
	}
	if req.Yes || !a.interactive || len(candidates) == 1 {
		return candidates[0], nil
	}
	items := make([]string, len(candidates))
	for i, c := range candidates {
		items[i] = c
		if i == 0 {
			items[i] += "（当前目录）"
		}
	}
	fmt.Fprintf(a.out, "目录 %s 还不在槽位 %s 的挂载范围内。\n", req.Dir, req.Slot)
	i, err := a.choose("挂载哪一级？挂上级目录的话，以后在它下面的其他项目里进入就不用再重建。", items, 0)
	if err != nil {
		return "", err
	}
	return candidates[i], nil
}

// offerClaudeInstall 在槽内还没有 Claude Code 时询问是否安装。
// 安装在 worker 里进行，下载经该槽绑定的出口；装进该槽的 HOME，随 HOME 持久化。
func (a *app) offerClaudeInstall(docker, worker string, st *store.Store, req enterRequest) error {
	if _, err := os.Lstat(filepath.Join(st.HomeDir(req.Slot), ".local", "bin", "claude")); err == nil {
		return nil
	}
	if !a.interactive {
		if req.Claude {
			return fmt.Errorf("槽位 %s 里还没有安装 Claude Code；在终端里运行 cc-cluster claude %s 会提示安装", req.Slot, req.Slot)
		}
		return nil
	}
	ok, err := a.confirm(fmt.Sprintf("槽位 %s 里还没有安装 Claude Code。现在安装吗？（在槽内执行官方安装脚本，下载经该槽的出口）", req.Slot), req.Claude)
	if err != nil {
		return err
	}
	if !ok {
		if req.Claude {
			return errCancelled
		}
		return nil
	}
	cmd := exec.Command(docker, "exec", "-it", worker, "/bin/sh", "-lc", claudeInstall)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("安装 Claude Code 失败：%w", err)
	}
	return nil
}
