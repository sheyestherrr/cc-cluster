package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	got, err := fetchUpdate(t, srv, h, query)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// fetchUpdate 不自己让测试失败，可以在别的 goroutine 里用。
func fetchUpdate(t *testing.T, srv *Server, h http.Handler, query string) (updateView, error) {
	code, _, raw := do(t, h, "GET", "/api/update"+query, "127.0.0.1:7878", srv.Token(), "")
	var out struct {
		Data updateView `json:"data"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(raw), &out) != nil {
		return updateView{}, fmt.Errorf("GET /api/update%s: %d %s", query, code, raw)
	}
	return out.Data, nil
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
	if got.Current != "v0.0.2" || !got.IsRelease || !got.AutoCheck || !got.Newer || got.Latest == nil || got.Latest.Version != "v0.0.4" || got.CheckedAt == nil || got.Error != "" {
		t.Fatalf("应查到新版本: %+v", got)
	}
	if len(got.Releases) != 2 || got.Releases[0].Version != "v0.0.4" || got.Releases[1].Version != "v0.0.3" {
		t.Fatalf("应列出要经过的两个版本: %+v", got.Releases)
	}
	if got.Releases[0].Notes != "第四版\n- 一条" || got.Releases[0].PublishedAt.Day() != 28 {
		t.Fatalf("更新说明应只留中文的那一段，并带发布时间: %+v", got.Releases[0])
	}
	// 给人点的链接由版本号拼出，不采用接口返回的地址。
	if got.Releases[0].URL != update.ReleasesURL+"/tag/v0.0.4" {
		t.Fatalf("发布页的地址不对: %s", got.Releases[0].URL)
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
	if got := getUpdate(t, srv, h, ""); got.AutoCheck || got.CheckedAt != nil || got.Latest != nil || got.Newer {
		t.Fatalf("关掉自动检查后，不点就不应去查: %+v", got)
	}
	if got := getUpdate(t, srv, h, "?refresh=1"); !got.Newer || got.Latest == nil {
		t.Fatalf("使用者点了才查: %+v", got)
	}
}

// 同时来的请求等同一次检查：都拿到结果，只问 GitHub 一次。
func TestUpdateInfoSharesOneCheck(t *testing.T) {
	srv, h, hub, _ := newUpdateServer(t, "v0.0.2", Options{})
	hub.Add("v0.0.3", 27, "第三版")
	hub.Gate = make(chan struct{})
	const n = 4
	type result struct {
		view updateView
		err  error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			v, err := fetchUpdate(t, srv, h, "")
			results <- result{v, err}
		}()
	}
	// 等第一个请求把检查发起来，再放行。
	for i := 0; i < 200 && hub.APICalls.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(hub.Gate)
	for i := 0; i < n; i++ {
		if got := <-results; got.err != nil || got.view.Latest == nil || !got.view.Newer || got.view.Error != "" {
			t.Fatalf("每个请求都应拿到这次检查的结果: %+v %v", got.view, got.err)
		}
	}
	if calls := hub.APICalls.Load(); calls != 1 {
		t.Fatalf("同时来的请求应共用一次检查，实际问了 %d 次", calls)
	}
}

// 发起检查的页面刷新或关掉了：检查照常做完并记下结果，不把“请求被取消”记成检查失败。
func TestUpdateInfoSurvivesCancelledRequest(t *testing.T) {
	srv, h, hub, _ := newUpdateServer(t, "v0.0.2", Options{})
	hub.Add("v0.0.3", 27, "第三版")
	hub.Gate = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/update", nil).WithContext(ctx)
	req.Host = "127.0.0.1:7878"
	req.Header.Set(TokenHeader, srv.Token())
	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() { h.ServeHTTP(rec, req); close(served) }()
	for i := 0; i < 200 && hub.APICalls.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-served
	var out struct {
		Data updateView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Data.Error != "" {
		t.Fatalf("请求被取消不应记成检查失败: %s %v", rec.Body.String(), err)
	}

	close(hub.Gate)
	got := getUpdate(t, srv, h, "") // 等的还是那一次检查
	if got.Latest == nil || !got.Newer || got.Error != "" || hub.APICalls.Load() != 1 {
		t.Fatalf("那次检查应照常做完并被之后的请求用上: %+v（问了 %d 次）", got, hub.APICalls.Load())
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

	// 二进制退回了 v0.0.1，镜像的设置是更新的 v0.0.2：不提示“更新镜像”。
	srv, h, hub, _ = newUpdateServer(t, "v0.0.1", Options{})
	hub.Add("v0.0.1", 24, "第一版")
	if got := getUpdate(t, srv, h, ""); got.ImagesFrom != "" || got.ImagesCommand != "" {
		t.Fatalf("镜像比二进制新时不应提醒: %+v", got)
	}
}

func TestSelfCommand(t *testing.T) {
	if got := SelfCommand(store.DefaultDir(), "update"); got != "cc-cluster update" {
		t.Errorf("默认的数据目录不用带: %s", got)
	}
	if got := SelfCommand(filepath.Join("/srv", "cc data"), "update --images"); !strings.HasPrefix(got, "cc-cluster --data-dir '/srv/cc data' update") {
		t.Errorf("带空格的路径应加引号: %s", got)
	}
	if got := CommandFor("/srv/a", "/srv/a/", "web"); got != "cc-cluster web" {
		t.Errorf("就是不带 --data-dir 时会用的那个目录时不用带: %s", got)
	}
	if got := CommandFor("/srv/a", "", "web"); got != "cc-cluster --data-dir /srv/a web" {
		t.Errorf("不知道默认目录时一定带上: %s", got)
	}
}
