package mihomo

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DelayGroup 是测延迟用的配置里包含全部待测节点的代理组名。
const DelayGroup = "all"

// DelayConfigPath 是测延迟用的配置在容器内的路径。
const DelayConfigPath = HomeDir + "/delay.yaml"

// RenderDelayProbe 生成只用于测延迟的核心配置：没有 TUN，没有任何监听入口，
// 规则全部拒绝，只留一个本机的控制接口用来发起测速。
// 节点在配置里依次叫 n0、n1……，不使用订阅里的名称：
// 订阅里的名称可以重复，也可能含有控制接口路径里不便出现的字符。
//
// via 不为空时测的是整条链路：每个节点后面接上这个落地（c0、c1……，经对应的 n 拨号），
// 这样能发现"节点自己能用，但连不上落地"的情况。
// 返回的 names 是要测的代理名，与入参一一对应。
func RenderDelayProbe(proxies []map[string]any, via map[string]any) (config []byte, names []string, err error) {
	names = make([]string, len(proxies))
	list := make([]map[string]any, 0, 2*len(proxies))
	entry := func(src map[string]any, name, dialer string) map[string]any {
		item := make(map[string]any, len(src)+2)
		for k, v := range src {
			item[k] = v
		}
		item["name"] = name
		delete(item, "dialer-proxy")
		if dialer != "" {
			item["dialer-proxy"] = dialer
		}
		return item
	}
	for i, p := range proxies {
		hop := fmt.Sprintf("n%d", i)
		list = append(list, entry(p, hop, ""))
		names[i] = hop
		if via != nil {
			names[i] = fmt.Sprintf("c%d", i)
			list = append(list, entry(via, names[i], hop))
		}
	}
	cfg := map[string]any{
		"mode":                     "rule",
		"ipv6":                     false,
		"allow-lan":                false,
		"log-level":                "error",
		"find-process-mode":        "off",
		"geo-auto-update":          false,
		"external-controller-unix": SocketPath,
		"profile":                  map[string]any{"store-selected": false, "store-fake-ip": false},
		"proxies":                  list,
		"proxy-groups":             []map[string]any{{"name": DelayGroup, "type": "select", "proxies": names}},
		"rules":                    []string{"MATCH,REJECT"},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), names, nil
}
