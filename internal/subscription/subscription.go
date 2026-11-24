// Package subscription 拉取并解析订阅：Clash 格式，以及 base64 订阅和分享链接（见 links.go）。
//
// Clash 格式的做法与 Claude-Hydra 相同（https://github.com/jin-yu-liu/Claude-Hydra，MIT 许可，
// app/src-tauri/src/clash.rs 的 clash_subscription 与 app/src/lib/clashNodes.ts）：
// 以 clash.meta 的 User-Agent 请求订阅地址，取回 Clash YAML，读其中的 proxies 列表。
// 这里是按同样的思路用 Go 重新实现的，没有复制其代码。
//
// 拉取订阅是由宿主直接访问订阅服务器，与任何 Clash 客户端一样；它不经过任何槽位的出口。
package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

const (
	// UserAgent 让订阅服务器返回 Clash（Mihomo）格式的内容。
	UserAgent = "clash.meta"
	// MaxBytes 是订阅内容的大小上限。
	MaxBytes = 4 << 20
	// Timeout 是一次拉取的时限。
	Timeout = 20 * time.Second
)

// 由管理器控制、不从订阅里带进来的字段。
var droppedKeys = []string{"name", "udp", "dialer-proxy", "interface-name", "routing-mark"}

// 订阅服务常把套餐信息做成节点塞在列表里，名称形如"剩余流量：267 GB""套餐到期：2026-11-23"。
var infoNameRe = regexp.MustCompile(`(?i)剩余流量|下次重置|套餐到期|到期时间|过期时间|traffic|expire`)

// IsInfoName 报告一个节点名称是否像这类提示项。它们往往也能连通，但不是给人用的线路，
// 自动挑选节点时跳过；手工仍然可以选。
func IsInfoName(name string) bool { return infoNameRe.MatchString(name) }

// CheckURL 检查订阅地址的写法。
func CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("订阅地址无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("订阅地址须为 HTTP 或 HTTPS")
	}
	return nil
}

// Fetch 拉取订阅内容。错误信息里不带订阅地址（地址里通常有令牌）。
func Fetch(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if err := CheckURL(raw); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(raw), nil)
	if err != nil {
		return nil, errors.New("订阅地址无效")
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("订阅下载超时")
		}
		return nil, errors.New("无法连接订阅服务器，请检查地址和网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("订阅服务器返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, errors.New("读取订阅内容失败")
	}
	if len(body) > MaxBytes {
		return nil, errors.New("订阅超过 4 MB")
	}
	return body, nil
}

// 订阅内容的格式。
const (
	FormatClash = "clash" // Clash（Mihomo）的 YAML 或 JSON，带 proxies 列表
	FormatLinks = "links" // 一行一个的分享链接，或者把它们整体做了 base64 的“通用订阅”
)

// Report 是一次解析的结果。
type Report struct {
	Proxies []model.SubProxy
	Format  string
	// Skipped 是没能解析的行号（从 1 起；base64 订阅是解码之后的行号）。只有分享链接才有：
	// 协议不认识、写法不对，或者用了 Mihomo 不支持的传输方式。
	Skipped []int
}

var errUnknownFormat = errors.New("认不出订阅内容的格式：支持 Clash 格式（带 proxies 列表）、base64 订阅，和一行一个的分享链接（ss、vmess、vless、trojan、hysteria2、socks5、http）")

// Parse 解析订阅内容，只返回节点。
func Parse(body []byte) ([]model.SubProxy, error) {
	rep, err := ParseReport(body)
	return rep.Proxies, err
}

// ParseReport 解析订阅内容：依次试 Clash 格式、分享链接，都不是时再把整段当 base64 解开试一次。
func ParseReport(body []byte) (Report, error) {
	text := normalizeText(body)
	if text == "" {
		return Report{}, errors.New("订阅内容是空的")
	}
	rep, ok, err := parseText(text)
	if err == nil && !ok {
		decoded, isBase64 := decodeWhole(text)
		if !isBase64 {
			return Report{}, errUnknownFormat
		}
		if rep, ok, err = parseText(normalizeText([]byte(decoded))); err == nil && !ok {
			return Report{}, errUnknownFormat
		}
	}
	if err != nil {
		return Report{}, err
	}
	if len(rep.Proxies) == 0 {
		if rep.Format == FormatLinks {
			return Report{}, fmt.Errorf("没有能用的节点：%d 行都没能解析（协议不支持或写法不对）", len(rep.Skipped))
		}
		return Report{}, errors.New("订阅里没有可用的节点（proxies 列表是空的，或者条目缺少名称、类型、服务器、端口）")
	}
	return rep, nil
}

// Canonical 返回适合保存的本地订阅内容：统一换行、去掉首尾空白；整段是 base64 的解开存明文，
// 这样以后往后面追加分享链接时内容仍然能解析。
func Canonical(content string) string {
	text := normalizeText([]byte(content))
	if text == "" {
		return ""
	}
	if _, ok, _ := parseText(text); !ok {
		if decoded, isBase64 := decodeWhole(text); isBase64 {
			return normalizeText([]byte(decoded))
		}
	}
	return text
}

// LooksLikeSubURL 报告贴进来的内容是不是只有一个普通网址（多半是把订阅地址贴错了地方）。
// 写明端口、不带路径的 http(s) 地址是代理的分享链接，不算。
func LooksLikeSubURL(content string) bool {
	text := normalizeText([]byte(content))
	lower := strings.ToLower(text)
	if strings.ContainsAny(text, "\n ") || !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	_, _, ok := parseProxyURL(text)
	return !ok
}

// normalizeText 去掉 BOM、统一换行并去掉首尾空白。
func normalizeText(body []byte) string {
	text := strings.TrimPrefix(string(body), "\xEF\xBB\xBF")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.TrimSpace(text)
}

// parseText 试着把一段文本当成 Clash 格式或分享链接来解析；recognized 为 false 表示两种都不是。
func parseText(text string) (rep Report, recognized bool, err error) {
	if looksLikeClash(text) || text[0] == '{' {
		proxies, err := parseClash(text)
		return Report{Proxies: proxies, Format: FormatClash}, true, err
	}
	proxies, skipped, ok := parseLinks(text)
	return Report{Proxies: proxies, Format: FormatLinks, Skipped: skipped}, ok, nil
}

func looksLikeClash(text string) bool {
	for _, key := range []string{"proxies:", "proxy-groups:", "proxy-providers:"} {
		if strings.HasPrefix(text, key) || strings.Contains(text, "\n"+key) {
			return true
		}
	}
	return false
}

// parseClash 从 Clash 的 YAML（或 JSON）里取出 proxies 列表。缺少名称、类型、服务器或端口的条目被跳过；
// 重名的条目只保留第一个。
func parseClash(text string) ([]model.SubProxy, error) {
	var doc struct {
		Proxies   []map[string]any `yaml:"proxies"`
		Outbounds []any            `yaml:"outbounds"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, errors.New("订阅内容看起来是 Clash 格式，但不是合法的 YAML")
	}
	if doc.Proxies == nil && doc.Outbounds != nil {
		return nil, errors.New("这是 sing-box 格式的配置，暂不支持；请换成 Clash 格式或通用（base64）格式的订阅")
	}
	seen := map[string]bool{}
	var out []model.SubProxy
	for _, raw := range doc.Proxies {
		name, _ := raw["name"].(string)
		name = strings.TrimSpace(name)
		clean, ok := plain(raw).(map[string]any)
		if name == "" || seen[name] || !ok {
			continue
		}
		if clean, ok = cleanProxy(clean); !ok {
			continue
		}
		seen[name] = true
		out = append(out, model.SubProxy{Name: name, Proxy: clean})
	}
	return out, nil
}

// cleanProxy 去掉由管理器控制的字段，并确认片段有类型、服务器和合法的端口。
func cleanProxy(p map[string]any) (map[string]any, bool) {
	for _, k := range droppedKeys {
		delete(p, k)
	}
	n := model.Node{Proxy: p}
	if t, _ := p["type"].(string); t == "" || n.Server() == "" || n.Port() < 1 || n.Port() > 65535 {
		return nil, false
	}
	return p, true
}

// plain 把 YAML 解出的值整理成能存成 JSON 的形状：映射的键必须是字符串，否则该值作废（返回 nil）。
func plain(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = plain(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			ks, ok := k.(string)
			if !ok {
				return nil
			}
			out[ks] = plain(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = plain(e)
		}
		return out
	}
	return v
}
