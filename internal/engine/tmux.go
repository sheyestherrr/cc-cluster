package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 宿主 tmux 桥接。
//
// 开启后，每个槽位在宿主上有一个专用的 tmux server，它的 socket 放在该槽数据目录的 tmux/ 下；
// 这个目录（不是 socket 文件本身，这样 server 重建后 worker 仍看得到新的 socket）挂进 worker。
// 槽内的 tmux-mcp 用镜像里的 tmux 客户端连它，于是 Claude 能操作宿主上的终端。
//
// 目录在 worker 里的路径与宿主上相同。tmux-mcp 跟踪一条命令是否结束的办法，是在发给 pane 的命令后面
// 接一句 tmux -S <socket> wait-for …，这一句在宿主的 pane 里执行；两边路径不同的话，
// 宿主上找不到那个 socket，命令虽然执行了，结果却等不回来。
//
// 经桥接执行的命令在宿主上运行：用的是启动 tmux server 的那个用户的权限和宿主的网络，
// 不经过网关，也不受该槽断网与否的影响。这是用户按槽位选择的信任边界。
const (
	tmuxSocketName = "sock"
	// TmuxMCPConfig 是写进 worker 的 MCP 服务器配置，启动 Claude Code 时经 --mcp-config 给出。
	TmuxMCPConfig = "/etc/tmux-mcp/mcp.json"
	// tmuxPolicy 是写进 worker 的 tmux-mcp 配置（工具范围与允许的 socket）。
	tmuxPolicy = "/etc/tmux-mcp/config.toml"
	// TmuxSession 是管理器启动 server 时建的第一个 session。
	TmuxSession = "main"
	// Unix socket 的路径放在 sockaddr_un.sun_path 里，Linux 上连同结尾的 0 最多 108 字节。
	maxSocketPath = 107
)

// TmuxHost 描述怎样在宿主上运行 tmux。零值表示用 PATH 里的 tmux、它自己的默认配置和本进程的环境。
type TmuxHost struct {
	// Bin 是 tmux 可执行文件；为空则在 PATH 里找。
	Bin string
	// Conf 传给 tmux 的 -f；为空则由 tmux 读它的默认配置。
	Conf string
	// Env 是追加到 tmux 环境里的 名=值。
	Env []string
}

// TmuxSocket 返回槽位的 tmux socket 在宿主上的路径。
func (e *Engine) TmuxSocket(id string) string {
	return filepath.Join(e.st.TmuxDir(id), tmuxSocketName)
}

func (e *Engine) tmuxBin() (string, error) {
	if e.Tmux.Bin != "" {
		return e.Tmux.Bin, nil
	}
	p, err := exec.LookPath("tmux")
	if err != nil {
		return "", errors.New("宿主的 PATH 里没有 tmux")
	}
	return p, nil
}

// tmuxEnv 是运行宿主 tmux 时的环境：本进程的环境去掉 TMUX 与 TMUX_PANE
// （在别的 tmux 里运行管理器时，它们指向那一个 server），再加上 Tmux.Env。
func (e *Engine) tmuxEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, e.Tmux.Env...)
}

// tmuxArgs 是访问该槽 tmux server 的参数（不含可执行文件本身）。
func (e *Engine) tmuxArgs(id string, args ...string) []string {
	argv := []string{"-S", e.TmuxSocket(id)}
	if e.Tmux.Conf != "" {
		argv = append(argv, "-f", e.Tmux.Conf)
	}
	return append(argv, args...)
}

func (e *Engine) tmuxRun(ctx context.Context, id string, args ...string) (string, error) {
	bin, err := e.tmuxBin()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, e.tmuxArgs(id, args...)...)
	cmd.Env = e.tmuxEnv()
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", truncate(text, 300))
		}
		return text, err
	}
	return text, nil
}

// prepareTmuxDir 确保槽位的 socket 目录存在（只有本人可进入），并检查 socket 路径没有超长。
// 目录必须在创建 worker 之前就有：挂载源不存在时 Docker 会以 root 身份把它建出来。
func (e *Engine) prepareTmuxDir(id string) (string, error) {
	sock := e.TmuxSocket(id)
	if len(sock) > maxSocketPath {
		return "", fmt.Errorf("槽位 %s 的 tmux socket 路径有 %d 字节，超过了 Unix socket 的上限 %d：把数据目录放到更短的路径下才能开启桥接（%s）",
			id, len(sock), maxSocketPath, sock)
	}
	dir := e.st.TmuxDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// TmuxAlive 报告槽位的宿主 tmux server 是否在运行。
func (e *Engine) TmuxAlive(ctx context.Context, id string) bool {
	if _, err := os.Stat(e.TmuxSocket(id)); err != nil {
		return false
	}
	_, err := e.tmuxRun(ctx, id, "list-sessions", "-F", "#{session_name}")
	return err == nil
}

// TmuxVersion 返回宿主 tmux 的版本行，例如 "tmux 3.8"。
func (e *Engine) TmuxVersion(ctx context.Context) (string, error) {
	bin, err := e.tmuxBin()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-V").Output()
	if err != nil {
		return "", fmt.Errorf("运行 %s -V 失败：%w", bin, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// TmuxEnsure 确保槽位的宿主 tmux server 在运行；没有在运行时启动它，并建一个名为 main 的 session。
// dir 是这个 session 的工作目录，为空时取该槽的第一个挂载，再没有就用当前目录。
// 已经在运行的 server 不受影响。started 报告这次调用是否启动了 server。
func (e *Engine) TmuxEnsure(ctx context.Context, id, dir string) (started bool, err error) {
	sl, err := e.st.Slot(id)
	if err != nil {
		return false, err
	}
	if !sl.TmuxBridge {
		return false, fmt.Errorf("槽位 %s 没有开启宿主 tmux 桥接；用 cc-cluster tmux %s --on 开启", id, id)
	}
	if sl.UID != os.Getuid() {
		return false, fmt.Errorf("槽位 %s 在 worker 里的 UID 是 %d，当前用户是 %d：tmux 只接受同一个用户的连接，桥接要由登记该槽位的用户来启动", id, sl.UID, os.Getuid())
	}
	if _, err := e.prepareTmuxDir(id); err != nil {
		return false, err
	}
	if e.TmuxAlive(ctx, id) {
		return false, nil
	}
	if dir == "" {
		for _, m := range sl.Mounts {
			if fi, err := os.Stat(m.Source); err == nil && fi.IsDir() {
				dir = m.Source
				break
			}
		}
	}
	if dir == "" {
		if dir, err = os.Getwd(); err != nil {
			return false, err
		}
	}
	if _, err := e.tmuxRun(ctx, id, "new-session", "-d", "-s", TmuxSession, "-c", dir); err != nil {
		return false, fmt.Errorf("启动槽位 %s 的宿主 tmux 失败：%w", id, err)
	}
	e.record("tmux.start", id, nil, dir)
	return true, nil
}

// TmuxKill 结束槽位的宿主 tmux server，连同其中所有 pane 里的进程。server 没在运行时什么也不做。
func (e *Engine) TmuxKill(ctx context.Context, id string) (err error) {
	if _, err := e.st.Slot(id); err != nil {
		return err
	}
	if !e.TmuxAlive(ctx, id) {
		return nil
	}
	defer func() { e.record("tmux.kill", id, err, "") }()
	_, err = e.tmuxRun(ctx, id, "kill-server")
	return err
}

// TmuxAttach 返回在终端里接入槽位 tmux 的可执行文件、参数（含 argv[0]）和环境。
func (e *Engine) TmuxAttach(id string) (bin string, argv, env []string, err error) {
	if bin, err = e.tmuxBin(); err != nil {
		return "", nil, nil, err
	}
	return bin, append([]string{"tmux"}, e.tmuxArgs(id, "attach-session")...), e.tmuxEnv(), nil
}

// TmuxSettings 是桥接在槽位上的设置。
type TmuxSettings struct {
	On    bool
	Tools string // 为空表示不改
	Shell string // 为空表示不改；首次开启且没有设置过时取 HostShellType()
}

// SetTmuxBridge 修改槽位的桥接设置。只改期望配置：运行中的 worker 要重建后才生效。
func (e *Engine) SetTmuxBridge(id string, set TmuxSettings) (out model.Slot, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return out, err
	}
	defer unlock()
	defer func() { e.record("slot.tmux", id, err, fmt.Sprintf("on=%v", set.On)) }()

	sl, err := e.st.Slot(id)
	if err != nil {
		return out, err
	}
	sl.TmuxBridge = set.On
	if set.Tools != "" {
		sl.TmuxTools = set.Tools
	}
	if set.Shell != "" {
		sl.TmuxShell = set.Shell
	}
	if sl.TmuxBridge {
		if sl.TmuxShell == "" {
			sl.TmuxShell = HostShellType()
		}
		if _, err := e.prepareTmuxDir(id); err != nil {
			return out, err
		}
	}
	if err := sl.Validate(); err != nil {
		return out, err
	}
	return sl, e.st.SaveSlot(sl, false)
}

// HostShellType 按当前环境的 SHELL 给出 tmux-mcp 认识的 shell 类型；认不出时是 bash。
// tmux-mcp 靠它在命令前后加标记来判断一条命令何时结束、退出码是多少。
func HostShellType() string {
	switch t := filepath.Base(os.Getenv("SHELL")); t {
	case "zsh", "fish":
		return t
	}
	return "bash"
}

// bridgeFiles 是开启桥接时写进 worker 文件层的两个配置文件（路径相对容器根）。
// 它们属于容器内的 root，槽内的用户改不了。sock 是 tmux socket 的路径（宿主与 worker 里相同）。
func bridgeFiles(sl model.Slot, sock string) (map[string][]byte, error) {
	if !sl.TmuxBridge {
		return nil, nil
	}
	if strings.ContainsAny(sock, "\"\\\n") {
		return nil, fmt.Errorf("tmux socket 的路径 %q 里有引号、反斜杠或换行，不能开启桥接", sock)
	}
	mode, items, err := model.ParseTmuxTools(sl.EffectiveTmuxTools())
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = `"` + it + `"`
	}
	shell := sl.TmuxShell
	if shell == "" {
		shell = "bash"
	}
	policy := fmt.Sprintf(`# 由 cc-cluster 生成，重建 worker 时重新写入。

[shell]
type = "%s"

[security]
allowed_sockets = ["%s"]

[security.tools]
mode = "%s"
items = [%s]
`, shell, sock, mode, strings.Join(quoted, ", "))

	mcp, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"tmux": map[string]any{
				"command": "tmux-mcp-rs",
				"args":    []string{"--socket", sock, "--config", tmuxPolicy},
			},
		},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		strings.TrimPrefix(tmuxPolicy, "/"):    []byte(policy),
		strings.TrimPrefix(TmuxMCPConfig, "/"): append(mcp, '\n'),
	}, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "未知版本"
	}
	return s
}

// bridgeUp 在 worker 就绪后把桥接接通并核对两端。桥接的问题不影响槽位本身的网络，
// 所以这里只给出说明，不让 slot up 失败。返回值报告桥接是否可用。
func (e *Engine) bridgeUp(ctx context.Context, sl model.Slot, worker string) bool {
	started, err := e.TmuxEnsure(ctx, sl.ID, "")
	if err != nil {
		e.logf("宿主 tmux 桥接不可用：%v", err)
		return false
	}
	if started {
		e.logf("已启动槽位 %s 的宿主 tmux（cc-cluster tmux %s 可以接入观察）", sl.ID, sl.ID)
	}
	inWorker := func(cmd ...string) (string, bool) {
		res, err := e.dk.Exec(ctx, worker, cmd, dockerapi.ExecOptions{})
		if err != nil || res.ExitCode != 0 {
			return strings.TrimSpace(res.Stdout + res.Stderr), false
		}
		return strings.TrimSpace(res.Stdout), true
	}
	client, ok := inWorker("tmux", "-V")
	if !ok {
		e.logf("宿主 tmux 桥接不可用：该槽的 worker 镜像里没有 tmux")
		return false
	}
	if _, ok := inWorker("tmux-mcp-rs", "--version"); !ok {
		e.logf("宿主 tmux 桥接不可用：该槽的 worker 镜像里没有 tmux-mcp-rs")
		return false
	}
	if out, ok := inWorker("tmux", "-S", e.TmuxSocket(sl.ID), "list-sessions", "-F", "#{session_name}"); !ok {
		// tmux 的客户端与 server 要同一版本：版本不同时协议号可能相同，但照样连不上，报错也看不出原因。
		host, _ := e.TmuxVersion(ctx)
		hint := ""
		if host != "" && host != client {
			hint = "；两端版本不同，用 make image-worker 重建 worker 镜像（它会按宿主的版本编译 tmux）后再 slot up --recreate"
		}
		e.logf("宿主 tmux 桥接不可用：槽内连不上宿主的 tmux（%s）。宿主是 %s，worker 镜像里是 %s%s", truncate(out, 200), orUnknown(host), client, hint)
		return false
	}
	e.logf("宿主 tmux 桥接已接通：槽内经它执行的命令使用宿主的权限和宿主网络，不受该槽的网络约束")
	return true
}
