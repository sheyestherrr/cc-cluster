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
// 节点在配置里依次叫 n0、n1……（返回值与入参一一对应），不使用订阅里的名称：
// 订阅里的名称可以重复，也可能含有控制接口路径里不便出现的字符。
func RenderDelayProbe(proxies []map[string]any) (config []byte, names []string, err error) {
	names = make([]string, len(proxies))
	list := make([]map[string]any, len(proxies))
	for i, p := range proxies {
		names[i] = fmt.Sprintf("n%d", i)
		item := make(map[string]any, len(p)+1)
		for k, v := range p {
			item[k] = v
		}
		item["name"] = names[i]
		list[i] = item
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
