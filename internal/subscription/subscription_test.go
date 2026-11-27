package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sample = `
port: 7890
proxies:
  - {name: "HK 01", type: ss, server: hk1.example.com, port: 8388, cipher: aes-256-gcm, password: "p1", udp: true}
  - name: "JP 02"
    type: vless
    server: 203.0.113.9
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    tls: true
    dialer-proxy: other
    reality-opts:
      public-key: abc
      short-id: "01"
    alpn: [h2, http/1.1]
  - {name: "HK 01", type: ss, server: dup.example.com, port: 1, cipher: x, password: y}
  - {name: "broken", type: ss, port: 8388}
  - {name: "", type: ss, server: a.example.com, port: 1}
proxy-groups:
  - {name: auto, type: select, proxies: ["HK 01"]}
`

func TestParse(t *testing.T) {
	got, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "HK 01" || got[1].Name != "JP 02" {
		t.Fatalf("应只留下两个可用且不重名的节点: %+v", got)
	}
	hk, jp := got[0].Proxy, got[1].Proxy
	if hk["server"] != "hk1.example.com" || hk["password"] != "p1" {
		t.Fatalf("片段内容不对: %v", hk)
	}
	for _, k := range droppedKeys {
		if _, ok := hk[k]; ok {
			t.Errorf("%s 由管理器控制，不应带进来", k)
		}
		if _, ok := jp[k]; ok {
			t.Errorf("%s 由管理器控制，不应带进来", k)
		}
	}
	// 嵌套的映射和列表要能原样存成 JSON。
	b, err := json.Marshal(jp)
	if err != nil {
		t.Fatalf("片段应能存成 JSON: %v", err)
	}
	for _, want := range []string{`"reality-opts":{"public-key":"abc","short-id":"01"}`, `"alpn":["h2","http/1.1"]`, `"port":443`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON 里缺少 %s:\n%s", want, b)
		}
	}
}

func TestIsInfoName(t *testing.T) {
	for name, want := range map[string]bool{
		"剩余流量：267.59 GB":  true,
		"距离下次重置剩余：4 天":    true,
		"套餐到期：2026-11-23": true,
		"Traffic: 10 GB":  true,
		"🇺🇸美·凤凰城|专线-BP2":  false,
		"悦 · 🇯🇵 日本专属2💎":   false,
	} {
		if IsInfoName(name) != want {
			t.Errorf("IsInfoName(%q) 应为 %v", name, want)
		}
	}
}

func TestParseRejectsNonClash(t *testing.T) {
	for name, body := range map[string]string{
		"base64 节点列表": "c3M6Ly9hYmM=",
		"没有 proxies":  "port: 7890\n",
		"空列表":         "proxies: []\n",
		"不是 YAML":     "proxies: [\n",
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s：应报错", name)
		}
	}
}

// 默认的客户端跟随重定向，但不从 HTTPS 降到 HTTP：订阅内容里有节点的账号密码。
func TestFetchRefusesRedirectToPlainHTTP(t *testing.T) {
	served := false
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		_, _ = w.Write([]byte(sample))
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/down":
			http.Redirect(w, r, plain.URL+"/ok", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/moved":
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			_, _ = w.Write([]byte(sample))
		}
	}))
	defer secure.Close()
	// 沿用默认客户端的重定向规则，只把传输换成信任测试证书的那个。
	client := &http.Client{Transport: secure.Client().Transport, CheckRedirect: defaultClient.CheckRedirect}

	if body, err := Fetch(context.Background(), client, secure.URL+"/moved?token=SECRET"); err != nil || string(body) != sample {
		t.Fatalf("HTTPS 之间的重定向应照常跟随: %v", err)
	}
	_, err := Fetch(context.Background(), client, secure.URL+"/down?token=SECRET")
	if !errors.Is(err, errDowngrade) {
		t.Fatalf("降到 HTTP 的重定向应被拒绝: %v", err)
	}
	if served {
		t.Fatal("不应向 HTTP 地址发出请求")
	}
	_, err = Fetch(context.Background(), client, secure.URL+"/loop?token=SECRET")
	if !errors.Is(err, errTooManyRedirects) {
		t.Fatalf("无休止的重定向应被拒绝: %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), secure.URL) {
		t.Errorf("错误信息里不应带订阅地址: %v", err)
	}
	// 地址本身是 HTTP 的照常访问。
	if _, err := Fetch(context.Background(), &http.Client{CheckRedirect: defaultClient.CheckRedirect}, plain.URL+"/ok"); err != nil {
		t.Fatalf("HTTP 的订阅地址应照常访问: %v", err)
	}
}

func TestFetch(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(sample))
		case "/big":
			_, _ = w.Write(make([]byte, MaxBytes+10))
		default:
			http.Error(w, "no", http.StatusForbidden)
		}
	}))
	defer srv.Close()

	body, err := Fetch(context.Background(), srv.Client(), srv.URL+"/ok?token=SECRET")
	if err != nil || string(body) != sample {
		t.Fatalf("拉取失败: %v", err)
	}
	if ua != UserAgent {
		t.Fatalf("User-Agent 应为 %s，得到 %s", UserAgent, ua)
	}
	for _, path := range []string{"/big", "/denied?token=SECRET"} {
		_, err := Fetch(context.Background(), srv.Client(), srv.URL+path)
		if err == nil {
			t.Errorf("%s 应报错", path)
		} else if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), srv.URL) {
			t.Errorf("错误信息里不应带订阅地址: %v", err)
		}
	}
	for _, bad := range []string{"", "ftp://x/y", "not a url", "file:///etc/passwd"} {
		if _, err := Fetch(context.Background(), srv.Client(), bad); err == nil {
			t.Errorf("地址 %q 应被拒绝", bad)
		}
	}
}
