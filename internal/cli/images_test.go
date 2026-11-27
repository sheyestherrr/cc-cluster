package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func TestDefaultImagesFollowVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "dev"
	if g, w := defaultImages(); g != model.DefaultGatewayImage || w != model.DefaultWorkerImage {
		t.Fatalf("从源码构建的应当用本机构建的镜像: %s %s", g, w)
	}
	Version = "v0.0.2"
	if g, w := defaultImages(); g != "ghcr.io/sheyestherrr/cc-cluster-gateway:v0.0.2" || w != "ghcr.io/sheyestherrr/cc-cluster-worker:v0.0.2" {
		t.Fatalf("发布版应当用同版本的发布镜像: %s %s", g, w)
	}
	// git describe 给源码构建起的名字以 v 开头，但不是发布版：没有对应的发布镜像。
	Version = "v0.0.2-3-g768d650"
	if g, w := defaultImages(); g != model.DefaultGatewayImage || w != model.DefaultWorkerImage {
		t.Fatalf("标签之后的源码构建应当用本机构建的镜像: %s %s", g, w)
	}
}

func TestImagesCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	run := func(args ...string) (string, int) {
		var out, errOut strings.Builder
		code := Run(append([]string{"--data-dir", dir}, args...), strings.NewReader(""), &out, &errOut)
		return out.String() + errOut.String(), code
	}
	if out, code := run("init", "--name", "t", "--gateway-image", "gw:1", "--worker-image", "w:1"); code != 0 {
		t.Fatalf("init 失败: %s", out)
	}
	if out, code := run("images"); code != 0 || !strings.Contains(out, "gw:1") || strings.Contains(out, "仍在用原来的镜像") {
		t.Fatalf("不带参数应只显示现状: %d %s", code, out)
	}
	// 只给一个：另一个不变。
	if out, code := run("images", "--worker-image", "w:2"); code != 0 || !strings.Contains(out, "仍在用原来的镜像") {
		t.Fatalf("更换后应提示要重建: %d %s", code, out)
	}
	var c model.Cluster
	b, err := os.ReadFile(filepath.Join(dir, "cluster.json"))
	if err != nil || json.Unmarshal(b, &c) != nil {
		t.Fatal(err)
	}
	if c.GatewayImage != "gw:1" || c.WorkerImage != "w:2" || c.Name != "t" {
		t.Fatalf("写回的设置不对: %+v", c)
	}
	if out, code := run("images", "--gateway-image", "has space"); code == 0 {
		t.Fatalf("带空白的镜像名应被拒绝: %s", out)
	}
}
