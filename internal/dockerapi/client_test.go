package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDaemon 在临时目录的 Unix socket 上起一个假的 Docker 守护进程。
func fakeDaemon(t *testing.T, apiVersion string, h http.Handler) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Version{Version: "test", APIVersion: apiVersion, MinAPIVersion: "1.24"})
	})
	if h != nil {
		mux.Handle("/", h)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	c, err := New(context.Background(), "unix://"+sock)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func frame(stream byte, payload string) []byte {
	hdr := make([]byte, 8)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
	return append(hdr, payload...)
}

func TestVersionNegotiation(t *testing.T) {
	if c := fakeDaemon(t, "1.54", nil); c.APIVersion() != maxAPIVersion {
		t.Errorf("新守护进程应封顶到 %s，得到 %s", maxAPIVersion, c.APIVersion())
	}
	if c := fakeDaemon(t, "1.41", nil); c.APIVersion() != "1.41" {
		t.Errorf("旧守护进程应使用它自己的版本，得到 %s", c.APIVersion())
	}

	sock := filepath.Join(t.TempDir(), "old.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Version{APIVersion: "1.39"})
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	if _, err := New(context.Background(), "unix://"+sock); err == nil {
		t.Error("低于最低版本的守护进程应被拒绝")
	}
	if _, err := New(context.Background(), "tcp://127.0.0.1:2375"); err == nil {
		t.Error("非 unix:// 地址应被拒绝")
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.40", "1.44", true}, {"1.44", "1.40", false}, {"1.9", "1.10", true}, {"1.44", "1.44", false}, {"1.54", "2.0", true}}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%s,%s)=%v", c.a, c.b, got)
		}
	}
}

func TestErrorsAndNotModified(t *testing.T) {
	c := fakeDaemon(t, "1.44", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/gone/json"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container: gone"}`))
		case strings.HasSuffix(r.URL.Path, "/containers/up/start"):
			w.WriteHeader(http.StatusNotModified)
		case strings.HasSuffix(r.URL.Path, "/containers/gone/stop"), r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no such container"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("plain text failure"))
		}
	}))
	ctx := context.Background()

	_, err := c.ContainerInspect(ctx, "gone")
	if !IsNotFound(err) || !strings.Contains(err.Error(), "No such container") {
		t.Errorf("404 应映射为 IsNotFound 并带上守护进程的消息: %v", err)
	}
	if err := c.ContainerStart(ctx, "up"); err != nil {
		t.Errorf("启动已在运行的容器（304）应视为成功: %v", err)
	}
	if err := c.ContainerStop(ctx, "gone", 0); err != nil {
		t.Errorf("停止不存在的容器不应报错: %v", err)
	}
	if err := c.ContainerRemove(ctx, "gone"); err != nil {
		t.Errorf("删除不存在的容器不应报错: %v", err)
	}
	if _, err := c.Info(ctx); err == nil || IsNotFound(err) || !strings.Contains(err.Error(), "plain text failure") {
		t.Errorf("非 JSON 的错误体应原样带回: %v", err)
	}
}

func TestDemux(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(1, "out-1 "))
	in.Write(frame(2, "err-1"))
	in.Write(frame(1, "out-2"))
	var o, e bytes.Buffer
	if err := demux(&in, &o, &e); err != nil {
		t.Fatal(err)
	}
	if o.String() != "out-1 out-2" || e.String() != "err-1" {
		t.Fatalf("拆流结果不对: %q %q", o.String(), e.String())
	}
	if err := demux(bytes.NewReader(frame(1, "abcdef")[:10]), io.Discard, io.Discard); err == nil {
		t.Fatal("截断的帧应报错")
	}
	if err := demux(bytes.NewReader([]byte{1, 0, 0}), io.Discard, io.Discard); err == nil {
		t.Fatal("截断的帧头应报错")
	}
}

func TestExecCollectsOutputAndExitCode(t *testing.T) {
	var gotCreate map[string]any
	c := fakeDaemon(t, "1.44", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/gw/exec"):
			_ = json.NewDecoder(r.Body).Decode(&gotCreate)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"e1"}`))
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			_, _ = w.Write(frame(1, "hello\n"))
			_, _ = w.Write(frame(2, "warn\n"))
		case strings.HasSuffix(r.URL.Path, "/exec/e1/json"):
			_, _ = w.Write([]byte(`{"ExitCode":3,"Running":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	res, err := c.Exec(context.Background(), "gw", []string{"sh", "-c", "x"}, ExecOptions{User: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Stdout != "hello\n" || res.Stderr != "warn\n" {
		t.Fatalf("exec 结果不对: %+v", res)
	}
	if gotCreate["User"] != "0" || gotCreate["AttachStdout"] != true {
		t.Fatalf("exec 创建请求不对: %v", gotCreate)
	}
}

func TestCopyToSendsTar(t *testing.T) {
	got := map[string]string{}
	var mode int64
	var path string
	c := fakeDaemon(t, "1.44", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Query().Get("path")
		tr := tar.NewReader(r.Body)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(tr)
			got[h.Name] = string(b)
			mode = h.Mode
			if h.Uid != 0 || h.Gid != 0 {
				t.Errorf("文件属主应为容器内 root: %d:%d", h.Uid, h.Gid)
			}
		}
	}))
	err := c.CopyTo(context.Background(), "gw", "/var/lib/ccgw", map[string]File{"config.new.yaml": {Data: []byte("{}"), Mode: 0o600}})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/var/lib/ccgw" || got["config.new.yaml"] != "{}" || mode != 0o600 {
		t.Fatalf("上传内容不对: path=%s files=%v mode=%o", path, got, mode)
	}
}

func TestContainerListFiltersByLabel(t *testing.T) {
	var filters string
	c := fakeDaemon(t, "1.44", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filters = r.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/cc-w-a"],"State":"running"}]`))
	}))
	out, err := c.ContainerList(context.Background(), map[string]string{"cc-cluster.cluster": "cc"})
	if err != nil || len(out) != 1 || out[0].ID != "abc" {
		t.Fatalf("列表结果不对: %+v %v", out, err)
	}
	if !strings.Contains(filters, `"cc-cluster.cluster=cc"`) {
		t.Fatalf("应按标签过滤: %s", filters)
	}
}
