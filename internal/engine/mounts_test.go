package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// mountFixture 准备一组目录：主目录、带假 Docker socket 的运行目录、一个普通的项目目录。
func mountFixture(t *testing.T) (eng *Engine, home, run, project string) {
	t.Helper()
	eng, _ = newTestEngine(t, nil, nil)
	// macOS 上临时目录本身经过符号链接，先取实际路径，下面比较时才对得上。
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, run, project = filepath.Join(root, "home"), filepath.Join(root, "run"), filepath.Join(root, "project")
	for _, d := range []string{home, run, project} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sock := filepath.Join(run, "docker.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	old := dockerSockets
	t.Cleanup(func() { dockerSockets = old })
	dockerSockets = func() []string { return []string{sock} }
	return eng, home, run, project
}

func symlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestCheckMountSourceFollowsSymlinks(t *testing.T) {
	eng, home, run, project := mountFixture(t)
	links := filepath.Join(filepath.Dir(project), "links")
	if err := os.Mkdir(links, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(home, "work")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	other, sibling := filepath.Join(run, "other"), run+"ner"
	for _, d := range []string{other, sibling} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ok := []struct{ what, path string }{
		{"普通目录", project},
		{"主目录下的项目目录", sub},
		{"指向项目目录的符号链接", symlink(t, project, filepath.Join(links, "to-project"))},
		{"指向主目录下项目的链接", symlink(t, sub, filepath.Join(links, "to-sub"))},
		{"运行目录下的其他文件夹", other},
		{"与运行目录同前缀的目录", sibling},
	}
	for _, c := range ok {
		if err := eng.checkMountSource(c.path); err != nil {
			t.Errorf("%s 应被接受: %v", c.what, err)
		}
	}

	bad := []struct{ what, path, want string }{
		{"主目录", home, "整体挂载"},
		{"数据目录", eng.st.Dir(), "整体挂载"},
		{"Docker socket", filepath.Join(run, "docker.sock"), "Docker socket"},
		{"含 socket 的目录", run, "Docker socket"},
		{"指向主目录的符号链接", symlink(t, home, filepath.Join(links, "to-home")), "符号链接"},
		{"指向数据目录的符号链接", symlink(t, eng.st.Dir(), filepath.Join(links, "to-data")), "符号链接"},
		{"指向主目录上级的符号链接", symlink(t, filepath.Dir(home), filepath.Join(links, "to-root")), "符号链接"},
		{"指向 socket 目录的链接", symlink(t, run, filepath.Join(links, "to-run")), "Docker socket"},
		{"指向 socket 的链接", symlink(t, filepath.Join(run, "docker.sock"), filepath.Join(links, "engine.sock")), "Docker socket"},
		{"经链接的目录再往下", filepath.Join(symlink(t, filepath.Dir(home), filepath.Join(links, "up")), "home"), "符号链接"},
		{"不存在的路径", filepath.Join(project, "missing"), "不可用"},
		{"断开的符号链接", symlink(t, filepath.Join(project, "gone"), filepath.Join(links, "broken")), "不可用"},
	}
	for _, c := range bad {
		err := eng.checkMountSource(c.path)
		if err == nil {
			t.Errorf("%s 应被拒绝", c.what)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s 的报错应包含 %q: %v", c.what, c.want, err)
		}
	}
}

// 数据目录或主目录本身经过符号链接时，按实际路径给出的挂载源同样要拦住。
func TestCheckMountSourceResolvesProtectedPaths(t *testing.T) {
	eng, home, _, project := mountFixture(t)
	alias := symlink(t, home, filepath.Join(filepath.Dir(project), "home-alias"))
	t.Setenv("HOME", alias)
	if err := eng.checkMountSource(home); err == nil {
		t.Error("主目录的实际路径应被拒绝")
	}
	if err := eng.checkMountSource(alias); err == nil {
		t.Error("主目录的链接路径应被拒绝")
	}
}

// 登记槽位与追加挂载都按实际指向的位置检查。
func TestAddSlotAndWorkspaceRejectSymlinkedHome(t *testing.T) {
	eng, home, _, project := mountFixture(t)
	ctx := context.Background()
	proxy := map[string]any{"type": "socks5", "server": "203.0.113.9", "port": 1080}
	if err := eng.AddNode(ctx, model.Node{ID: "land", Role: model.RoleLanding, Proxy: proxy}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AddGateway(model.Gateway{ID: "solo"}); err != nil {
		t.Fatal(err)
	}
	link := symlink(t, home, filepath.Join(filepath.Dir(project), "innocent"))
	slot := model.Slot{ID: "s", Gateway: "solo", Landing: "land", UID: 1000, GID: 1000}

	withLink := slot
	withLink.Mounts = []model.Mount{{Source: link, Target: "/work"}}
	if _, _, err := eng.AddSlot(ctx, withLink, SlotOptions{}); err == nil {
		t.Fatal("挂载源指向主目录时不应登记成功")
	}
	slot.Mounts = []model.Mount{{Source: project, Target: project}}
	if _, _, err := eng.AddSlot(ctx, slot, SlotOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := eng.SlotAddWorkspace("s", link); err == nil {
		t.Fatal("追加指向主目录的挂载应被拒绝")
	}
	sl, err := eng.st.Slot("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(sl.Mounts) != 1 || sl.Mounts[0].Source != project {
		t.Fatalf("被拒绝的挂载不应写入槽位: %+v", sl.Mounts)
	}
}
