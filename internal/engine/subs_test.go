package engine

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

// newTestEngine 建一个只有数据目录、没有 Docker 的引擎，并把宿主网络的两个入口换成假的。
func newTestEngine(t *testing.T, yaml *string, addrs map[string][]string) (*Engine, *store.Store) {
	t.Helper()
	st, err := store.Init(t.TempDir(), model.Cluster{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(st, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldFetch, oldLookup := fetchSubscription, lookupIPv4
	t.Cleanup(func() { fetchSubscription, lookupIPv4 = oldFetch, oldLookup })
	fetchSubscription = func(ctx context.Context, url string) ([]byte, error) {
		if yaml == nil {
			return nil, errors.New("不应拉取订阅")
		}
		return []byte(*yaml), nil
	}
	lookupIPv4 = func(ctx context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range addrs[host] {
			out = append(out, netip.MustParseAddr(a))
		}
		if len(out) == 0 {
			return nil, errors.New("no such host")
		}
		return out, nil
	}
	return eng, st
}

func TestTransitDomainIsResolvedOnce(t *testing.T) {
	addrs := map[string][]string{"hk.example.com": {"203.0.113.9", "203.0.113.2"}}
	eng, st := newTestEngine(t, nil, addrs)
	ctx := context.Background()
	socks := func(server string) map[string]any {
		return map[string]any{"type": "socks5", "server": server, "port": 1080}
	}

	if err := eng.AddNode(ctx, model.Node{ID: "hk", Role: model.RoleTransit, Proxy: socks("hk.example.com")}); err != nil {
		t.Fatal(err)
	}
	n, _ := st.Node("hk")
	if n.ConnectIPv4 != "203.0.113.2" || !n.ConnectAuto {
		t.Fatalf("应取排序后的第一个地址并标记为自动解析: %+v", n)
	}

	// 已有的地址仍在解析结果里时不换，避免轮转的域名让配置来回变。
	addrs["hk.example.com"] = []string{"203.0.113.1", "203.0.113.2"}
	if err := resolveFirstHop(ctx, &n); err != nil || n.ConnectIPv4 != "203.0.113.2" {
		t.Fatalf("地址仍然有效时应保持不变: %s %v", n.ConnectIPv4, err)
	}
	// 明确要求重新解析时才换。
	addrs["hk.example.com"] = []string{"203.0.113.50"}
	if ip, err := eng.ResolveNode(ctx, "hk"); err != nil || ip != "203.0.113.50" {
		t.Fatalf("重新解析的结果不对: %s %v", ip, err)
	}

	// 手工指定的地址不被覆盖；落地节点登记时不解析（它在中转之后，由核心经中转解析）。
	if err := eng.AddNode(ctx, model.Node{ID: "manual", Role: model.RoleTransit, Proxy: socks("hk.example.com"), ConnectIPv4: "198.51.100.7"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Node("manual"); n.ConnectIPv4 != "198.51.100.7" || n.ConnectAuto {
		t.Fatalf("手工指定的地址应原样保留: %+v", n)
	}
	if _, err := eng.ResolveNode(ctx, "manual"); err == nil {
		t.Fatal("手工指定的地址不应被自动解析覆盖")
	}
	if err := eng.AddNode(ctx, model.Node{ID: "land", Role: model.RoleLanding, Proxy: socks("nowhere.example.com")}); err != nil {
		t.Fatalf("落地节点登记时不应解析: %v", err)
	}
	if err := eng.AddNode(ctx, model.Node{ID: "bad", Role: model.RoleTransit, Proxy: socks("nowhere.example.com")}); err == nil {
		t.Fatal("中转的域名解析不出来时应报错")
	}

	// 专用网关直连落地：登记槽位时才需要落地的地址，此时解析并记下。
	addrs["nowhere.example.com"] = []string{"198.51.100.20"}
	if err := eng.AddGateway(model.Gateway{ID: "solo"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.AddSlot(ctx, model.Slot{ID: "s", Gateway: "solo", Landing: "land", UID: 1000, GID: 1000}, SlotOptions{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Node("land"); n.ConnectIPv4 != "198.51.100.20" || !n.ConnectAuto {
		t.Fatalf("专用网关的落地应在登记槽位时解析: %+v", n)
	}
}

func TestFakeIPFirstHopIsKeptAndFollowed(t *testing.T) {
	// 宿主或上游路由器在用 fake-ip 模式的透明代理时，系统解析器返回 198.18.0.0/15 里的假地址。
	// 这个地址要原样采用：只有连它才通。透明代理重启后映射会变，启动网关前要跟着更新。
	addrs := map[string][]string{"hk.example.com": {"198.18.0.160"}}
	eng, st := newTestEngine(t, nil, addrs)
	ctx := context.Background()
	if err := eng.AddNode(ctx, model.Node{ID: "hk", Role: model.RoleTransit, Proxy: map[string]any{"type": "socks5", "server": "hk.example.com", "port": 1080}}); err != nil {
		t.Fatal(err)
	}
	n, _ := st.Node("hk")
	if n.ConnectIPv4 != "198.18.0.160" || !IsFakeIP(n.ConnectIPv4) || IsFakeIP("203.0.113.8") {
		t.Fatalf("假地址应原样记下并能被认出来: %+v", n)
	}

	addrs["hk.example.com"] = []string{"198.18.3.77"}
	eng.refreshFirstHop(ctx, "hk")
	if n, _ := st.Node("hk"); n.ConnectIPv4 != "198.18.3.77" {
		t.Fatalf("映射变了以后应更新: %+v", n)
	}
	// 解析不出来时沿用已记下的地址，不清空。
	delete(addrs, "hk.example.com")
	eng.refreshFirstHop(ctx, "hk")
	if n, _ := st.Node("hk"); n.ConnectIPv4 != "198.18.3.77" {
		t.Fatalf("解析失败时应沿用旧地址: %+v", n)
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	yaml := `proxies:
  - {name: "HK 01", type: ss, server: hk1.example.com, port: 8388, cipher: aes-256-gcm, password: old}
  - {name: "JP 02", type: socks5, server: 203.0.113.9, port: 1080}
`
	eng, st := newTestEngine(t, &yaml, map[string][]string{"hk1.example.com": {"203.0.113.2"}, "hk9.example.com": {"203.0.113.77"}})
	ctx := context.Background()
	const url = "https://sub.example.com/link?token=SECRET-TOKEN"

	sub, err := eng.SubAdd(ctx, "main", url, "")
	if err != nil || len(sub.Proxies) != 2 {
		t.Fatalf("登记订阅失败: %v %+v", err, sub)
	}
	if _, err := eng.SubAdd(ctx, "main", url, ""); err == nil {
		t.Fatal("同名订阅应被拒绝")
	}
	if _, err := eng.SubAdd(ctx, "other", "ftp://x", ""); err == nil {
		t.Fatal("非 HTTP 的订阅地址应被拒绝")
	}

	if err := eng.NodeFromSub(ctx, model.Node{ID: "hk", Role: model.RoleTransit}, "main", "HK 01"); err != nil {
		t.Fatal(err)
	}
	if err := eng.NodeFromSub(ctx, model.Node{ID: "jp", Role: model.RoleLanding, TZ: "Asia/Tokyo"}, "main", "JP 02"); err != nil {
		t.Fatal(err)
	}
	if err := eng.NodeFromSub(ctx, model.Node{ID: "x", Role: model.RoleTransit}, "main", "不存在"); err == nil {
		t.Fatal("订阅里没有的名称应报错")
	}
	hk, _ := st.Node("hk")
	if hk.Source == nil || hk.Source.Sub != "main" || hk.Source.Name != "HK 01" || hk.ConnectIPv4 != "203.0.113.2" || hk.Proxy["password"] != "old" {
		t.Fatalf("取自订阅的节点不对: %+v", hk)
	}

	// 没有变化时刷新不动任何节点。
	if res, err := eng.SubRefresh(ctx, "main"); err != nil || len(res.Updated) != 0 || len(res.Missing) != 0 || res.Total != 2 {
		t.Fatalf("没有变化时不应更新: %+v %v", res, err)
	}
	// 订阅变了：HK 换了服务器和口令，JP 被拿掉。
	yaml = `proxies:
  - {name: "HK 01", type: ss, server: hk9.example.com, port: 8388, cipher: aes-256-gcm, password: new}
`
	res, err := eng.SubRefresh(ctx, "main")
	if err != nil || len(res.Updated) != 1 || res.Updated[0] != "hk" || len(res.Missing) != 1 || res.Missing[0] != "jp" {
		t.Fatalf("刷新结果不对: %+v %v", res, err)
	}
	hk, _ = st.Node("hk")
	if hk.Proxy["password"] != "new" || hk.ConnectIPv4 != "203.0.113.77" || hk.Role != model.RoleTransit {
		t.Fatalf("节点应随订阅更新并重新解析首跳: %+v", hk)
	}
	if jp, _ := st.Node("jp"); jp.Server() != "203.0.113.9" || jp.TZ != "Asia/Tokyo" {
		t.Fatalf("订阅里找不到的节点应保持原样: %+v", jp)
	}

	// 订阅地址带令牌：只出现在订阅文件里（0600），不进操作记录。
	ops, err := os.ReadFile(st.Dir() + "/ops.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ops), "SECRET-TOKEN") || strings.Contains(string(ops), "sub.example.com") {
		t.Fatalf("操作记录里不应出现订阅地址:\n%s", ops)
	}
	if fi, err := os.Stat(st.Dir() + "/subs/main.json"); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("订阅文件应为 0600: %v %v", fi, err)
	}

	if err := eng.SubRemove("main"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Node("hk"); err != nil {
		t.Fatal("删除订阅后，取自它的节点应保留")
	}
}
