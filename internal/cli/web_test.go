package cli

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
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

func TestDefaultClusterName(t *testing.T) {
	name := defaultClusterName()
	if name != model.DefaultClusterName && !strings.HasPrefix(name, model.DefaultClusterName+"-") {
		t.Fatalf("前缀应是 cc 或 cc-<用户名>: %q", name)
	}
	if err := model.ValidateID("集群", name); err != nil {
		t.Fatalf("默认前缀应当合法: %v", err)
	}
}
