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

// 以下两个函数是引擎与宿主网络打交道的全部入口，测试里替换成假的。
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

// resolveFirstHop 在节点的 server 是域名且没有首跳地址时，在宿主上解析一次并记进节点。
//
// 网关的主路由表只放行首跳地址，核心不能自己去解析它，所以要事先知道。解析由宿主的系统
// 解析器完成，与任何代理客户端连接节点前所做的一样。多个地址时取排序后的第一个，
// 这样重复解析得到的结果稳定，不会让网关配置无故变化。
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
	// 已有的地址仍在解析结果里就不换，避免多地址轮转的域名让配置来回变。
	for _, a := range addrs {
		if a.String() == n.ConnectIPv4 {
			return nil
		}
	}
	n.ConnectIPv4, n.ConnectAuto = addrs[0].String(), true
	return nil
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

// SubAdd 登记一个订阅并拉取一次。
func (e *Engine) SubAdd(ctx context.Context, id, url string) (sub model.Subscription, err error) {
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
	if err := subscription.CheckURL(url); err != nil {
		return sub, err
	}
	if _, err := e.st.Sub(id); err == nil {
		return sub, fmt.Errorf("订阅 %s 已存在", id)
	}
	sub = model.Subscription{ID: id, URL: url}
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
