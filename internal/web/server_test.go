package web

import (
	"encoding/json"
	"fmt"
	"io"
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
)

const testPort = 7878

func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	if _, err := store.Init(dir, model.Cluster{Name: "t"}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{DataDir: dir, Version: "test", Assets: fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>cc-cluster</title>")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler(testPort)
}

// do 发一个请求；token 为空时不带令牌。
func do(t *testing.T, h http.Handler, method, path, host, token, body string) (int, http.Header, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, rec.Result().Header, string(out)
}

func TestOnlyLocalHostNamesAreServed(t *testing.T) {
	srv, h := newTestServer(t)
	// 别的域名即使解析到 127.0.0.1（DNS 重绑定），带着正确的令牌也到不了接口。
	for _, host := range []string{"evil.example:7878", "127.0.0.1:9999", "127.0.0.1", ""} {
		if code, _, _ := do(t, h, "GET", "/api/state", host, srv.Token(), ""); code != http.StatusForbidden {
			t.Errorf("Host %q 应被拒绝，得到 %d", host, code)
		}
	}
	for _, host := range []string{"127.0.0.1:7878", "localhost:7878", "[::1]:7878"} {
		if code, _, body := do(t, h, "GET", "/api/state", host, srv.Token(), ""); code != http.StatusOK {
			t.Errorf("Host %q 应可访问，得到 %d %s", host, code, body)
		}
	}
}

func TestAPIRequiresToken(t *testing.T) {
	srv, h := newTestServer(t)
	for _, token := range []string{"", "wrong", srv.Token() + "x"} {
		code, _, body := do(t, h, "GET", "/api/state", "127.0.0.1:7878", token, "")
		if code != http.StatusUnauthorized || !strings.Contains(body, "访问令牌") {
			t.Errorf("令牌 %q 应被拒绝，得到 %d %s", token, code, body)
		}
	}
	// 页面本身不需要令牌：令牌在地址的 # 之后，浏览器不会发给服务器。
	code, hdr, body := do(t, h, "GET", "/", "127.0.0.1:7878", "", "")
	if code != http.StatusOK || !strings.Contains(body, "cc-cluster") {
		t.Errorf("首页应可访问: %d %s", code, body)
	}
	if csp := hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("页面应带内容安全策略: %q", csp)
	}
	if code, _, _ := do(t, h, "GET", "/api/nope", "127.0.0.1:7878", srv.Token(), ""); code != http.StatusNotFound {
		t.Errorf("不存在的接口应是 404，得到 %d", code)
	}
}

// 路径里的 ID 写法不对时直接拒绝，不交给引擎：转义过的斜杠（%2F）在路由之后已经还原成路径分隔符。
func TestInvalidPathIDIsRejected(t *testing.T) {
	srv, h := newTestServer(t)
	for _, path := range []string{
		"/api/slots/..%2F..%2Fx/release",
		"/api/slots/..%2F..%2Fx/connections",
		"/api/gateways/a%2F..%2Fb",
		"/api/nodes/A%20b",
	} {
		method := "POST"
		switch {
		case strings.HasSuffix(path, "/connections"):
			method = "GET"
		case !strings.HasSuffix(path, "/release"):
			method = "DELETE"
		}
		code, _, body := do(t, h, method, path, "127.0.0.1:7878", srv.Token(), "")
		if code != http.StatusBadRequest || !strings.Contains(body, "无效") {
			t.Errorf("%s %s 应以 400 拒绝，得到 %d %s", method, path, code, body)
		}
	}
}

// 操作记录很长时只取末尾的一段，仍然返回最近的记录、新的在前。
func TestOpsReadsOnlyTheTail(t *testing.T) {
	srv, h := newTestServer(t)
	st, err := store.Open(srv.opt.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	const total = 6000 // 每条约 280 字节，总长超过读取的那一段
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		op := model.Op{Time: base.Add(time.Duration(i) * time.Second), Op: "slot.up", Target: fmt.Sprintf("s%d", i), OK: true, Detail: strings.Repeat("x", 200)}
		if err := st.AppendOp(op); err != nil {
			t.Fatal(err)
		}
	}
	if fi, err := os.Stat(filepath.Join(srv.opt.DataDir, "ops.jsonl")); err != nil || fi.Size() <= opsTail {
		t.Fatalf("准备的操作记录应超过 %d 字节: %v", opsTail, err)
	}
	code, _, raw := do(t, h, "GET", "/api/ops", "127.0.0.1:7878", srv.Token(), "")
	var out struct {
		Data []model.Op `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); code != http.StatusOK || err != nil {
		t.Fatalf("读取操作记录失败: %d %v", code, err)
	}
	if len(out.Data) != 200 {
		t.Fatalf("应返回最近的 200 条，得到 %d 条", len(out.Data))
	}
	for i, op := range out.Data {
		if want := fmt.Sprintf("s%d", total-1-i); op.Target != want {
			t.Fatalf("第 %d 条应是 %s，得到 %s", i, want, op.Target)
		}
	}
}

func TestStateAndNodeLifecycle(t *testing.T) {
	srv, h := newTestServer(t)
	call := func(method, path, body string) (int, map[string]any, string) {
		code, _, raw := do(t, h, method, path, "127.0.0.1:7878", srv.Token(), body)
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("%s %s 的应答不是 JSON: %s", method, path, raw)
		}
		return code, out, raw
	}

	code, _, raw := call("POST", "/api/nodes", `{"id":"home","role":"landing","type":"socks5","server":"203.0.113.9","port":1080,"username":"u","password":"s3cret-pass","tz":"Asia/Tokyo"}`)
	if code != http.StatusOK {
		t.Fatalf("添加节点失败: %d %s", code, raw)
	}
	code, _, raw = call("POST", "/api/gateways", `{"id":"solo"}`)
	if code != http.StatusOK {
		t.Fatalf("添加网关失败: %d %s", code, raw)
	}

	code, out, raw := call("GET", "/api/state", "")
	if code != http.StatusOK {
		t.Fatalf("读取现状失败: %d %s", code, raw)
	}
	// 现状里不带任何凭据。
	if strings.Contains(raw, "s3cret-pass") || strings.Contains(raw, `"password"`) || strings.Contains(raw, `"proxy"`) {
		t.Errorf("现状里不应出现节点凭据: %s", raw)
	}
	data := out["data"].(map[string]any)
	nodes := data["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["server"] != "203.0.113.9" || nodes[0].(map[string]any)["tz"] != "Asia/Tokyo" {
		t.Errorf("节点列表不对: %v", nodes)
	}
	if gws := data["gateways"].([]any); len(gws) != 1 || gws[0].(map[string]any)["id"] != "solo" {
		t.Errorf("网关列表不对: %v", gws)
	}
	if data["version"] != "test" || data["landing_limit"] != float64(model.MaxSlotsPerLanding) {
		t.Errorf("版本或落地上限不对: %v", data)
	}

	// 备注：ID 里不允许的字符可以写在这里，现状里带出来；ID 不合法时报错里给出可用的写法。
	if code, _, raw := call("POST", "/api/nodes/home/note", `{"note":"韩国家宽_LG"}`); code != http.StatusOK {
		t.Fatalf("设置备注失败: %d %s", code, raw)
	}
	if code, _, raw := call("POST", "/api/gateways/solo/note", `{"note":"两行\n备注"}`); code != http.StatusBadRequest {
		t.Errorf("带换行的备注应被拒绝: %d %s", code, raw)
	}
	if _, out, _ := call("GET", "/api/state", ""); out["data"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["note"] != "韩国家宽_LG" {
		t.Errorf("现状里应带出节点的备注: %v", out)
	}
	if code, out, _ := call("POST", "/api/gateways", `{"id":"kr_home_lg"}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "kr-home-lg") {
		t.Errorf("不合法的 ID 应报错并给出可用的写法: %d %v", code, out)
	}

	// 出错时给出说明而不是 500。
	code, out, _ = call("POST", "/api/nodes", `{"id":"home","role":"landing","type":"socks5","server":"203.0.113.9","port":1080}`)
	if code != http.StatusBadRequest || out["error"] == nil {
		t.Errorf("重复添加应报错: %d %v", code, out)
	}
	if code, out, _ = call("POST", "/api/nodes", `{broken`); code != http.StatusBadRequest || out["error"] == nil {
		t.Errorf("不合法的请求内容应报错: %d %v", code, out)
	}
	if code, _, raw = call("DELETE", "/api/nodes/home", ""); code != http.StatusOK {
		t.Errorf("删除节点失败: %d %s", code, raw)
	}
	if code, out, _ = call("GET", "/api/ops", ""); code != http.StatusOK || len(out["data"].([]any)) == 0 {
		t.Errorf("操作记录应有内容: %d %v", code, out)
	}
}

func TestLocalSubscriptionAPI(t *testing.T) {
	srv, h := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		code, _, raw := do(t, h, method, path, "127.0.0.1:7878", srv.Token(), body)
		return code, raw
	}
	// 贴分享链接建一个本地订阅：不访问任何地址。
	code, raw := call("POST", "/api/subs", `{"id":"mine","note":"自己的","content":"trojan://s3cret-pass@203.0.113.2:443#🇭🇰 香港 01\nwireguard://x"}`)
	if code != http.StatusOK || !strings.Contains(raw, `"count":1`) || !strings.Contains(raw, `"skipped":1`) {
		t.Fatalf("添加本地订阅失败: %d %s", code, raw)
	}
	if code, raw = call("POST", "/api/nodes", `{"id":"hk","role":"landing","sub":"mine","name":"🇭🇰 香港 01"}`); code != http.StatusOK {
		t.Fatalf("从订阅登记节点失败: %d %s", code, raw)
	}
	// 订阅节点带出从名称认出的地区和已登记的节点；不带凭据。
	code, raw = call("GET", "/api/subs/mine/nodes", "")
	if code != http.StatusOK || !strings.Contains(raw, `"region":"HK"`) || !strings.Contains(raw, `"nodes":["hk"]`) || strings.Contains(raw, "s3cret-pass") {
		t.Errorf("订阅节点列表不对: %d %s", code, raw)
	}
	// 追加一条；现状里说明它是本地订阅，同样不带内容。
	if code, raw = call("POST", "/api/subs/mine/source", `{"content":"socks5://u:pw@203.0.113.9:1080#JP","append":true}`); code != http.StatusOK || !strings.Contains(raw, `"Total":2`) {
		t.Fatalf("追加失败: %d %s", code, raw)
	}
	code, raw = call("GET", "/api/state", "")
	if code != http.StatusOK || !strings.Contains(raw, `"local":true`) || !strings.Contains(raw, `"format":"links"`) || !strings.Contains(raw, `"region":"HK"`) {
		t.Errorf("现状里的订阅或节点地区不对: %d %s", code, raw)
	}
	if strings.Contains(raw, "s3cret-pass") || strings.Contains(raw, "trojan://") {
		t.Errorf("现状里不应出现订阅内容: %s", raw)
	}
	if code, raw = call("POST", "/api/subs/mine/source", `{"content":"https://sub.example.com/link?token=abc"}`); code != http.StatusBadRequest || !strings.Contains(raw, "订阅地址") {
		t.Errorf("贴网址应提示填到订阅地址里: %d %s", code, raw)
	}
}

func TestTrafficAndHistoryAPI(t *testing.T) {
	srv, h := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		code, _, raw := do(t, h, method, path, "127.0.0.1:7878", srv.Token(), body)
		return code, raw
	}
	// 没有采样过：实时的数是空的，接口照常应答。
	if code, raw := call("GET", "/api/traffic", ""); code != http.StatusOK || !strings.Contains(raw, `"slots":{}`) || !strings.Contains(raw, `"recording":false`) {
		t.Fatalf("实时流量: %d %s", code, raw)
	}
	if code, raw := call("GET", "/api/slots/nope/connections", ""); code != http.StatusBadRequest {
		t.Fatalf("不存在的槽位应报错: %d %s", code, raw)
	}

	st, err := store.Open(srv.opt.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.AppendMetrics([]model.Metric{
		{T: now.Add(-10 * time.Minute), Kind: model.MetricTraffic, Slot: "alice", Span: 300, Up: 1000, Down: 20000, Conns: 3},
		{T: now.Add(-9 * time.Minute), Kind: model.MetricWatch, Slot: "alice", Status: "ok", ExitIP: "198.51.100.20"},
		{T: now.Add(-8 * time.Minute), Kind: model.MetricSub, Sub: "air", Usable: 12, Total: 30},
		{T: now.Add(-40 * time.Hour), Kind: model.MetricTraffic, Slot: "alice", Span: 300, Up: 7, Down: 7},
	}); err != nil {
		t.Fatal(err)
	}
	code, raw := call("GET", "/api/history", "")
	var hist struct {
		Data struct {
			Step    int
			Traffic []struct{ Up, Down int64 }
			Totals  map[string]struct{ Up, Down int64 }
			Watch   map[string][]struct{ Status, ExitIP string }
			Subs    map[string][]struct{ Usable, Total int }
		}
	}
	if err := json.Unmarshal([]byte(raw), &hist); code != http.StatusOK || err != nil {
		t.Fatalf("历史: %d %v %s", code, err, raw)
	}
	d := hist.Data
	if d.Step != 300 || len(d.Traffic) < 288 || d.Totals["alice"].Up != 1000 || d.Totals["alice"].Down != 20000 || len(d.Watch["alice"]) != 1 || d.Subs["air"][0].Usable != 12 {
		t.Fatalf("默认看最近 24 小时，5 分钟一档: step=%d n=%d %+v %+v %+v", d.Step, len(d.Traffic), d.Totals, d.Watch, d.Subs)
	}
	// 看 7 天：一小时一档，40 小时前的那条也在内。
	code, raw = call("GET", "/api/history?hours=168", "")
	if err := json.Unmarshal([]byte(raw), &hist); code != http.StatusOK || err != nil || hist.Data.Step != 3600 || hist.Data.Totals["alice"].Up != 1007 {
		t.Fatalf("7 天的历史: %d %v step=%d %+v", code, err, hist.Data.Step, hist.Data.Totals)
	}

	// 连接历史要槽位存在。
	if code, raw := call("GET", "/api/slots/alice/connlog", ""); code != http.StatusBadRequest {
		t.Fatalf("不存在的槽位应报错: %d %s", code, raw)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/data/cc":       "/data/cc",
		"/data/my dir":   "'/data/my dir'",
		"/data/it's":     `'/data/it'\''s'`,
		"":               "''",
		"/data/$(touch)": "'/data/$(touch)'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q，期望 %q", in, got, want)
		}
	}
}
