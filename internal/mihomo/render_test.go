package mihomo

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

var pool = netip.MustParsePrefix("10.213.250.0/24")

func link(t *testing.T, idx int) netplan.Link {
	t.Helper()
	l, err := netplan.At(pool, idx)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func landing(id, server string) model.Node {
	return model.Node{ID: id, Role: model.RoleLanding, Proxy: map[string]any{
		"type": "socks5", "server": server, "port": float64(1080), "username": "u-" + id, "password": "secret-" + id,
	}}
}

func gateway() model.Gateway {
	g := model.Gateway{ID: "main", Transit: "hk"}
	g.Normalize()
	return g
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("生成的配置不是合法 JSON: %v", err)
	}
	return m
}

func rulesOf(t *testing.T, cfg map[string]any) []string {
	t.Helper()
	var out []string
	for _, r := range cfg["rules"].([]any) {
		out = append(out, r.(string))
	}
	return out
}

func TestRenderTransitBackups(t *testing.T) {
	node := func(id, server, ip string) model.Node {
		return model.Node{ID: id, Role: model.RoleTransit, ConnectIPv4: ip, Proxy: map[string]any{"type": "socks5", "server": server, "port": float64(1080)}}
	}
	main, spare := node("hk", "hk.example.com", "203.0.113.10"), node("sg", "203.0.113.11", "")
	in := Input{Gateway: gateway(), Transit: &main, Bindings: []Binding{{SlotID: "a", Link: link(t, 2), Landing: landing("home-a", "198.51.100.20")}}}
	single, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(single.Config), "proxy-groups") {
		t.Error("没有候选中转时不应出现代理组（已有网关的配置摘要不能变）")
	}

	in.Backups = []model.Node{spare}
	out, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, out.Config)
	if len(out.FirstHops) != 2 {
		t.Fatalf("两个中转都应是首跳: %v", out.FirstHops)
	}
	groups := cfg["proxy-groups"].([]any)
	g := groups[0].(map[string]any)
	if len(groups) != 1 || g["name"] != TransitName || g["type"] != "fallback" || fmt.Sprint(g["proxies"]) != "[transit-0 transit-1]" {
		t.Errorf("候选中转应组成一个按顺序改用的组: %v", g)
	}
	names := map[string]any{}
	for _, p := range cfg["proxies"].([]any) {
		m := p.(map[string]any)
		names[m["name"].(string)] = m["dialer-proxy"]
	}
	// 落地仍经 TransitName 拨号（现在是那个组），中转自己直连。
	if names["land-home-a"] != TransitName || names["transit-0"] != nil || names["transit-1"] != nil || len(names) != 3 {
		t.Errorf("代理与拨号方式: %v", names)
	}

	if _, err := Render(Input{Gateway: gateway(), Backups: []model.Node{spare}}); err == nil {
		t.Error("没有主用中转时不应接受候选中转")
	}
	bad := landing("home-b", "198.51.100.30")
	if _, err := Render(Input{Gateway: gateway(), Transit: &main, Backups: []model.Node{bad}}); err == nil {
		t.Error("候选中转的角色必须是中转")
	}
}

func TestRenderSharedGateway(t *testing.T) {
	transit := model.Node{ID: "hk", Role: model.RoleTransit, ConnectIPv4: "203.0.113.10", Proxy: map[string]any{
		"type": "vless", "server": "transit.example.com", "port": float64(443), "uuid": "0000", "udp": true,
	}}
	out, err := Render(Input{
		Gateway: gateway(),
		Transit: &transit,
		Bindings: []Binding{
			{SlotID: "b", Link: link(t, 3), Landing: landing("home-b", "198.51.100.30")},
			{SlotID: "a", Link: link(t, 2), Landing: landing("home-a", "198.51.100.20")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, out.Config)

	// 首跳只有中转；落地经中转到达，不出现在主路由表里。
	if len(out.FirstHops) != 1 || out.FirstHops[0].String() != "203.0.113.10" {
		t.Fatalf("首跳应只有中转地址: %v", out.FirstHops)
	}
	if got := cfg["hosts"].(map[string]any)["transit.example.com"]; got != "203.0.113.10" {
		t.Fatalf("中转域名应固定到 connect_ipv4: %v", got)
	}

	tun := cfg["tun"].(map[string]any)
	if tun["auto-route"] != false || tun["device"] != "cc0" || tun["stack"] != "gvisor" {
		t.Fatalf("TUN 设置不符合骨架: %v", tun)
	}
	if cfg["ipv6"] != false || cfg["allow-lan"] != false {
		t.Fatal("必须关闭 IPv6 与 allow-lan")
	}
	if _, has := cfg["external-controller"]; has {
		t.Fatal("不允许出现 TCP 控制端口")
	}
	if _, has := cfg["proxy-groups"]; has {
		t.Fatal("不允许出现代理组")
	}

	proxies := cfg["proxies"].([]any)
	if len(proxies) != 3 {
		t.Fatalf("应有 1 个中转 + 2 个落地: %d", len(proxies))
	}
	byName := map[string]map[string]any{}
	for _, p := range proxies {
		m := p.(map[string]any)
		byName[m["name"].(string)] = m
		if m["udp"] != false {
			t.Errorf("%s 的 udp 必须被强制为 false", m["name"])
		}
	}
	if _, has := byName["transit"]["dialer-proxy"]; has {
		t.Error("中转不应有 dialer-proxy")
	}
	for _, n := range []string{"land-home-a", "land-home-b"} {
		if byName[n]["dialer-proxy"] != "transit" {
			t.Errorf("%s 必须经中转拨号", n)
		}
	}

	rules := rulesOf(t, cfg)
	if rules[0] != "NETWORK,udp,REJECT" || rules[len(rules)-1] != "MATCH,REJECT" {
		t.Fatalf("首条必须拒绝 UDP、末条必须兜底拒绝: %v", rules)
	}
	joined := strings.Join(rules, "\n")
	for _, want := range []string{
		"SRC-IP-CIDR,10.213.250.8/30,land-home-a",
		"SRC-IP-CIDR,10.213.250.12/30,land-home-b",
		"IP-CIDR,10.0.0.0/8,REJECT,no-resolve",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少规则 %q", want)
		}
	}
	if strings.Contains(joined, "DIRECT") {
		t.Fatal("规则里不允许出现 DIRECT")
	}
	if strings.Index(joined, "IP-CIDR,10.0.0.0/8") > strings.Index(joined, "SRC-IP-CIDR") {
		t.Fatal("私网拒绝规则必须排在来源分流之前")
	}

	ls := cfg["listeners"].([]any)
	if len(ls) != 2 {
		t.Fatalf("每个槽一个回环探测入口: %d", len(ls))
	}
	first := ls[0].(map[string]any)
	if first["listen"] != "127.0.0.1" || first["port"] != float64(17002) || first["proxy"] != "land-home-a" {
		t.Fatalf("探测入口应按序号排序并只监听回环: %v", first)
	}

	dns := cfg["dns"].(map[string]any)
	ns := dns["nameserver"].([]any)
	if ns[0] != "https://1.1.1.1/dns-query#transit" {
		t.Fatalf("核心解析应经中转: %v", ns)
	}
	// 槽内的 A 查询由核心答映射地址，域名交给落地解析：外部看到的解析器才在落地所在地。
	if dns["enhanced-mode"] != "fake-ip" || dns["fake-ip-range"] != FakeIPRange {
		t.Fatalf("应使用 fake-ip 并指定映射地址段: %v", dns)
	}
	// 映射地址段不能与局域网透明代理常用的 198.18.0.0/15 重叠（首跳可能落在那里面）。
	if pfx := netip.MustParsePrefix(FakeIPRange).Masked(); pfx.Overlaps(netip.MustParsePrefix("198.18.0.0/15")) {
		t.Fatalf("映射地址段 %s 与 198.18.0.0/15 重叠", pfx)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	transit := model.Node{ID: "hk", Role: model.RoleTransit, Proxy: map[string]any{"type": "socks5", "server": "203.0.113.3", "port": float64(1080)}}
	a := Binding{SlotID: "a", Link: link(t, 0), Landing: landing("la", "198.51.100.3")}
	b := Binding{SlotID: "b", Link: link(t, 1), Landing: landing("lb", "198.51.100.4")}
	x, err := Render(Input{Gateway: gateway(), Transit: &transit, Bindings: []Binding{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	y, _ := Render(Input{Gateway: gateway(), Transit: &transit, Bindings: []Binding{b, a}})
	if x.Digest != y.Digest {
		t.Fatal("绑定顺序不应影响生成结果")
	}
	z, _ := Render(Input{Gateway: gateway(), Transit: &transit, Bindings: []Binding{a}})
	if z.Digest == x.Digest {
		t.Fatal("绑定变化必须改变摘要")
	}
}

func TestRenderDedicatedGateway(t *testing.T) {
	g := gateway()
	g.Transit = ""
	out, err := Render(Input{Gateway: g, Bindings: []Binding{{SlotID: "a", Link: link(t, 0), Landing: landing("la", "198.51.100.20")}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, out.Config)
	if len(out.FirstHops) != 1 || out.FirstHops[0].String() != "198.51.100.20" {
		t.Fatalf("单跳时首跳是落地: %v", out.FirstHops)
	}
	p := cfg["proxies"].([]any)[0].(map[string]any)
	if _, has := p["dialer-proxy"]; has {
		t.Fatal("单跳落地不应有 dialer-proxy")
	}
	if ns := cfg["dns"].(map[string]any)["nameserver"].([]any); ns[0] != "https://1.1.1.1/dns-query#land-la" {
		t.Fatalf("专用网关的解析应经本槽落地: %v", ns)
	}

	// 没有中转的网关也可以接多个槽：各自直连自己的出口。核心自己的解析此时不指定出口
	//（不让一个槽的查询从另一个槽的出口出去）。
	out, err = Render(Input{Gateway: g, Bindings: []Binding{
		{SlotID: "a", Link: link(t, 0), Landing: landing("la", "198.51.100.20")},
		{SlotID: "b", Link: link(t, 1), Landing: landing("lb", "198.51.100.30")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg = decode(t, out.Config)
	if len(out.FirstHops) != 2 {
		t.Fatalf("两个出口都应是首跳: %v", out.FirstHops)
	}
	if ns := cfg["dns"].(map[string]any)["nameserver"].([]any); ns[0] != "https://1.1.1.1/dns-query" {
		t.Fatalf("有几个出口时核心的解析不应指定出口: %v", ns)
	}
	for _, p := range cfg["proxies"].([]any) {
		if _, has := p.(map[string]any)["dialer-proxy"]; has {
			t.Fatal("直连的出口不应有 dialer-proxy")
		}
	}
}

func TestRenderEmptyGateway(t *testing.T) {
	g := gateway()
	g.Transit = ""
	out, err := Render(Input{Gateway: g})
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, out.Config)
	if len(out.FirstHops) != 0 || len(cfg["proxies"].([]any)) != 0 {
		t.Fatal("空网关不应有首跳和代理")
	}
	if got := rulesOf(t, cfg); got[len(got)-1] != "MATCH,REJECT" {
		t.Fatal("空网关也必须兜底拒绝")
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	transit := model.Node{ID: "hk", Role: model.RoleTransit, Proxy: map[string]any{"type": "vless", "server": "t.example.com", "port": float64(443)}}
	if _, err := Render(Input{Gateway: gateway(), Transit: &transit}); err == nil {
		t.Error("首跳是域名且没有 connect_ipv4 时应报错")
	}
	ok := model.Node{ID: "hk", Role: model.RoleTransit, Proxy: map[string]any{"type": "socks5", "server": "203.0.113.3", "port": float64(1080)}}
	wrongRole := landing("x", "198.51.100.9")
	wrongRole.Role = model.RoleTransit
	if _, err := Render(Input{Gateway: gateway(), Transit: &ok, Bindings: []Binding{{SlotID: "a", Link: link(t, 0), Landing: wrongRole}}}); err == nil {
		t.Error("槽位引用非落地节点时应报错")
	}
	dup := []Binding{
		{SlotID: "a", Link: link(t, 0), Landing: landing("la", "198.51.100.3")},
		{SlotID: "b", Link: link(t, 0), Landing: landing("lb", "198.51.100.4")},
	}
	if _, err := Render(Input{Gateway: gateway(), Transit: &ok, Bindings: dup}); err == nil {
		t.Error("两个槽占同一网段时应报错")
	}
}

func TestProxyEntryStripsControlledFields(t *testing.T) {
	n := landing("la", "198.51.100.3")
	n.Proxy["dialer-proxy"] = "evil"
	n.Proxy["interface-name"] = "eth0"
	n.Proxy["routing-mark"] = float64(1)
	p := proxyEntry(n, "land-la", "")
	for _, k := range []string{"dialer-proxy", "interface-name", "routing-mark"} {
		if _, has := p[k]; has {
			t.Errorf("%s 应被去掉", k)
		}
	}
	if _, has := n.Proxy["name"]; has {
		t.Error("不应修改原始片段")
	}
}

func TestRedactHidesCredentials(t *testing.T) {
	transit := model.Node{ID: "hk", Role: model.RoleTransit, Proxy: map[string]any{"type": "vless", "server": "203.0.113.3", "port": float64(443), "uuid": "11111111-secret"}}
	out, err := Render(Input{Gateway: gateway(), Transit: &transit, Bindings: []Binding{{SlotID: "a", Link: link(t, 0), Landing: landing("la", "198.51.100.3")}}})
	if err != nil {
		t.Fatal(err)
	}
	red, err := Redact(out.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-la", "u-la", "11111111-secret"} {
		if !strings.Contains(string(out.Config), secret) {
			t.Fatalf("测试前提不成立：原始配置里应有 %s", secret)
		}
		if strings.Contains(string(red), secret) {
			t.Errorf("预览里泄漏了 %s", secret)
		}
	}
	if !strings.Contains(string(red), "198.51.100.3") {
		t.Error("预览应保留 server 以便排查")
	}
}
