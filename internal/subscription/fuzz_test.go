package subscription

import (
	"encoding/json"
	"testing"
)

// FuzzParseReport 检查订阅解析对任意内容都不崩溃：远程订阅在后台循环里定时刷新，
// 那里的崩溃会结束整个网页进程，持续检测也随之停止。
// 平时随单元测试只跑下面的样例；要长时间跑时用 go test -fuzz=FuzzParseReport ./internal/subscription。
func FuzzParseReport(f *testing.F) {
	// 协议头拆成两段写，理由同 links_test.go。
	seeds := []string{
		"ss:" + "//" + b64("aes-256-gcm:pw") + "@a.example.com:8388#n",
		"ss:" + "//" + b64("aes-128-gcm:pw@203.0.113.5:443"),
		"ss:" + "//" + b64("aes-128-gcm:pw") + "@a.example.com:80?plugin=obfs-local;obfs=http;obfs-host=h.example.com",
		"vmess:" + "//" + b64(`{"ps":"n","add":"a.example.com","port":"443","id":"u-u-i-d","net":"ws","tls":"tls"}`),
		"vmess:" + "//" + b64("aes-128-gcm:u-u-i-d@a.example.com:443") + "?remarks=n&obfs=websocket",
		"vmess:" + "//grpc+tls:u-u-i-d-0@a.example.com:443/?serviceName=s#n",
		"vless:" + "//u-u-i-d@a.example.com:443?security=reality&pbk=K&sid=ab&type=tcp#n",
		"trojan:" + "//pw@a.example.com:443?type=grpc&serviceName=g#n",
		"hysteria2:" + "//auth@a.example.com:8443?mport=20000-30000,40000#n",
		"hy2:" + "//u:p@a.example.com#n",
		"socks5:" + "//u:p@203.0.113.8:1080#n",
		"socks:" + "//" + b64("u:p@203.0.113.8:1080") + "#n",
		"https:" + "//u:p@a.example.com:8443?sni=s.example.com#n",
		"proxies:\n  - {name: n, type: ss, server: a.example.com, port: 8388, cipher: aes-128-gcm, password: pw}\n",
		`{"proxies":[{"name":"n","type":"http","server":"a.example.com","port":3128}]}`,
		b64("trojan:" + "//pw@a.example.com:443#n\n"),
		"ss:" + "//@", "vmess:" + "//", "vless:" + "//@:", "trojan:" + "//@[::1", "hy2:" + "//@:0?mport=-", "socks:" + "//#", "://", "#", "",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		rep, err := ParseReport(body)
		if err != nil {
			return
		}
		if len(rep.Proxies) == 0 {
			t.Fatal("没有报错，也没有节点")
		}
		for _, p := range rep.Proxies {
			if p.Name == "" {
				t.Fatalf("节点没有名称：%+v", p.Proxy)
			}
			// 片段要写进数据目录，再渲染成核心配置。
			if _, err := json.Marshal(p.Proxy); err != nil {
				t.Fatalf("节点 %q 的片段无法序列化：%v", p.Name, err)
			}
		}
	})
}
