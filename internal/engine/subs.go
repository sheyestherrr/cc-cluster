package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/subscription"
)

// 以下三个函数是引擎与宿主网络打交道的全部入口，测试里替换成假的。
var (
	// fetchSubscription 拉取订阅内容。
	fetchSubscription = func(ctx context.Context, url string) ([]byte, error) {
		return subscription.Fetch(ctx, nil, url)
	}
	// lookupIPv4 在宿主上解析一个域名的 IPv4 地址。
	lookupIPv4 = func(ctx context.Context, host string) ([]netip.Addr, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	}
)

// fakeIPRange 是 fake-ip 模式的透明代理（Clash / Mihomo 等）返回给客户端的假地址段。
// 宿主或上游路由器在用这种代理时，系统解析器对任何域名都返回这个段里的地址。
var fakeIPRange = netip.MustParsePrefix("198.18.0.0/15")

// IsFakeIP 报告一个地址是否落在 fake-ip 地址段里。
func IsFakeIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && fakeIPRange.Contains(a)
}

// resolveFirstHop 在节点的 server 是域名且没有手工指定首跳地址时，在宿主上解析并记进节点。
//
// 网关的主路由表只放行首跳地址，核心不能自己去解析它，所以要事先知道。解析用宿主的系统
// 解析器，结果原样采用：宿主上别的程序怎样到达这台服务器，网关就怎样到达。
//
// 这一点在宿主或上游路由器使用 fake-ip 透明代理的网络里尤其要紧：那里解析到的是假地址，
// 而且只有连这个假地址才通——透明代理靠它认出目标域名。实测中绕开它去连服务器的真实地址，
// 连接反而被透明代理按 TLS 里的域名改了去向，中转的握手认证失败。
//
// 多个地址时取排序后的第一个；已记下的地址仍在解析结果里就不换，配置不会无故变化。
func resolveFirstHop(ctx context.Context, n *model.Node) error {
	if !n.ServerIsDomain() || (n.ConnectIPv4 != "" && !n.ConnectAuto) {
		return nil
	}
	addrs, err := lookupIPv4(ctx, n.Server())
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("无法解析节点 %s 的服务器地址 %s；检查宿主的网络，或用 --connect-ipv4 指定", n.ID, n.Server())
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
	for _, a := range addrs {
		if a.String() == n.ConnectIPv4 {
			return nil
		}
	}
	n.ConnectIPv4, n.ConnectAuto = addrs[0].String(), true
	return nil
}

// refreshFirstHop 在启动网关前核对一个自动解析的首跳地址，变了就更新并保存。
// fake-ip 的映射在透明代理重启后会变，所以每次都核对；解析不出来时沿用已记下的地址。
func (e *Engine) refreshFirstHop(ctx context.Context, id string) {
	n, err := e.st.Node(id)
	if err != nil || !n.ConnectAuto {
		return
	}
	old := n.ConnectIPv4
	if err := resolveFirstHop(ctx, &n); err != nil {
		e.logf("提醒：%v；沿用已记下的 %s", err, old)
		return
	}
	if n.ConnectIPv4 == old {
		return
	}
	if err := e.st.SaveNode(n, false); err != nil {
		e.logf("提醒：保存节点 %s 的新首跳地址失败：%v", id, err)
		return
	}
	e.logf("节点 %s 的首跳地址由 %s 变为 %s", id, old, n.ConnectIPv4)
}

// ResolveNode 重新解析一个节点的首跳地址，返回解析后的地址。
func (e *Engine) ResolveNode(ctx context.Context, id string) (ip string, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	defer func() { e.record("node.resolve", id, err, ip) }()
	n, err := e.st.Node(id)
	if err != nil {
		return "", err
	}
	if !n.ServerIsDomain() {
		return n.Server(), nil
	}
	if n.ConnectIPv4 != "" && !n.ConnectAuto {
		return "", fmt.Errorf("节点 %s 的首跳地址是手工指定的（%s），不自动解析", id, n.ConnectIPv4)
	}
	n.ConnectIPv4 = ""
	if err := resolveFirstHop(ctx, &n); err != nil {
		return "", err
	}
	return n.ConnectIPv4, e.st.SaveNode(n, false)
}

// SubAdd 登记一个订阅并拉取一次。note 是备注，可以为空。
func (e *Engine) SubAdd(ctx context.Context, id, url, note string) (sub model.Subscription, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return sub, err
	}
	defer unlock()
	// 操作记录里不写订阅地址：地址里通常带着令牌。
	defer func() { e.record("sub.add", id, err, fmt.Sprintf("%d 个节点", len(sub.Proxies))) }()
	if err := model.ValidateID("订阅", id); err != nil {
		return sub, err
	}
	if err := model.ValidateNote(note); err != nil {
		return sub, err
	}
	if err := subscription.CheckURL(url); err != nil {
		return sub, err
	}
	if _, err := e.st.Sub(id); err == nil {
		return sub, fmt.Errorf("订阅 %s 已存在", id)
	}
	sub = model.Subscription{ID: id, Note: note, URL: url}
	if err := e.pull(ctx, &sub); err != nil {
		return sub, err
	}
	return sub, e.st.SaveSub(sub, true)
}

func (e *Engine) pull(ctx context.Context, sub *model.Subscription) error {
	body, err := fetchSubscription(ctx, sub.URL)
	if err != nil {
		return err
	}
	proxies, err := subscription.Parse(body)
	if err != nil {
		return err
	}
	sub.Proxies, sub.FetchedAt = proxies, time.Now().UTC()
	return nil
}

// RefreshResult 是刷新订阅的结果。
type RefreshResult struct {
	Total   int      // 订阅里的节点数
	Updated []string // 片段有变化、已更新的节点 ID
	Missing []string // 订阅里已经找不到的节点 ID；它们保持原样
}

// SubRefresh 重新拉取订阅，并更新取自它的节点。网关上已经生效的配置不会自动改变：
// 之后执行 gateway up 或 slot up 才应用。
func (e *Engine) SubRefresh(ctx context.Context, id string) (res RefreshResult, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return res, err
	}
	defer unlock()
	defer func() {
		e.record("sub.refresh", id, err, fmt.Sprintf("更新 %d 个节点，%d 个已不在订阅里", len(res.Updated), len(res.Missing)))
	}()
	sub, err := e.st.Sub(id)
	if err != nil {
		return res, err
	}
	if err := e.pull(ctx, &sub); err != nil {
		return res, err
	}
	if err := e.st.SaveSub(sub, false); err != nil {
		return res, err
	}
	res.Total = len(sub.Proxies)

	nodes, err := e.st.Nodes()
	if err != nil {
		return res, err
	}
	for _, n := range nodes {
		if n.Source == nil || n.Source.Sub != id {
			continue
		}
		p, ok := sub.Find(n.Source.Name)
		if !ok {
			res.Missing = append(res.Missing, n.ID)
			continue
		}
		if sameJSON(p.Proxy, n.Proxy) {
			continue
		}
		n.Proxy = p.Proxy
		if n.ConnectAuto {
			n.ConnectIPv4 = ""
			if err := resolveFirstHop(ctx, &n); err != nil {
				return res, err
			}
		}
		if err := n.Validate(); err != nil {
			return res, err
		}
		if err := e.st.SaveNode(n, false); err != nil {
			return res, err
		}
		res.Updated = append(res.Updated, n.ID)
	}
	return res, nil
}

// sameJSON 比较两个片段存成 JSON 后是否相同。刚从 YAML 解出的整数与从 JSON 文件读回的浮点数
// 在这种比较下是相等的。
func sameJSON(a, b map[string]any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// SubRemove 删除一个订阅。取自它的节点保留，只是不能再随订阅刷新。
func (e *Engine) SubRemove(id string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("sub.rm", id, err, "") }()
	return e.st.DeleteSub(id)
}

// DirectGatewayID 是“直出”槽位默认共用的那个不带中转的网关。
const DirectGatewayID = "direct"

// DirectNotice 是登记直出槽位时给用户的提醒。
const DirectNotice = "这个槽位直接用订阅里的节点当出口：出口是多人共用的地址；节点已固定，不会自动换成别的，它不通时槽位就是没网，要换请手动改"

// PrepareDirect 为“不用落地、直接用订阅里的一个节点当出口”的槽位做准备：把订阅 subID 里名为
// name 的节点登记为该槽专用的出口节点（<槽位>-exit，标记为直出），并确保有一个不带中转的网关。
// 返回网关与出口节点的 ID，调用方接着用它们登记槽位；登记失败时应把出口节点删掉。
//
// 直出的出口就是这个节点本身：它被固定下来，不会自动换成订阅里的别的节点。
func (e *Engine) PrepareDirect(ctx context.Context, slotID, subID, name string) (gateway, landing string, err error) {
	if err := model.ValidateID("槽位", slotID); err != nil {
		return "", "", err
	}
	landing = slotID + "-exit"
	if err := e.NodeFromSub(ctx, model.Node{ID: landing, Role: model.RoleLanding, Direct: true}, subID, name); err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = e.RemoveNode(landing)
		}
	}()
	gws, err := e.st.Gateways()
	if err != nil {
		return "", "", err
	}
	used := map[string]bool{}
	taken := map[string]bool{}
	for _, g := range gws {
		g.Normalize()
		if g.ID == DirectGatewayID && g.Transit == "" {
			return g.ID, landing, nil
		}
		used[g.Pool], taken[g.ID] = true, true
	}
	gateway = DirectGatewayID
	for i := 2; taken[gateway]; i++ { // 这个名字被一个带中转的网关占了
		gateway = fmt.Sprintf("%s-%d", DirectGatewayID, i)
	}
	pool := ""
	for i := 249; i >= 200; i-- {
		if p := fmt.Sprintf("10.213.%d.0/24", i); !used[p] {
			pool = p
			break
		}
	}
	if pool == "" {
		return "", "", errors.New("没有空闲的地址池可以给直出网关；先用 gateway add --pool 手动建一个不带中转的网关")
	}
	if err := e.AddGateway(model.Gateway{ID: gateway, Pool: pool}); err != nil {
		return "", "", err
	}
	return gateway, landing, nil
}

// NodeFromSub 把订阅里名为 name 的节点登记为节点 n（n 的 ID、角色等已填好，片段由订阅提供）。
func (e *Engine) NodeFromSub(ctx context.Context, n model.Node, subID, name string) error {
	sub, err := e.st.Sub(subID)
	if err != nil {
		return err
	}
	p, ok := sub.Find(name)
	if !ok {
		return fmt.Errorf("订阅 %s 里没有名为 %q 的节点；用 sub nodes %s 查看", subID, name, subID)
	}
	if len(n.Proxy) != 0 {
		return errors.New("取自订阅的节点不能再另外指定片段")
	}
	n.Proxy = p.Proxy
	n.Source = &model.NodeSource{Sub: subID, Name: name}
	return e.AddNode(ctx, n)
}
