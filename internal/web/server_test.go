package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

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
