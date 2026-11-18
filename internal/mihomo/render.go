// Package mihomo 由期望配置生成网关核心（Mihomo）的配置。
//
// 生成的配置是 JSON 文本；JSON 是 YAML 的子集，核心可以直接读取，
// 这样管理器不需要引入 YAML 库。
package mihomo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// 网关容器内的固定路径。
const (
	HomeDir    = "/var/lib/ccgw"
	ConfigPath = HomeDir + "/config.yaml"
	SocketPath = HomeDir + "/mihomo.sock"
)

// TransitName 是中转在核心配置里的代理名。
const TransitName = "transit"

// LandingName 返回落地节点在核心配置里的代理名。
func LandingName(nodeID string) string { return "land-" + nodeID }

// Binding 是"某个槽的来源网段 → 某个落地"的一条绑定。
type Binding struct {
	SlotID  string
	Link    netplan.Link
	Landing model.Node
}

// Input 是生成一个网关配置所需的全部期望状态。
type Input struct {
	Gateway  model.Gateway
	Transit  *model.Node // 为 nil 表示单跳
	Bindings []Binding
}

// Output 是生成结果。
type Output struct {
	// Config 是核心配置文本，含节点凭据，不得写入日志。
	Config []byte
	// Digest 是 Config 的 SHA-256。
	Digest string
	// FirstHops 是网关主路由表里唯一允许直连的地址，已排序去重。
	FirstHops []netip.Addr
}

// 不允许 worker 经落地访问的目的网段：私网、回环、链路本地等。
// 这些地址在落地一侧指向落地自己的内网，与槽位的用途无关。
var rejectedDestinations = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
}

// Render 生成网关的核心配置。
func Render(in Input) (Output, error) {
	gw := in.Gateway
	if in.Transit == nil && len(in.Bindings) > 1 {
		return Output{}, fmt.Errorf("网关 %s 没有中转，只能接一个槽（专用网关）；现有 %d 个", gw.ID, len(in.Bindings))
	}
	if in.Transit != nil && in.Transit.Role != model.RoleTransit {
		return Output{}, fmt.Errorf("节点 %s 的角色不是中转", in.Transit.ID)
	}

	bindings := append([]Binding(nil), in.Bindings...)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Link.Index < bindings[j].Link.Index })

	hosts := map[string]string{}
	hops := map[netip.Addr]bool{}
	var proxies []map[string]any
	dnsVia := ""

	if t := in.Transit; t != nil {
		hop, err := t.FirstHopIP()
		if err != nil {
			return Output{}, err
		}
		hops[hop] = true
		if t.ServerIsDomain() {
			hosts[t.Server()] = hop.String()
		}
		proxies = append(proxies, proxyEntry(*t, TransitName, ""))
		dnsVia = TransitName
	}

	seenLanding := map[string]bool{}
	seenIndex := map[int]string{}
	var listeners []map[string]any
	var srcRules []string
	for _, b := range bindings {
		if other, dup := seenIndex[b.Link.Index]; dup {
			return Output{}, fmt.Errorf("槽位 %s 与 %s 占用了同一个网段 %s", b.SlotID, other, b.Link.Subnet)
		}
		seenIndex[b.Link.Index] = b.SlotID
		if b.Landing.Role != model.RoleLanding {
			return Output{}, fmt.Errorf("槽位 %s 引用的节点 %s 不是落地", b.SlotID, b.Landing.ID)
		}
		name := LandingName(b.Landing.ID)
		if !seenLanding[b.Landing.ID] {
			seenLanding[b.Landing.ID] = true
			dialer := ""
			if in.Transit != nil {
				dialer = TransitName
				if b.Landing.ConnectIPv4 != "" && b.Landing.ServerIsDomain() {
					hosts[b.Landing.Server()] = b.Landing.ConnectIPv4
				}
			} else {
				hop, err := b.Landing.FirstHopIP()
				if err != nil {
					return Output{}, err
				}
				hops[hop] = true
				if b.Landing.ServerIsDomain() {
					hosts[b.Landing.Server()] = hop.String()
				}
				dnsVia = name
			}
			proxies = append(proxies, proxyEntry(b.Landing, name, dialer))
		}
		listeners = append(listeners, map[string]any{
			"name":   fmt.Sprintf("probe-%d", b.Link.Index),
			"type":   "mixed",
			"listen": "127.0.0.1",
			"port":   b.Link.ProbePort(),
			"proxy":  name,
		})
		srcRules = append(srcRules, fmt.Sprintf("SRC-IP-CIDR,%s,%s", b.Link.Subnet, name))
	}

	// 核心自己的解析走中转（共享网关）或本槽落地（专用网关）。
	// 两者都没有时不加后缀：主路由表里没有通往 DNS 服务器的路由，解析只会失败，不会泄漏。
	nameservers := make([]string, 0, len(gw.DNS))
	for _, d := range gw.DNS {
		if dnsVia != "" {
			d += "#" + dnsVia
		}
		nameservers = append(nameservers, d)
	}

	rules := []string{"NETWORK,udp,REJECT"} // 首版不转发业务 UDP；DNS 已在 TUN 层被接管
	for _, cidr := range rejectedDestinations {
		rules = append(rules, "IP-CIDR,"+cidr+",REJECT,no-resolve")
	}
	rules = append(rules, srcRules...)
	rules = append(rules, "MATCH,REJECT") // 未登记的来源一律拒绝

	logLevel := gw.LogLevel
	if logLevel == "" {
		logLevel = model.DefaultLogLevel
	}
	cfg := map[string]any{
		"mode":                     "rule",
		"ipv6":                     false,
		"allow-lan":                false,
		"log-level":                logLevel,
		"find-process-mode":        "off",
		"geo-auto-update":          false,
		"external-controller-unix": SocketPath,
		"profile":                  map[string]any{"store-selected": false, "store-fake-ip": false},
		"tun": map[string]any{
			"enable":                true,
			"device":                netplan.TUNDevice,
			"stack":                 "gvisor",
			"auto-route":            false, // 路由由监督进程和管理器设置
			"auto-detect-interface": false,
			"dns-hijack":            []string{"any:53", "tcp://any:53"},
		},
		"sniffer": map[string]any{
			"enable": true,
			"sniff": map[string]any{
				"HTTP": map[string]any{"ports": []int{80}},
				"TLS":  map[string]any{"ports": []int{443}},
			},
		},
		"dns": map[string]any{
			"enable":           true,
			"ipv6":             false,
			"enhanced-mode":    "redir-host",
			"use-hosts":        true,
			"use-system-hosts": false,
			"nameserver":       nameservers,
		},
		"hosts":     hosts,
		"proxies":   orEmpty(proxies),
		"listeners": orEmpty(listeners),
		"rules":     rules,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return Output{}, err
	}
	sum := sha256.Sum256(buf.Bytes())

	out := Output{Config: buf.Bytes(), Digest: hex.EncodeToString(sum[:])}
	for h := range hops {
		out.FirstHops = append(out.FirstHops, h)
	}
	sort.Slice(out.FirstHops, func(i, j int) bool { return out.FirstHops[i].Less(out.FirstHops[j]) })
	return out, nil
}

// proxyEntry 把节点片段变成核心配置里的一个 proxy：名字、udp 和拨号方式由管理器决定。
func proxyEntry(n model.Node, name, dialer string) map[string]any {
	p := make(map[string]any, len(n.Proxy)+3)
	for k, v := range n.Proxy {
		p[k] = v
	}
	for _, k := range []string{"dialer-proxy", "interface-name", "routing-mark"} {
		delete(p, k)
	}
	p["name"] = name
	p["udp"] = false
	if dialer != "" {
		p["dialer-proxy"] = dialer
	}
	return p
}

func orEmpty(v []map[string]any) []map[string]any {
	if v == nil {
		return []map[string]any{}
	}
	return v
}

// 预览时可以原样显示的 proxy 字段；其余字段（口令、UUID、密钥等）一律隐藏。
var visibleProxyKeys = map[string]bool{
	"name": true, "type": true, "server": true, "port": true, "udp": true,
	"dialer-proxy": true, "tls": true, "network": true,
}

// Redact 返回隐藏了节点凭据的配置文本，用于预览和排查。
func Redact(config []byte) ([]byte, error) {
	var cfg map[string]any
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	proxies, ok := cfg["proxies"].([]any)
	if !ok {
		return nil, errors.New("配置里没有 proxies 列表")
	}
	for _, p := range proxies {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		for k := range m {
			if !visibleProxyKeys[k] {
				m[k] = "<已隐藏>"
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
