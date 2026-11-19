package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func TestBridgeFiles(t *testing.T) {
	const sock = "/d/slots/a/tmux/sock"
	if files, err := bridgeFiles(model.Slot{ID: "a"}, sock); err != nil || files != nil {
		t.Fatalf("没有开启桥接时不应生成文件：%v %v", files, err)
	}
	files, err := bridgeFiles(model.Slot{ID: "a", TmuxBridge: true, TmuxShell: "zsh"}, sock)
	if err != nil {
		t.Fatal(err)
	}
	policy := string(files["etc/tmux-mcp/config.toml"])
	for _, want := range []string{
		`type = "zsh"`,
		`allowed_sockets = ["/d/slots/a/tmux/sock"]`,
		`mode = "deny"`,
		`items = ["@kill"]`,
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("默认的工具范围里缺少 %s：\n%s", want, policy)
		}
	}
	mcp := string(files["etc/tmux-mcp/mcp.json"])
	for _, want := range []string{`"command": "tmux-mcp-rs"`, `"/d/slots/a/tmux/sock"`, `"/etc/tmux-mcp/config.toml"`} {
		if !strings.Contains(mcp, want) {
			t.Errorf("MCP 配置里缺少 %s：\n%s", want, mcp)
		}
	}

	files, err = bridgeFiles(model.Slot{ID: "a", TmuxBridge: true, TmuxTools: "allow:@read,execute-command"}, sock)
	if err != nil {
		t.Fatal(err)
	}
	policy = string(files["etc/tmux-mcp/config.toml"])
	if !strings.Contains(policy, `mode = "allow"`) || !strings.Contains(policy, `items = ["@read", "execute-command"]`) || !strings.Contains(policy, `type = "bash"`) {
		t.Errorf("指定的工具范围没有照写：\n%s", policy)
	}
	files, _ = bridgeFiles(model.Slot{ID: "a", TmuxBridge: true, TmuxTools: "deny:"}, sock)
	if !strings.Contains(string(files["etc/tmux-mcp/config.toml"]), "items = []") {
		t.Error("deny: 后面不带项应表示不限制")
	}
	// 路径原样写进 TOML 的字符串里，带引号的路径会破坏配置。
	if _, err := bridgeFiles(model.Slot{ID: "a", TmuxBridge: true}, `/d/x"y/sock`); err == nil {
		t.Error("带引号的 socket 路径应被拒绝")
	}
}

func TestParseTmuxTools(t *testing.T) {
	for _, bad := range []string{"@kill", "block:@kill", "allow:", "deny:@kill;rm", `deny:"x"`, "deny:Kill", "deny:a b"} {
		if _, _, err := model.ParseTmuxTools(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	mode, items, err := model.ParseTmuxTools("deny:@kill,send-keys")
	if err != nil || mode != "deny" || len(items) != 2 || items[1] != "send-keys" {
		t.Fatalf("解析结果不对：%s %v %v", mode, items, err)
	}
}

func TestWorkerSpecMountsTmuxDirectory(t *testing.T) {
	p := testParams()
	if strings.Contains(strings.Join(workerSpec(p).HostConfig.Binds, "\n"), "/tmux") {
		t.Fatal("没有开启桥接时不应出现 tmux 的挂载")
	}
	p.TmuxDir = "/d/slots/a/tmux"
	binds := strings.Join(workerSpec(p).HostConfig.Binds, "\n")
	// 必须挂目录：挂 socket 文件的话，宿主的 server 重建后 worker 里还是旧的那个。
	// 槽内路径必须与宿主相同：tmux-mcp 会让宿主的 pane 用同一个路径回连这个 socket。
	if !strings.Contains(binds, "/d/slots/a/tmux:/d/slots/a/tmux") || strings.Contains(binds, "tmux/sock:") {
		t.Fatalf("应把 socket 所在的目录以相同的路径挂进 worker：\n%s", binds)
	}
}

func TestTmuxSocketPathLimit(t *testing.T) {
	eng, _ := newTestEngine(t, nil, nil)
	if _, err := eng.prepareTmuxDir("a"); err != nil {
		t.Fatalf("正常长度的路径应可用：%v", err)
	}
	fi, err := os.Stat(eng.st.TmuxDir("a"))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket 目录应只有本人可进入：%v %v", fi, err)
	}
	long := strings.Repeat("x", 30)
	if len(eng.TmuxSocket(long)) <= maxSocketPath {
		// 测试用的临时目录本身很短时，再换一个更长的数据目录。
		t.Skip("临时目录太短，构造不出超长的 socket 路径")
	}
	if _, err := eng.prepareTmuxDir(long); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超长的 socket 路径应被拒绝并说明原因：%v", err)
	}
}

func TestSetTmuxBridge(t *testing.T) {
	eng, st := newTestEngine(t, nil, nil)
	sl := model.Slot{ID: "a", Gateway: "g", Landing: "l", Subnet: "198.18.0.0/30", Shell: "/bin/bash", UID: os.Getuid(), GID: os.Getgid()}
	if sl.UID == 0 {
		t.Skip("不以 root 运行")
	}
	if err := st.SaveSlot(sl, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/usr/bin/zsh")
	if _, err := eng.SetTmuxBridge("a", TmuxSettings{On: true, Tools: "deny:@kill;x"}); err == nil {
		t.Fatal("不合法的工具范围应被拒绝")
	}
	out, err := eng.SetTmuxBridge("a", TmuxSettings{On: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.TmuxBridge || out.TmuxShell != "zsh" || out.EffectiveTmuxTools() != model.DefaultTmuxTools {
		t.Fatalf("开启后的设置不对：%+v", out)
	}
	// shell 类型记下之后不随环境变化：它写进了 worker，变了就要重建。
	t.Setenv("SHELL", "/bin/bash")
	out, err = eng.SetTmuxBridge("a", TmuxSettings{On: true, Tools: "deny:"})
	if err != nil || out.TmuxShell != "zsh" || out.TmuxTools != "deny:" {
		t.Fatalf("再次设置不应改掉已记下的 shell：%+v %v", out, err)
	}
	out, err = eng.SetTmuxBridge("a", TmuxSettings{On: false})
	if err != nil || out.TmuxBridge {
		t.Fatalf("关闭失败：%+v %v", out, err)
	}
	if _, err := eng.TmuxEnsure(context.Background(), "a", ""); err == nil {
		t.Fatal("没有开启桥接时不应启动宿主 tmux")
	}
}

// 用真实的 tmux 走一遍启动、探活、结束。需要 PATH 里有 tmux；配置和 HOME 都指向临时目录。
func TestTmuxServerLifecycle(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("PATH 里没有 tmux")
	}
	eng, st := newTestEngine(t, nil, nil)
	if os.Getuid() == 0 {
		t.Skip("不以 root 运行")
	}
	home := t.TempDir()
	eng.Tmux = TmuxHost{Bin: bin, Conf: "/dev/null", Env: []string{"HOME=" + home, "SHELL=/bin/sh"}}
	proj := t.TempDir()
	sl := model.Slot{ID: "a", Gateway: "g", Landing: "l", Subnet: "198.18.0.0/30", Shell: "/bin/bash",
		UID: os.Getuid(), GID: os.Getgid(), TmuxBridge: true, Mounts: []model.Mount{{Source: proj, Target: proj}}}
	if err := st.SaveSlot(sl, true); err != nil {
		t.Fatal(err)
	}
	if len(eng.TmuxSocket("a")) > maxSocketPath {
		t.Skip("临时目录太长，放不下 socket")
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = eng.TmuxKill(ctx, "a") })

	if eng.TmuxAlive(ctx, "a") {
		t.Fatal("还没启动就报告在运行")
	}
	started, err := eng.TmuxEnsure(ctx, "a", "")
	if err != nil || !started {
		t.Fatalf("启动失败：%v %v", started, err)
	}
	if !eng.TmuxAlive(ctx, "a") {
		t.Fatal("启动后应在运行")
	}
	// 第一个 session 叫 main，工作目录是该槽的第一个挂载。
	out, err := eng.tmuxRun(ctx, "a", "list-panes", "-a", "-F", "#{session_name} #{pane_current_path}")
	want, _ := filepath.EvalSymlinks(proj)
	if err != nil || !strings.HasPrefix(out, TmuxSession+" ") || !strings.Contains(out, want) {
		t.Fatalf("session 或工作目录不对：%q %v（期望目录 %s）", out, err, want)
	}
	if started, err := eng.TmuxEnsure(ctx, "a", ""); err != nil || started {
		t.Fatalf("已在运行时不应再启动一次：%v %v", started, err)
	}
	if err := eng.TmuxKill(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if eng.TmuxAlive(ctx, "a") {
		t.Fatal("结束后不应还在运行")
	}
	// socket 重建：同一个目录下再次启动，路径不变。
	if started, err := eng.TmuxEnsure(ctx, "a", proj); err != nil || !started {
		t.Fatalf("结束后应能重新启动：%v %v", started, err)
	}
}
