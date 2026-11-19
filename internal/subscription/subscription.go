// Package subscription 拉取并解析 Clash 格式的订阅。
//
// 做法与 Claude-Hydra 相同（https://github.com/jin-yu-liu/Claude-Hydra，MIT 许可，
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

// Parse 从 Clash YAML 里取出 proxies 列表。缺少名称、类型、服务器或端口的条目被跳过；
// 重名的条目只保留第一个。
func Parse(body []byte) ([]model.SubProxy, error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("订阅内容不是合法的 YAML（需要 Clash 格式的订阅）")
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
		for _, k := range droppedKeys {
			delete(clean, k)
		}
		n := model.Node{Proxy: clean}
		if t, _ := clean["type"].(string); t == "" || n.Server() == "" || n.Port() < 1 || n.Port() > 65535 {
			continue
		}
		seen[name] = true
		out = append(out, model.SubProxy{Name: name, Proxy: clean})
	}
	if len(out) == 0 {
		return nil, errors.New("订阅里没有可用的节点（需要 Clash 格式、带 proxies 列表的订阅）")
	}
	return out, nil
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
