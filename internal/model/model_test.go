package model

import (
	"strings"
	"testing"
)

func validNode() Node {
	return Node{ID: "home-a", Role: RoleLanding, Proxy: map[string]any{"type": "socks5", "server": "198.51.100.3", "port": float64(1080)}}
}

func TestValidateID(t *testing.T) {
	for _, ok := range []string{"a", "slot-1", "0x", strings.Repeat("a", 31)} {
		if err := ValidateID("槽位", ok); err != nil {
			t.Errorf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "A", "a_b", "a/b", "..", "a b", strings.Repeat("a", 32)} {
		if err := ValidateID("槽位", bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	// 报错里给出改好的写法，并指向备注。
	err := ValidateID("节点", "kr_home_lg")
	if err == nil || !strings.Contains(err.Error(), `"kr-home-lg"`) || !strings.Contains(err.Error(), "备注") {
		t.Errorf("报错应给出可用的写法并提到备注: %v", err)
	}
	if err := ValidateID("节点", "韩国家宽"); err == nil || strings.Contains(err.Error(), "可以改用") {
		t.Errorf("改不出合法写法时不应给建议: %v", err)
	}
}

func TestSuggestID(t *testing.T) {
	for in, want := range map[string]string{
		"kr_home_lg":                   "kr-home-lg",
		"KR Home.LG":                   "kr-home-lg",
		"_a__b_":                       "a-b",
		"韩国 lg":                        "lg",
		"韩国家宽":                         "",
		"":                             "",
		strings.Repeat("a", 40):        strings.Repeat("a", 31),
		strings.Repeat("a", 30) + "_b": strings.Repeat("a", 30),
	} {
		got := SuggestID(in)
		if got != want {
			t.Errorf("SuggestID(%q) = %q，期望 %q", in, got, want)
		}
		if got != "" && ValidateID("节点", got) != nil {
			t.Errorf("SuggestID(%q) 给出的 %q 本身不合法", in, got)
		}
	}
}

func TestValidateNote(t *testing.T) {
	for _, ok := range []string{"", "韩国家宽 LG", "kr_home_lg", strings.Repeat("中", MaxNoteLen)} {
		if err := ValidateNote(ok); err != nil {
			t.Errorf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"a\nb", "a\tb", "a\x1b[31m", strings.Repeat("中", MaxNoteLen+1)} {
		if err := ValidateNote(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
}

func TestNodeValidate(t *testing.T) {
	if err := validNode().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Node){
		"缺 type":       func(n *Node) { delete(n.Proxy, "type") },
		"缺 server":     func(n *Node) { delete(n.Proxy, "server") },
		"端口越界":         func(n *Node) { n.Proxy["port"] = float64(70000) },
		"端口非整数":        func(n *Node) { n.Proxy["port"] = 1080.5 },
		"自带拨号链":        func(n *Node) { n.Proxy["dialer-proxy"] = "x" },
		"自带出口网卡":       func(n *Node) { n.Proxy["interface-name"] = "eth0" },
		"自带路由标记":       func(n *Node) { n.Proxy["routing-mark"] = float64(1) },
		"角色不对":         func(n *Node) { n.Role = "exit" },
		"connect 非 IP": func(n *Node) { n.ConnectIPv4 = "example.com" },
		"connect 是 v6": func(n *Node) { n.ConnectIPv4 = "::1" },
		"中转带预期出口":      func(n *Node) { n.Role = RoleTransit; n.ExpectExitIP = "198.51.100.3" },
	}
	for name, mutate := range cases {
		n := validNode()
		mutate(&n)
		if err := n.Validate(); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
}

func TestNodePortForms(t *testing.T) {
	for _, v := range []any{float64(443), 443, int64(443), "443"} {
		n := validNode()
		n.Proxy["port"] = v
		if n.Port() != 443 {
			t.Errorf("port %T(%v) 应解析为 443，得到 %d", v, v, n.Port())
		}
	}
}

func TestFirstHopIP(t *testing.T) {
	n := validNode()
	if a, err := n.FirstHopIP(); err != nil || a.String() != "198.51.100.3" {
		t.Fatalf("IP 字面量应直接作首跳: %v %v", a, err)
	}
	n.Proxy["server"] = "node.example.com"
	if _, err := n.FirstHopIP(); err == nil {
		t.Fatal("域名且未给 connect_ipv4 时应报错")
	}
	n.ConnectIPv4 = "203.0.113.9"
	if a, err := n.FirstHopIP(); err != nil || a.String() != "203.0.113.9" {
		t.Fatalf("应使用 connect_ipv4: %v %v", a, err)
	}
	n.ConnectIPv4 = ""
	n.Proxy["server"] = "2001:db8::1"
	if _, err := n.FirstHopIP(); err == nil {
		t.Fatal("IPv6 首跳应被拒绝")
	}
}

func TestGatewayValidate(t *testing.T) {
	g := Gateway{ID: "main"}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Gateway){
		"地址池太小":     func(g *Gateway) { g.Pool = "10.0.0.0/31" },
		"地址池非网络地址":  func(g *Gateway) { g.Pool = "10.0.0.1/24" },
		"地址池是公网":    func(g *Gateway) { g.Pool = "8.8.8.0/24" },
		"DNS 用域名":   func(g *Gateway) { g.DNS = []string{"https://dns.google/dns-query"} },
		"DNS 走 UDP": func(g *Gateway) { g.DNS = []string{"1.1.1.1"} },
		"DNS 自带链后缀": func(g *Gateway) { g.DNS = []string{"https://1.1.1.1/dns-query#DIRECT"} },
		"探测地址协议不对":  func(g *Gateway) { g.EchoURLs = []string{"ftp://x"} },
		"探测地址含引号":   func(g *Gateway) { g.EchoURLs = []string{"http://x/'; rm -rf /'"} },
		"日志级别不对":    func(g *Gateway) { g.LogLevel = "verbose" },
		"中转 ID 不合法": func(g *Gateway) { g.Transit = "Bad ID" },
	}
	for name, mutate := range cases {
		c := g
		c.DNS = append([]string(nil), g.DNS...)
		c.EchoURLs = append([]string(nil), g.EchoURLs...)
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
}

func TestSlotValidate(t *testing.T) {
	base := Slot{ID: "a", Gateway: "main", Landing: "home-a", Subnet: "10.213.250.8/30", UID: 1000, GID: 1000}
	base.Normalize()
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Slot){
		"网段不是 /30":   func(s *Slot) { s.Subnet = "10.213.250.8/29" },
		"网段非网络地址":    func(s *Slot) { s.Subnet = "10.213.250.9/30" },
		"root 用户":    func(s *Slot) { s.UID = 0 },
		"代理变量":       func(s *Slot) { s.Env = map[string]string{"HTTPS_PROXY": "http://x"} },
		"小写代理变量":     func(s *Slot) { s.Env = map[string]string{"all_proxy": "socks5://x"} },
		"挂载源相对路径":    func(s *Slot) { s.Mounts = []Mount{{Source: "proj", Target: "/work"}} },
		"挂载目标占用":     func(s *Slot) { s.Mounts = []Mount{{Source: "/data/p", Target: "/etc/passwd"}} },
		"挂到根目录":      func(s *Slot) { s.Mounts = []Mount{{Source: "/data/p", Target: "/"}} },
		"挂进系统目录":     func(s *Slot) { s.Mounts = []Mount{{Source: "/usr/local/x", Target: "/usr/local/x"}} },
		"时区带空格":      func(s *Slot) { s.TZ = "Asia/Tokyo; id" },
		"语言带斜杠":      func(s *Slot) { s.Lang = "en_US/UTF-8" },
		"无限额却改 proc": func(s *Slot) { s.FakeProc = true },
		"挂载目标不规范":    func(s *Slot) { s.Mounts = []Mount{{Source: "/data/p", Target: "/work/../etc"}} },
		"挂 Docker 口": func(s *Slot) { s.Mounts = []Mount{{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"}} },
		"路径含冒号":      func(s *Slot) { s.Mounts = []Mount{{Source: "/data/p:rw", Target: "/work"}} },
		"shell 相对":   func(s *Slot) { s.Shell = "bash" },
	}
	for name, mutate := range cases {
		c := base
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
	ok := base
	ok.Mounts = []Mount{{Source: "/data/proj", Target: "/work", ReadOnly: true}}
	ok.Env = map[string]string{"EDITOR": "vim"}
	ok.TZ, ok.Lang = "America/Argentina/Buenos_Aires", "en_US.UTF-8"
	if err := ok.Validate(); err != nil {
		t.Fatalf("正常的挂载与环境变量应通过: %v", err)
	}
}

func TestCheckMountTargetProtectsHome(t *testing.T) {
	for _, bad := range []string{"/home/kevin", "/home"} {
		if err := CheckMountTarget(bad, "/home/kevin"); err == nil {
			t.Errorf("%s 会盖住 HOME，应被拒绝", bad)
		}
	}
	for _, good := range []string{"/home/kevin/work", "/home/dev/proj", "/data/proj", "/tmp/x"} {
		if err := CheckMountTarget(good, "/home/kevin"); err != nil {
			t.Errorf("%s 应允许: %v", good, err)
		}
	}
}

func TestEffectiveLocale(t *testing.T) {
	landing := Node{TZ: "Asia/Tokyo", Lang: "ja_JP.UTF-8"}
	if tz, lang := EffectiveLocale(Slot{}, landing); tz != "Asia/Tokyo" || lang != "ja_JP.UTF-8" {
		t.Fatalf("槽位未指定时应沿用落地: %s %s", tz, lang)
	}
	if tz, lang := EffectiveLocale(Slot{TZ: "Europe/Paris", Lang: "fr_FR.UTF-8"}, landing); tz != "Europe/Paris" || lang != "fr_FR.UTF-8" {
		t.Fatalf("槽位自己的设置优先: %s %s", tz, lang)
	}
	if tz, lang := EffectiveLocale(Slot{}, Node{}); tz != "" || lang != DefaultLang {
		t.Fatalf("都没有时时区为空、语言用默认: %q %s", tz, lang)
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{"": 0, "1024": 1024, "512m": 512 << 20, "8g": 8 << 30, "1.5G": 3 << 29, "64k": 64 << 10}
	for in, want := range cases {
		got, err := ParseBytes(in)
		if err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v；期望 %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"abc", "-1g", "g"} {
		if _, err := ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) 应报错", bad)
		}
	}
}
