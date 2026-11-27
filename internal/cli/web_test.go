package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

func TestListenWebSkipsBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	if _, err := listenWeb(port, true); err == nil {
		t.Fatal("指定的端口被占用时应报错，而不是换一个")
	}
	ln, err := listenWeb(port, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := ln.Addr().(*net.TCPAddr).Port; got <= port || got >= port+webPortTries {
		t.Fatalf("应顺延到后面的空闲端口: %d（起点 %d）", got, port)
	}
}

func TestRunningWeb(t *testing.T) {
	dir := t.TempDir()
	if _, ok := runningWeb(dir); ok {
		t.Fatal("没有记录时不应认为在运行")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	write := func(r webRun) {
		b, _ := json.Marshal(r)
		if err := os.WriteFile(webRunPath(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 父进程一定活着，拿它当“另一个正在运行的进程”。
	write(webRun{PID: os.Getppid(), Port: port, Token: "t"})
	if r, ok := runningWeb(dir); !ok || r.Port != port || r.Token != "t" {
		t.Fatalf("进程在、端口在监听时应认为在运行: %+v %v", r, ok)
	}
	// 自己的进程号不算：那是上一次没清掉、进程号又被重用的记录。
	write(webRun{PID: os.Getpid(), Port: port, Token: "t"})
	if _, ok := runningWeb(dir); ok {
		t.Fatal("记录的是当前进程时不应认为另有一个在运行")
	}
	ln.Close()
	write(webRun{PID: os.Getppid(), Port: port, Token: "t"})
	if _, ok := runningWeb(dir); ok {
		t.Fatal("端口已经没人监听时不应认为在运行")
	}
}

func TestWebTokenIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := webToken(dir)
	if err != nil || !tokenRe.MatchString(a) {
		t.Fatalf("应生成一个令牌: %q %v", a, err)
	}
	if fi, err := os.Stat(webTokenPath(dir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("令牌文件应当只有自己能读: %v", err)
	}
	if b, _ := webToken(dir); b != a {
		t.Fatalf("再取一次应当是同一个: %s %s", a, b)
	}
	// 文件被写坏了就换一个新的，而不是把坏内容当令牌用。
	if err := os.WriteFile(webTokenPath(dir), []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := webToken(dir); err != nil || c == a || !tokenRe.MatchString(c) {
		t.Fatalf("坏文件应被换成新令牌: %q %v", c, err)
	}
}

func TestInheritWebOptions(t *testing.T) {
	old := webRun{Port: 7900, Watch: "0s", NoUpdateCheck: true}
	// 没有另外指定：端口、持续检测的间隔和“不自动检查更新”都沿用原来的。
	got := inheritWebOptions(webOptions{Port: DefaultWebPort, Interval: engine.DefaultWatchInterval}, false, old)
	if got.Port != 7900 || got.Fixed || got.Interval != 0 || !got.NoUpdateCheck {
		t.Fatalf("应沿用原来的选项: %+v", got)
	}
	// 这次明确指定了的以这次的为准。
	got = inheritWebOptions(webOptions{Port: 8000, Fixed: true, Interval: time.Minute}, true, old)
	if got.Port != 8000 || !got.Fixed || got.Interval != time.Minute {
		t.Fatalf("明确指定的不应被覆盖: %+v", got)
	}
	// 旧版本留下的记录里没有间隔：用这次的（默认值）。
	got = inheritWebOptions(webOptions{Port: DefaultWebPort, Interval: engine.DefaultWatchInterval}, false, webRun{Port: 7878})
	if got.Interval != engine.DefaultWatchInterval || got.NoUpdateCheck {
		t.Fatalf("记录里没有的选项应保持这次的: %+v", got)
	}
}

// 运行记录里要有重新启动时沿用的选项，并分清是使用者在前台启动的，还是 webSpawn 放到后台的。
func TestWebServeRecordsHowItWasStarted(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "none.sock")) // 不去碰真的 Docker
	for _, child := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "data")
		if _, err := store.Init(dir, model.Cluster{Name: "t", GatewayImage: "g", WorkerImage: "w"}); err != nil {
			t.Fatal(err)
		}
		if child {
			t.Setenv(envWebChild, "1")
		}
		a := &app{dataDir: dir, out: &bytes.Buffer{}, errw: &bytes.Buffer{}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- a.webServe(ctx, webOptions{Port: 0, NoUpdateCheck: true}) }()
		var r webRun
		for i := 0; i < 100 && r.PID == 0; i++ {
			if b, err := os.ReadFile(webRunPath(dir)); err == nil {
				_ = json.Unmarshal(b, &r)
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if r.PID != os.Getpid() || r.Watch != "0s" || !r.NoUpdateCheck || r.Foreground == child {
			t.Fatalf("运行记录不对（由 webSpawn 启动=%v）: %+v", child, r)
		}
		if _, err := os.Stat(webRunPath(dir)); err == nil {
			t.Fatal("退出时应删掉运行记录")
		}
		if os.Getenv(envWebChild) != "" {
			t.Fatal("后台进程的标记不应留在环境里传给它启动的进程")
		}
	}
}

func TestDefaultClusterName(t *testing.T) {
	name := defaultClusterName()
	if name != model.DefaultClusterName && !strings.HasPrefix(name, model.DefaultClusterName+"-") {
		t.Fatalf("前缀应是 cc 或 cc-<用户名>: %q", name)
	}
	if err := model.ValidateID("集群", name); err != nil {
		t.Fatalf("默认前缀应当合法: %v", err)
	}
}
