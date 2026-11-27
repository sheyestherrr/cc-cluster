package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
	"github.com/sheyestherrr/cc-cluster/internal/update"
	"github.com/sheyestherrr/cc-cluster/internal/update/updatetest"
)

// newUpdateServer 起一个版本是 current 的网页服务，查版本时访问的是假的发布页。
func newUpdateServer(t *testing.T, current string, opt Options) (*Server, http.Handler, *updatetest.Hub, string) {
	t.Helper()
	hub := updatetest.New(t, update.Repo)
	dir := t.TempDir()
	gw, wk := update.Images("v0.0.2")
	if _, err := store.Init(dir, model.Cluster{Name: "t", GatewayImage: gw, WorkerImage: wk}); err != nil {
		t.Fatal(err)
	}
	cl := update.NewClient(current)
	cl.API, cl.Site = hub.URL, hub.URL
	opt.DataDir, opt.Version, opt.UpdateClient = dir, current, cl
	opt.Assets = fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	srv, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler(testPort), hub, dir
}

func getUpdate(t *testing.T, srv *Server, h http.Handler, query string) updateView {
	t.Helper()
	code, _, raw := do(t, h, "GET", "/api/update"+query, "127.0.0.1:7878", srv.Token(), "")
	var out struct {
		Data updateView `json:"data"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatalf("GET /api/update%s: %d %s", query, code, raw)
	}
	return out.Data
}

func TestUpdateInfo(t *testing.T) {
	srv, h, hub, dir := newUpdateServer(t, "v0.0.2", Options{})
	hub.Add("v0.0.4", 28, "第四版\n- 一条\n\n---\n\nFourth")
	hub.Add("v0.0.3", 27, "第三版")
	hub.Add("v0.0.2", 25, "第二版")

	if code, _, _ := do(t, h, "GET", "/api/update", "127.0.0.1:7878", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("没有令牌不应能查: %d", code)
	}
	got := getUpdate(t, srv, h, "")
	if got.Current != "v0.0.2" || !got.IsRelease || !got.Newer || got.Latest == nil || got.Latest.Version != "v0.0.4" || got.CheckedAt == nil || got.Error != "" {
		t.Fatalf("应查到新版本: %+v", got)
	}
	if len(got.Releases) != 2 || got.Releases[0].Version != "v0.0.4" || got.Releases[1].Version != "v0.0.3" {
		t.Fatalf("应列出要经过的两个版本: %+v", got.Releases)
	}
	if got.Releases[0].Notes != "第四版\n- 一条" || got.Releases[0].PublishedAt.Day() != 28 {
		t.Fatalf("更新说明应只留中文的那一段，并带发布时间: %+v", got.Releases[0])
	}
	// 数据目录不是默认的那个：给出的命令带着它。
	if got.Command != "cc-cluster --data-dir "+dir+" update" || got.ReleasesURL != update.ReleasesURL {
		t.Fatalf("更新命令不对: %q", got.Command)
	}
	// 镜像的设置已经落后于这个二进制时才提；这里两者是同一个版本。
	if got.ImagesFrom != "" || got.ImagesCommand != "" {
		t.Fatalf("镜像与二进制同版本时不应提醒: %+v", got)
	}

	// 结果记在内存里：发布页上再有新版本，不到时候不会再去查，除非使用者要求。
	hub.Add("v0.0.5", 29, "第五版")
	if got := getUpdate(t, srv, h, ""); got.Latest.Version != "v0.0.4" {
		t.Fatalf("没过期时应当用记着的结果: %+v", got.Latest)
	}
	// 刚查过又点“重新检查”：也不去查，免得把匿名接口的额度用光。
	if got := getUpdate(t, srv, h, "?refresh=1"); got.Latest.Version != "v0.0.4" {
		t.Fatalf("一分钟内的重新检查应当用记着的结果: %+v", got.Latest)
	}
	srv.upd.checked = time.Now().Add(-2 * updateMinGap)
	if got := getUpdate(t, srv, h, "?refresh=1"); got.Latest.Version != "v0.0.5" || len(got.Releases) != 3 {
		t.Fatalf("重新检查应查到最新的: %+v", got)
	}
	// 查版本不往数据目录里写东西。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "cluster.json" && e.Name() != "lock" {
			t.Errorf("查版本不应在数据目录里留下文件: %s", e.Name())
		}
	}
}

func TestUpdateInfoKeepsLastResultOnFailure(t *testing.T) {
	srv, h, hub, _ := newUpdateServer(t, "v0.0.2", Options{})
	hub.Add("v0.0.3", 27, "第三版")
	if got := getUpdate(t, srv, h, ""); !got.Newer {
		t.Fatalf("应查到新版本: %+v", got)
	}
	hub.Close()
	srv.upd.checked = time.Now().Add(-2 * updateFresh)
	got := getUpdate(t, srv, h, "")
	if got.Error == "" || !got.Newer || got.Latest == nil || got.Latest.Version != "v0.0.3" {
		t.Fatalf("查不成时应说明原因，并保留上次查到的: %+v", got)
	}
	// 刚失败过：不会每次打开页面都再去连。
	before := srv.upd.checked
	getUpdate(t, srv, h, "")
	if !srv.upd.checked.Equal(before) {
		t.Fatal("失败之后短时间内不应重试")
	}
}

func TestUpdateInfoRespectsNoCheck(t *testing.T) {
	srv, h, hub, _ := newUpdateServer(t, "v0.0.2", Options{NoUpdateCheck: true})
	hub.Add("v0.0.3", 27, "第三版")
	if got := getUpdate(t, srv, h, ""); got.CheckedAt != nil || got.Latest != nil || got.Newer {
		t.Fatalf("关掉自动检查后，不点就不应去查: %+v", got)
	}
	if got := getUpdate(t, srv, h, "?refresh=1"); !got.Newer || got.Latest == nil {
		t.Fatalf("使用者点了才查: %+v", got)
	}
}

func TestUpdateInfoForSourceBuildAndStaleImages(t *testing.T) {
	// 从源码构建的：能看到最新发布的是什么，但不算“有新版本”。
	srv, h, hub, _ := newUpdateServer(t, "v0.0.2-3-g768d650", Options{})
	hub.Add("v0.0.3", 27, "第三版")
	if got := getUpdate(t, srv, h, ""); got.IsRelease || got.Newer || got.Latest == nil || got.ImagesFrom != "" {
		t.Fatalf("源码构建: %+v", got)
	}

	// 二进制已经是 v0.0.3，镜像的设置还是 v0.0.2 的：给出换镜像的命令。
	srv, h, hub, dir := newUpdateServer(t, "v0.0.3", Options{})
	hub.Add("v0.0.3", 27, "第三版")
	got := getUpdate(t, srv, h, "")
	if got.Newer || got.ImagesFrom != "v0.0.2" || got.ImagesCommand != "cc-cluster --data-dir "+dir+" update --images" {
		t.Fatalf("镜像落后时应提醒: %+v", got)
	}
	if len(got.Releases) != 1 || got.Releases[0].Notes != "第三版" {
		t.Fatalf("已是最新时给现在这个版本的说明: %+v", got.Releases)
	}
}

func TestSelfCommand(t *testing.T) {
	if got := SelfCommand(store.DefaultDir(), "update"); got != "cc-cluster update" {
		t.Errorf("默认的数据目录不用带: %s", got)
	}
	if got := SelfCommand(filepath.Join("/srv", "cc data"), "update --images"); !strings.HasPrefix(got, "cc-cluster --data-dir '/srv/cc data' update") {
		t.Errorf("带空格的路径应加引号: %s", got)
	}
}
