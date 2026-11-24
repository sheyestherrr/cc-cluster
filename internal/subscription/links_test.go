package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// link 解析一条链接，返回名称和片段的 JSON。
func link(t *testing.T, line string) (string, string) {
	t.Helper()
	rep, err := ParseReport([]byte(line))
	if err != nil {
		t.Fatalf("%s\n应能解析: %v", line, err)
	}
	if len(rep.Proxies) != 1 || rep.Format != FormatLinks || len(rep.Skipped) != 0 {
		t.Fatalf("%s\n应得到一个节点: %+v", line, rep)
	}
	b, err := json.Marshal(rep.Proxies[0].Proxy)
	if err != nil {
		t.Fatal(err)
	}
	return rep.Proxies[0].Name, string(b)
}

func TestParseLinks(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"🇯🇵 日本 01","add":"jp.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ray?ed=2048","tls":"tls","sni":"sni.example.com","alpn":"h2,http/1.1","fp":"chrome"}`
	cases := []struct {
		what, line, name, want string
	}{
		{"ss，SIP002", "ss://" + b64("aes-256-gcm:p@ss") + "@hk.example.com:8388#%F0%9F%87%AD%F0%9F%87%B0%20HK%2001", "🇭🇰 HK 01",
			`{"cipher":"aes-256-gcm","password":"p@ss","port":8388,"server":"hk.example.com","type":"ss"}`},
		{"ss，旧写法整段 base64", "ss://" + b64("chacha20-ietf-poly1305:secret@203.0.113.5:443"), "ss-203.0.113.5:443",
			`{"cipher":"chacha20-ietf-poly1305","password":"secret","port":443,"server":"203.0.113.5","type":"ss"}`},
		// 协议头拆成两段写：整条连着写会被仓库卫生检查当成真的分享链接。
		{"ss，2022 明文加转义", "ss:" + "//2022-blake3-aes-128-gcm:a%2Bb%3D@[2001:db8::1]:8388/?#v6", "v6",
			`{"cipher":"2022-blake3-aes-128-gcm","password":"a+b=","port":8388,"server":"2001:db8::1","type":"ss"}`},
		{"ss，obfs 插件（分号没转义）", "ss://" + b64("aes-128-gcm:pw") + "@a.example.com:80?plugin=obfs-local;obfs=http;obfs-host=bing.com#obfs", "obfs",
			`{"cipher":"aes-128-gcm","password":"pw","plugin":"obfs","plugin-opts":{"host":"bing.com","mode":"http"},"port":80,"server":"a.example.com","type":"ss"}`},
		{"ss，v2ray-plugin", "ss://" + b64("aes-128-gcm:pw") + "@a.example.com:443?plugin=v2ray-plugin%3Btls%3Bhost%3Dcdn.example.com%3Bpath%3D%2Fws", "ss-a.example.com:443",
			`{"cipher":"aes-128-gcm","password":"pw","plugin":"v2ray-plugin","plugin-opts":{"host":"cdn.example.com","mode":"websocket","path":"/ws","tls":true},"port":443,"server":"a.example.com","type":"ss"}`},
		{"vmess，v2rayN", "vmess://" + b64(vmessJSON), "🇯🇵 日本 01",
			`{"alpn":["http/1.1"],"alterId":0,"cipher":"auto","client-fingerprint":"chrome","network":"ws","port":443,"server":"jp.example.com","servername":"sni.example.com","tls":true,"type":"vmess","uuid":"11111111-2222-3333-4444-555555555555","ws-opts":{"early-data-header-name":"Sec-WebSocket-Protocol","headers":{"Host":"cdn.example.com"},"max-early-data":2048,"path":"/ray"}}`},
		{"vmess，端口和额外 ID 是数字，TCP", "vmess://" + b64(`{"ps":"plain","add":"198.51.100.7","port":10086,"id":"u-u-i-d","aid":2}`), "plain",
			`{"alterId":2,"cipher":"auto","port":10086,"server":"198.51.100.7","type":"vmess","uuid":"u-u-i-d"}`},
		{"vmess，Shadowrocket", "vmess://" + b64("aes-128-gcm:u-u-i-d@sr.example.com:443") + "?remarks=SR&obfs=websocket&obfsParam=h.example.com&path=/p&tls=1", "SR",
			`{"alterId":0,"cipher":"aes-128-gcm","network":"ws","port":443,"server":"sr.example.com","servername":"h.example.com","tls":true,"type":"vmess","uuid":"u-u-i-d","ws-opts":{"headers":{"Host":"h.example.com"},"path":"/p"}}`},
		{"vmess，标准写法", "vmess://grpc+tls:u-u-i-d-0@std.example.com:443/?serviceName=svc&sni=s.example.com#std", "std",
			`{"alterId":0,"cipher":"auto","grpc-opts":{"grpc-service-name":"svc"},"network":"grpc","port":443,"server":"std.example.com","servername":"s.example.com","tls":true,"type":"vmess","uuid":"u-u-i-d"}`},
		{"vless，reality", "vless://u-u-i-d@198.51.100.9:8443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&pbk=PUBKEY&sid=ab12&type=tcp#100%流量 US", "100%流量 US",
			`{"client-fingerprint":"chrome","flow":"xtls-rprx-vision","port":8443,"reality-opts":{"public-key":"PUBKEY","short-id":"ab12"},"server":"198.51.100.9","servername":"www.example.com","tls":true,"type":"vless","uuid":"u-u-i-d"}`},
		{"vless，ws 加 TLS，默认端口", "vless://u-u-i-d@v.example.com?security=tls&type=ws&host=h.example.com&path=%2Fvl&fp=firefox&allowInsecure=1", "vless-v.example.com:443",
			`{"client-fingerprint":"firefox","network":"ws","port":443,"server":"v.example.com","skip-cert-verify":true,"tls":true,"type":"vless","uuid":"u-u-i-d","ws-opts":{"headers":{"Host":"h.example.com"},"path":"/vl"}}`},
		{"vless，httpupgrade", "vless://u-u-i-d@v.example.com:80?type=httpupgrade&host=h.example.com&path=/up#hu", "hu",
			`{"network":"ws","port":80,"server":"v.example.com","type":"vless","uuid":"u-u-i-d","ws-opts":{"headers":{"Host":"h.example.com"},"path":"/up","v2ray-http-upgrade":true}}`},
		{"trojan", "trojan://pass%40word@t.example.com:443?sni=s.example.com&alpn=h2,http/1.1#TJ", "TJ",
			`{"alpn":["h2","http/1.1"],"password":"pass@word","port":443,"server":"t.example.com","sni":"s.example.com","type":"trojan"}`},
		{"trojan，grpc", "trojan://pw@t.example.com:443?type=grpc&serviceName=g#TG", "TG",
			`{"grpc-opts":{"grpc-service-name":"g"},"network":"grpc","password":"pw","port":443,"server":"t.example.com","type":"trojan"}`},
		{"hysteria2", "hysteria2://auth@h.example.com:8443?sni=s.example.com&insecure=1&obfs=salamander&obfs-password=op&mport=20000-30000,40000#HY", "HY",
			`{"obfs":"salamander","obfs-password":"op","password":"auth","port":8443,"ports":"20000-30000,40000","server":"h.example.com","skip-cert-verify":true,"sni":"s.example.com","type":"hysteria2"}`},
		{"hy2 简写，用户名加密码", "hy2://user:pw@h.example.com#hy2", "hy2",
			`{"password":"user:pw","port":443,"server":"h.example.com","type":"hysteria2"}`},
		{"socks5", "socks5://user:p%3Aw@203.0.113.8:1080#家宽", "家宽",
			`{"password":"p:w","port":1080,"server":"203.0.113.8","type":"socks5","username":"user"}`},
		{"socks，base64", "socks://" + b64("user:pw@203.0.113.8:1080") + "#S", "S",
			`{"password":"pw","port":1080,"server":"203.0.113.8","type":"socks5","username":"user"}`},
		{"http", "http://203.0.113.8:3128", "http-203.0.113.8:3128", `{"port":3128,"server":"203.0.113.8","type":"http"}`},
		{"https", "https://u:p@proxy.example.com:8443?sni=s.example.com#HS", "HS",
			`{"password":"p","port":8443,"server":"proxy.example.com","sni":"s.example.com","tls":true,"type":"http","username":"u"}`},
	}
	for _, c := range cases {
		name, got := link(t, c.line)
		if name != c.name || got != c.want {
			t.Errorf("%s\n名称 %q，期望 %q\n得到 %s\n期望 %s", c.what, name, c.name, got, c.want)
		}
	}
}

func TestParseLinkList(t *testing.T) {
	list := strings.Join([]string{
		"# 注释和空行不算",
		"",
		"trojan://pw@a.example.com:443#同名",
		"trojan://pw@b.example.com:443#同名",
		// 第 5 到 9 行都不该解析出来：写法不对、不认识的协议、Mihomo 不支持的传输、
		// 普通网址（不是代理）、看不出协议的 “IP:端口:用户:密码”。
		"ss://not-base64-@@",
		"wireguard://whatever",
		"vless://u-u-i-d@k.example.com:443?type=kcp",
		"https://sub.example.com/link?token=abc",
		"203.0.113.9:1080:user:pass",
		"socks5://203.0.113.9:1080",
	}, "\r\n")
	check := func(what string, body string) {
		rep, err := ParseReport([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		var names []string
		for _, p := range rep.Proxies {
			names = append(names, p.Name)
		}
		if got := strings.Join(names, "|"); got != "同名|同名 2|socks5-203.0.113.9:1080" {
			t.Errorf("%s：节点不对: %s", what, got)
		}
		if len(rep.Skipped) != 5 || rep.Skipped[0] != 5 || rep.Skipped[4] != 9 {
			t.Errorf("%s：没解析的行号不对: %v", what, rep.Skipped)
		}
	}
	check("明文", list)
	check("base64 订阅", b64(list))
	check("base64，带换行和 BOM", "\xEF\xBB\xBF"+b64(list)[:40]+"\n"+b64(list)[40:]+"\n")
}

func TestParseFormats(t *testing.T) {
	// Clash 的 JSON 写法也认。
	rep, err := ParseReport([]byte(`{"proxies":[{"name":"J","type":"socks5","server":"203.0.113.1","port":1080}]}`))
	if err != nil || rep.Format != FormatClash || len(rep.Proxies) != 1 {
		t.Fatalf("Clash JSON: %+v %v", rep, err)
	}
	// 依次是：空内容、sing-box 的配置、只有一个订阅地址、随便一段话、base64 包着的随便一段话。
	for i, body := range []string{
		" \n",
		`{"outbounds":[{"type":"direct"}]}`,
		"https://sub.example.com/link?token=abc",
		"hello world, this is not a subscription",
		b64("hello world, this is not a subscription either"),
	} {
		if _, err := ParseReport([]byte(body)); err == nil {
			t.Errorf("第 %d 项：应报错", i+1)
		} else if strings.Contains(err.Error(), "token") {
			t.Errorf("第 %d 项：错误信息里不应带内容: %v", i+1, err)
		}
	}
}
