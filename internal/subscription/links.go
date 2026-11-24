package subscription

// 分享链接（ss、vmess、vless、trojan、hysteria2、socks5、http）与 base64 订阅的识别。
//
// 识别的流程和各种链接的拆法改写自 Resin（https://github.com/Resinat/Resin，MIT 许可，
// internal/subscription/parser.go，提交 8e9f74e）：字段从哪里取、有哪些别名、各家客户端的变体
// 沿用它的做法；它输出的是 sing-box 的 outbound，这里输出 Mihomo 的 proxy 片段，所以每种协议的
// 结果都是重写的。许可全文见本目录的 NOTICE.md。
//
// 没有搬过来的部分：Surge / Quantumult X 的代理行、vmess1、ssd、Telegram 与 Netch 链接，
// 以及不带协议的 “IP:端口[:用户:密码]”（看不出是 HTTP 还是 SOCKS5，不猜）。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// linkSchemes 是认得的链接前缀。
var linkSchemes = map[string]func(string) (string, map[string]any, bool){
	"ss":        parseSS,
	"vmess":     parseVmess,
	"vless":     parseVless,
	"trojan":    parseTrojan,
	"hysteria2": parseHysteria2,
	"hy2":       parseHysteria2,
	"socks":     parseSocks,
	"socks5":    parseProxyURL,
	"socks5h":   parseProxyURL,
	"http":      parseProxyURL,
	"https":     parseProxyURL,
}

// parseLinks 逐行解析分享链接。recognized 表示至少有一行是认得的协议；skipped 是没能解析的行号（从 1 起）。
// 重名的节点在名称后面加序号，不丢弃：分享链接经常不带名称，或者几条线路同名。
func parseLinks(text string) (out []model.SubProxy, skipped []int, recognized bool) {
	seen := map[string]bool{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		scheme, _, found := strings.Cut(line, "://")
		parse := linkSchemes[strings.ToLower(scheme)]
		if !found || parse == nil {
			skipped = append(skipped, i+1)
			continue
		}
		recognized = true
		name, proxy, ok := parse(line)
		if ok {
			proxy, ok = cleanProxy(proxy)
		}
		if !ok {
			skipped = append(skipped, i+1)
			continue
		}
		for n, base := 2, name; seen[name]; n++ {
			name = fmt.Sprintf("%s %d", base, n)
		}
		seen[name] = true
		out = append(out, model.SubProxy{Name: name, Proxy: proxy})
	}
	if !recognized {
		return nil, nil, false
	}
	return out, skipped, true
}

// ---- 各种协议 ----

func parseSS(uri string) (string, map[string]any, bool) {
	before, fragment, _ := strings.Cut(strings.TrimSpace(uri[len("ss://"):]), "#")
	body, rawQuery, _ := strings.Cut(before, "?")
	body = strings.TrimSuffix(body, "/")

	var method, password, hostport string
	if at := strings.LastIndex(body, "@"); at > 0 && at < len(body)-1 {
		// SIP002：用户信息是 base64(加密方式:密码)，或者直接写明文并做百分号转义（2022 系列的加密方式）。
		var ok bool
		if method, password, ok = ssUserInfo(body[:at], true); !ok {
			return "", nil, false
		}
		hostport = body[at+1:]
	} else {
		// 旧写法：整段是 base64(加密方式:密码@地址:端口)。
		decoded, ok := decodeBase64(body)
		if !ok {
			return "", nil, false
		}
		at := strings.LastIndex(decoded, "@")
		if at <= 0 || at >= len(decoded)-1 {
			return "", nil, false
		}
		if method, password, ok = ssUserInfo(decoded[:at], false); !ok {
			return "", nil, false
		}
		hostport = decoded[at+1:]
	}
	server, port, ok := splitHostPort(hostport)
	if !ok {
		return "", nil, false
	}
	p := newProxy("ss", server, port)
	p["cipher"], p["password"] = method, password
	if !setSSPlugin(p, rawQuery) {
		return "", nil, false
	}
	return tagOr(fragment, p), p, true
}

func ssUserInfo(input string, escaped bool) (method, password string, ok bool) {
	if m, pw, found := strings.Cut(input, ":"); found {
		if escaped {
			m, pw = unescape(m), unescape(pw)
		}
		if m, pw = ssMethod(m), strings.TrimSpace(pw); m != "" && pw != "" {
			return m, pw, true
		}
	}
	decoded, ok := decodeBase64(input)
	if !ok {
		return "", "", false
	}
	m, pw, found := strings.Cut(decoded, ":")
	if m, pw = ssMethod(m), strings.TrimSpace(pw); !found || m == "" || pw == "" {
		return "", "", false
	}
	return m, pw, true
}

// ssMethod 把 AEAD_AES_128_GCM 这类写法换成通用的名称。
func ssMethod(raw string) string {
	method := strings.TrimSpace(raw)
	upper := strings.ToUpper(strings.ReplaceAll(method, "-", "_"))
	if upper == "AEAD_CHACHA20_POLY1305" {
		return "chacha20-ietf-poly1305"
	}
	if rest, ok := strings.CutPrefix(upper, "AEAD_"); ok {
		return strings.ToLower(strings.ReplaceAll(rest, "_", "-"))
	}
	return strings.ToLower(method)
}

// setSSPlugin 处理链接里的 plugin 参数（“名称;选项=值;选项”）。Mihomo 不支持的插件返回 false。
func setSSPlugin(p map[string]any, rawQuery string) bool {
	spec := rawQueryValue(rawQuery, "plugin")
	if spec == "" {
		return true
	}
	parts := splitUnescaped(spec, ';')
	name := strings.ToLower(strings.TrimSpace(parts[0]))
	opts := parts[1:]
	if len(opts) == 0 {
		if extra := rawQueryValue(rawQuery, "plugin-opts", "plugin_opts"); extra != "" {
			opts = splitUnescaped(extra, ';')
		}
	}
	values, flags := map[string]string{}, map[string]bool{}
	for _, o := range opts {
		k, v, has := strings.Cut(strings.TrimSpace(o), "=")
		if k = strings.ToLower(strings.TrimSpace(k)); k == "" {
			continue
		}
		if has {
			values[k] = strings.TrimSpace(v)
		} else {
			flags[k] = true
		}
	}
	po := map[string]any{}
	switch name {
	case "obfs", "simple-obfs", "obfs-local":
		mode := first(values["obfs"], values["mode"])
		if mode != "http" && mode != "tls" {
			return false
		}
		po["mode"] = mode
		if host := first(values["obfs-host"], values["obfs_host"], values["host"]); host != "" {
			po["host"] = host
		}
		p["plugin"] = "obfs"
	case "v2ray-plugin":
		if mode := values["mode"]; mode != "" && mode != "websocket" {
			return false
		}
		po["mode"] = "websocket"
		if flags["tls"] || values["tls"] == "true" || values["tls"] == "1" {
			po["tls"] = true
		}
		for _, k := range []string{"host", "path"} {
			if values[k] != "" {
				po[k] = values[k]
			}
		}
		p["plugin"] = "v2ray-plugin"
	case "shadow-tls":
		for _, k := range []string{"host", "password"} {
			if values[k] != "" {
				po[k] = values[k]
			}
		}
		if v, err := strconv.Atoi(values["version"]); err == nil {
			po["version"] = v
		}
		p["plugin"] = "shadow-tls"
	default:
		return false
	}
	p["plugin-opts"] = po
	return true
}

func parseVmess(uri string) (string, map[string]any, bool) {
	before, fragment, _ := strings.Cut(strings.TrimSpace(uri[len("vmess://"):]), "#")
	tag := decodeTag(fragment)

	// v2rayN：base64 包着一个 JSON 对象。
	if decoded, ok := decodeBase64(before); ok {
		var v map[string]any
		if json.Unmarshal([]byte(decoded), &v) == nil {
			return vmessFromMap(v, tag)
		}
	}
	// Shadowrocket：vmess://base64(加密方式:UUID@地址:端口)?参数
	if encoded, rawQuery, hasQuery := strings.Cut(before, "?"); hasQuery {
		if name, p, ok := vmessShadowrocket(encoded, rawQuery, tag); ok {
			return name, p, true
		}
	}
	return vmessStd(before, tag)
}

func vmessFromMap(v map[string]any, fallbackTag string) (string, map[string]any, bool) {
	server := mapString(v, "add", "server", "address")
	uuid := mapString(v, "id", "uuid", "username")
	if server == "" || uuid == "" {
		return "", nil, false
	}
	port := 443
	if n, ok := mapInt(v, "port", "server_port"); ok && n > 0 {
		port = n
	}
	p := newProxy("vmess", server, port)
	p["uuid"] = uuid
	p["alterId"], _ = mapInt(v, "aid", "alterId", "alter_id")
	p["cipher"] = first(mapString(v, "scy", "security", "cipher"), "auto")

	tlsValue := strings.ToLower(mapString(v, "tls"))
	insecure := mapBool(v, "allowInsecure", "allow_insecure", "insecure", "skip-cert-verify", "skip_cert_verify")
	alpn := splitList(mapString(v, "alpn"))
	fingerprint := mapString(v, "fp", "fingerprint", "client-fingerprint", "client_fingerprint")
	if tlsValue == "tls" || tlsValue == "1" || tlsValue == "true" || insecure || len(alpn) > 0 || fingerprint != "" {
		setTLS(p, "servername", mapString(v, "sni", "servername", "peer", "host", "ws.host"), insecure, alpn, fingerprint)
	}

	if !setTransport(p, vmessNetwork(v),
		mapString(v, "path", "wspath", "ws-path", "ws_path"),
		mapString(v, "ws.host", "wsHost", "host"),
		mapString(v, "serviceName", "service_name", "service-name", "grpc-service-name", "grpc_service_name"),
	) {
		return "", nil, false
	}
	return first(mapString(v, "ps", "name", "tag"), fallbackTag, defaultName(p)), p, true
}

// vmessNetwork 取传输方式。标准的 vmess JSON 里 type 多半是伪装头的类型（none / http），不是传输方式；
// 只有在没写 net 时才把它当传输方式看。
func vmessNetwork(v map[string]any) string {
	network := normalizeNetwork(mapString(v, "net", "network"))
	candidate := strings.ToLower(mapString(v, "type"))
	if (network == "" || network == "tcp") && candidate == "http" {
		return "http"
	}
	if network != "" {
		return network
	}
	switch normalizeNetwork(candidate) {
	case "ws", "grpc", "h2", "quic", "httpupgrade", "kcp":
		return candidate
	}
	return ""
}

func vmessShadowrocket(encoded, rawQuery, tag string) (string, map[string]any, bool) {
	decoded, ok := decodeBase64(encoded)
	if !ok {
		return "", nil, false
	}
	left, hostport, ok := strings.Cut(strings.TrimSpace(decoded), "@")
	if !ok {
		return "", nil, false
	}
	cipher, uuid, ok := strings.Cut(left, ":")
	if !ok {
		return "", nil, false
	}
	server, port, ok := splitHostPort(hostport)
	if !ok {
		return "", nil, false
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", nil, false
	}
	v := map[string]any{"add": server, "port": port, "id": strings.TrimSpace(uuid), "scy": strings.TrimSpace(cipher)}
	set := func(key string, value string) {
		if value != "" {
			v[key] = value
		}
	}
	set("aid", first(q.Get("aid"), q.Get("alterId"), q.Get("alter_id")))
	set("ps", first(tag, q.Get("remarks")))
	network := strings.ToLower(strings.TrimSpace(q.Get("network")))
	if obfs := strings.ToLower(strings.TrimSpace(q.Get("obfs"))); obfs == "websocket" || obfs == "ws" {
		network = "ws"
	}
	set("net", network)
	set("path", first(q.Get("path"), q.Get("wspath")))
	set("host", first(q.Get("obfsParam"), q.Get("wsHost"), q.Get("ws.host"), q.Get("host")))
	if queryBool(q, "tls") {
		v["tls"] = "tls"
	}
	if queryBool(q, "allowInsecure", "insecure") {
		v["allowInsecure"] = true
	}
	set("sni", first(q.Get("sni"), q.Get("peer"), q.Get("servername")))
	set("alpn", strings.TrimSpace(q.Get("alpn")))
	set("fp", first(q.Get("fp"), q.Get("fingerprint"), q.Get("client-fingerprint"), q.Get("client_fingerprint")))
	return vmessFromMap(v, "")
}

// vmessStd 解析 vmess://传输[+tls]:UUID-额外ID@地址:端口?参数 这种写法。
func vmessStd(before, tag string) (string, map[string]any, bool) {
	core, rawQuery, _ := strings.Cut(strings.ReplaceAll(before, "/?", "?"), "?")
	left, hostport, ok := strings.Cut(core, "@")
	if !ok {
		return "", nil, false
	}
	netPart, userPart, ok := strings.Cut(left, ":")
	if !ok {
		return "", nil, false
	}
	server, port, ok := splitHostPort(hostport)
	if !ok {
		return "", nil, false
	}
	userPart = strings.TrimSpace(userPart)
	split := strings.LastIndex(userPart, "-")
	if split <= 0 || split >= len(userPart)-1 {
		return "", nil, false
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", nil, false
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(netPart)), "+")
	v := map[string]any{"add": server, "port": port, "id": strings.TrimSpace(userPart[:split]), "net": first(parts[0], "tcp"), "ps": tag}
	for _, extra := range parts[1:] {
		if strings.TrimSpace(extra) == "tls" {
			v["tls"] = "tls"
		}
	}
	set := func(key string, value string) {
		if value != "" {
			v[key] = value
		}
	}
	set("aid", strings.TrimSpace(userPart[split+1:]))
	set("scy", first(q.Get("security"), q.Get("cipher")))
	set("path", strings.TrimSpace(q.Get("path")))
	set("host", first(q.Get("host"), q.Get("ws.host")))
	set("sni", first(q.Get("sni"), q.Get("peer"), q.Get("servername")))
	if queryBool(q, "allowInsecure", "insecure") {
		v["allowInsecure"] = true
	}
	set("alpn", strings.TrimSpace(q.Get("alpn")))
	set("fp", first(q.Get("fp"), q.Get("fingerprint"), q.Get("client-fingerprint"), q.Get("client_fingerprint")))
	set("serviceName", first(q.Get("serviceName"), q.Get("service_name"), q.Get("service-name"), q.Get("grpc-service-name"), q.Get("grpc_service_name")))
	return vmessFromMap(v, "")
}

func parseVless(uri string) (string, map[string]any, bool) {
	u, fragment, ok := parseURL(uri)
	if !ok || u.User == nil {
		return "", nil, false
	}
	uuid, server := strings.TrimSpace(u.User.Username()), strings.TrimSpace(u.Hostname())
	if uuid == "" || server == "" {
		return "", nil, false
	}
	q := u.Query()
	p := newProxy("vless", server, portOr(u, 443))
	p["uuid"] = uuid
	if strings.EqualFold(strings.TrimSpace(q.Get("flow")), "xtls-rprx-vision") {
		p["flow"] = "xtls-rprx-vision"
	}

	security := strings.ToLower(strings.TrimSpace(q.Get("security")))
	sni := first(q.Get("sni"), q.Get("servername"))
	insecure := queryBool(q, "allowInsecure", "insecure")
	alpn := splitList(q.Get("alpn"))
	fingerprint := first(q.Get("fp"), q.Get("fingerprint"), q.Get("client-fingerprint"), q.Get("client_fingerprint"))
	if security == "tls" || security == "reality" || sni != "" || insecure || len(alpn) > 0 || fingerprint != "" {
		if security == "reality" {
			reality := map[string]any{}
			if key := first(q.Get("pbk"), q.Get("publicKey"), q.Get("public-key"), q.Get("public_key")); key != "" {
				reality["public-key"] = key
			}
			if sid := first(q.Get("sid"), q.Get("shortId"), q.Get("short-id"), q.Get("short_id")); sid != "" {
				reality["short-id"] = sid
			}
			p["reality-opts"] = reality
			fingerprint = first(fingerprint, "chrome")
		}
		setTLS(p, "servername", sni, insecure, alpn, fingerprint)
	}

	network := strings.ToLower(first(q.Get("type"), q.Get("network")))
	if network == "" && queryBool(q, "ws") {
		network = "ws"
	}
	if (network == "" || network == "tcp") && strings.EqualFold(strings.TrimSpace(q.Get("headerType")), "http") {
		network = "http"
	}
	if !setTransport(p, network,
		first(q.Get("path"), q.Get("wspath"), q.Get("ws-path")),
		first(q.Get("host"), q.Get("ws.host"), q.Get("obfs-host")),
		first(q.Get("serviceName"), q.Get("service_name"), q.Get("service-name"), q.Get("grpc-service-name"), q.Get("grpc_service_name")),
	) {
		return "", nil, false
	}
	return tagOr(fragment, p), p, true
}

func parseTrojan(uri string) (string, map[string]any, bool) {
	u, fragment, ok := parseURL(uri)
	if !ok || u.User == nil {
		return "", nil, false
	}
	password, server := strings.TrimSpace(u.User.Username()), strings.TrimSpace(u.Hostname())
	if password == "" || server == "" {
		return "", nil, false
	}
	q := u.Query()
	p := newProxy("trojan", server, portOr(u, 443))
	p["password"] = password
	setTLS(p, "sni", first(q.Get("sni"), q.Get("peer"), q.Get("servername")), queryBool(q, "allowInsecure", "insecure"),
		splitList(q.Get("alpn")), first(q.Get("fp"), q.Get("fingerprint"), q.Get("client-fingerprint"), q.Get("client_fingerprint")))
	delete(p, "tls") // trojan 本身就是 TLS，Mihomo 的 trojan 没有 tls 这个开关

	network := strings.ToLower(first(q.Get("type"), q.Get("network")))
	if network == "" && queryBool(q, "ws") {
		network = "ws"
	}
	if !setTransport(p, network,
		first(q.Get("path"), q.Get("wspath"), q.Get("ws-path")),
		first(q.Get("host"), q.Get("ws.host"), q.Get("obfs-host")),
		first(q.Get("serviceName"), q.Get("service_name"), q.Get("service-name"), q.Get("grpc-service-name"), q.Get("grpc_service_name")),
	) {
		return "", nil, false
	}
	return tagOr(fragment, p), p, true
}

func parseHysteria2(uri string) (string, map[string]any, bool) {
	u, fragment, ok := parseURL(uri)
	if !ok {
		return "", nil, false
	}
	q := u.Query()
	password := ""
	if u.User != nil {
		password = strings.TrimSpace(u.User.Username())
		if pw, has := u.User.Password(); has {
			password = strings.TrimSpace(password + ":" + pw)
		}
	}
	password = first(password, q.Get("password"), q.Get("auth"))
	server := strings.TrimSpace(u.Hostname())
	if password == "" || server == "" {
		return "", nil, false
	}
	p := newProxy("hysteria2", server, portOr(u, 443))
	p["password"] = password
	setTLS(p, "sni", first(q.Get("sni"), q.Get("peer"), q.Get("servername")), queryBool(q, "insecure", "allowInsecure"), splitList(q.Get("alpn")), "")
	delete(p, "tls") // hysteria2 走 QUIC，总是带 TLS
	if pin := first(q.Get("pinSHA256"), q.Get("pin-sha256"), q.Get("pin_sha256")); pin != "" {
		p["fingerprint"] = strings.ToLower(strings.ReplaceAll(pin, ":", ""))
	}
	if ports := hysteriaPorts(first(q.Get("ports"), q.Get("mport"))); ports != "" {
		p["ports"] = ports
	}
	if n, err := strconv.Atoi(first(q.Get("upmbps"), q.Get("up_mbps"), q.Get("up"))); err == nil && n > 0 {
		p["up"] = fmt.Sprintf("%d Mbps", n)
	}
	if n, err := strconv.Atoi(first(q.Get("downmbps"), q.Get("down_mbps"), q.Get("down"))); err == nil && n > 0 {
		p["down"] = fmt.Sprintf("%d Mbps", n)
	}
	if obfs := strings.TrimSpace(q.Get("obfs")); obfs != "" && obfs != "none" {
		p["obfs"] = obfs
		if pw := first(q.Get("obfs-password"), q.Get("obfs_password")); pw != "" {
			p["obfs-password"] = pw
		}
	}
	return tagOr(fragment, p), p, true
}

// hysteriaPorts 规整端口跳跃的范围：逗号分隔，每项是一个端口或“起-止”。有一项写得不对就整个不要。
func hysteriaPorts(raw string) string {
	var out []string
	for _, item := range splitList(raw) {
		item = strings.ReplaceAll(strings.ReplaceAll(item, " ", ""), ":", "-")
		for _, n := range strings.Split(item, "-") {
			if v, err := strconv.Atoi(n); err != nil || v < 1 || v > 65535 {
				return ""
			}
		}
		if strings.Count(item, "-") > 1 {
			return ""
		}
		out = append(out, item)
	}
	return strings.Join(out, ",")
}

// parseSocks 解析 socks://base64(用户:密码@地址:端口)#名称；不是这种写法时按普通的地址解析。
func parseSocks(uri string) (string, map[string]any, bool) {
	before, fragment, _ := strings.Cut(strings.TrimSpace(uri[len("socks://"):]), "#")
	decoded, ok := decodeBase64(before)
	if !ok {
		return parseProxyURL(uri)
	}
	decoded = strings.TrimSpace(decoded)
	username, password, hostport := "", "", decoded
	if at := strings.LastIndex(decoded, "@"); at > 0 && at < len(decoded)-1 {
		var found bool
		if username, password, found = strings.Cut(decoded[:at], ":"); !found || strings.TrimSpace(username) == "" {
			return "", nil, false
		}
		hostport = decoded[at+1:]
	}
	server, port, ok := splitHostPort(hostport)
	if !ok {
		return "", nil, false
	}
	p := newProxy("socks5", server, port)
	setAuth(p, strings.TrimSpace(username), strings.TrimSpace(password))
	return tagOr(fragment, p), p, true
}

// parseProxyURL 解析 http://、https://、socks5:// 形式的代理地址。必须写明端口，并且不能带路径：
// 这样一行普通的网址（比如误贴进来的订阅地址）不会被当成代理。
func parseProxyURL(uri string) (string, map[string]any, bool) {
	u, fragment, ok := parseURL(uri)
	if !ok {
		return "", nil, false
	}
	server := strings.TrimSpace(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if server == "" || err != nil || (u.Path != "" && u.Path != "/") {
		return "", nil, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && u.RawQuery != "" {
		return "", nil, false
	}
	typ := "socks5"
	if scheme == "http" || scheme == "https" {
		typ = "http"
	}
	p := newProxy(typ, server, port)
	if u.User != nil {
		pw, _ := u.User.Password()
		setAuth(p, strings.TrimSpace(u.User.Username()), pw)
	}
	if scheme == "https" {
		q := u.Query()
		for key := range q {
			switch strings.ToLower(key) {
			case "sni", "servername", "peer", "allowinsecure", "insecure":
			default:
				return "", nil, false
			}
		}
		setTLS(p, "sni", first(q.Get("sni"), q.Get("servername"), q.Get("peer")), queryBool(q, "allowInsecure", "insecure"), nil, "")
	}
	return tagOr(fragment, p), p, true
}

// ---- 共用的部分 ----

func newProxy(typ, server string, port int) map[string]any {
	return map[string]any{"type": typ, "server": server, "port": port}
}

func setAuth(p map[string]any, username, password string) {
	if username != "" {
		p["username"] = username
	}
	if password != "" {
		p["password"] = password
	}
}

// setTLS 打开 TLS 并写上相关字段。sniKey 是这种协议里服务器名称的字段名（servername 或 sni）。
func setTLS(p map[string]any, sniKey, sni string, insecure bool, alpn []string, fingerprint string) {
	p["tls"] = true
	if sni != "" {
		p[sniKey] = sni
	}
	if insecure {
		p["skip-cert-verify"] = true
	}
	if len(alpn) > 0 {
		p["alpn"] = alpn
	}
	if fingerprint != "" {
		p["client-fingerprint"] = fingerprint
	}
}

func normalizeNetwork(raw string) string {
	switch network := strings.ToLower(strings.TrimSpace(raw)); network {
	case "none", "raw":
		return "tcp"
	case "websocket":
		return "ws"
	case "http-upgrade":
		return "httpupgrade"
	case "mkcp":
		return "kcp"
	default:
		return network
	}
}

// setTransport 按链接里的传输方式写 Mihomo 的 network 和对应的选项。Mihomo 不支持的传输
// （quic、kcp 等，以及 trojan 上的 h2 / http）返回 false，这个节点就不要了。
func setTransport(p map[string]any, network, path, host, service string) bool {
	network = normalizeNetwork(network)
	if p["type"] == "trojan" && (network == "h2" || network == "http") {
		return false
	}
	switch network {
	case "", "tcp":
	case "ws", "httpupgrade":
		opts := map[string]any{}
		setWSPath(opts, path)
		if host != "" {
			opts["headers"] = map[string]any{"Host": host}
		}
		if network == "httpupgrade" {
			opts["v2ray-http-upgrade"] = true
		}
		p["network"] = "ws"
		if len(opts) > 0 {
			p["ws-opts"] = opts
		}
		// WebSocket 只能走 HTTP/1.1：链接里写了别的 ALPN 时去掉，免得握手协商成 h2。
		if alpn, ok := p["alpn"].([]string); ok {
			delete(p, "alpn")
			for _, a := range alpn {
				if strings.EqualFold(a, "http/1.1") {
					p["alpn"] = []string{"http/1.1"}
				}
			}
		}
	case "grpc":
		p["network"] = "grpc"
		if name := first(service, path); name != "" {
			p["grpc-opts"] = map[string]any{"grpc-service-name": name}
		}
	case "h2":
		opts := map[string]any{}
		if path != "" {
			opts["path"] = path
		}
		if hosts := splitList(host); len(hosts) > 0 {
			opts["host"] = hosts
		}
		p["network"] = "h2"
		if len(opts) > 0 {
			p["h2-opts"] = opts
		}
	case "http":
		opts := map[string]any{}
		if path != "" {
			opts["path"] = []string{path}
		}
		if hosts := splitList(host); len(hosts) > 0 {
			opts["headers"] = map[string]any{"Host": hosts}
		}
		p["network"] = "http"
		if len(opts) > 0 {
			p["http-opts"] = opts
		}
	default:
		return false
	}
	return true
}

// setWSPath 写 WebSocket 的路径。路径形如 /path?ed=2048 时，ed 是提前数据的长度，拆出来单独写。
func setWSPath(opts map[string]any, path string) {
	if path == "" {
		return
	}
	opts["path"] = path
	base, rawQuery, has := strings.Cut(path, "?")
	if !has {
		return
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return
	}
	for key := range values {
		if k := strings.ToLower(key); k != "ed" && k != "eh" {
			return // 还带着别的参数：保持原样
		}
	}
	ed, err := strconv.Atoi(strings.TrimSpace(values.Get("ed")))
	if err != nil || ed <= 0 {
		return
	}
	opts["path"] = first(base, "/")
	opts["max-early-data"] = ed
	opts["early-data-header-name"] = first(values.Get("eh"), "Sec-WebSocket-Protocol")
}

// parseURL 解析一条链接，名称（# 后面的部分）单独拆出来：名称里常有不合规的 % 和空格，
// 交给 url.Parse 会让整条链接解析失败。
func parseURL(uri string) (*url.URL, string, bool) {
	before, fragment, _ := strings.Cut(strings.TrimSpace(uri), "#")
	u, err := url.Parse(before)
	if err != nil {
		return nil, "", false
	}
	return u, fragment, true
}

func portOr(u *url.URL, fallback int) int {
	if n, err := strconv.Atoi(u.Port()); err == nil {
		return n
	}
	return fallback
}

func decodeTag(fragment string) string { return strings.TrimSpace(unescape(fragment)) }

func defaultName(p map[string]any) string {
	return fmt.Sprintf("%v-%s", p["type"], net.JoinHostPort(fmt.Sprint(p["server"]), fmt.Sprint(p["port"])))
}

// tagOr 返回链接里的名称；没有名称时用“类型-地址:端口”。
func tagOr(fragment string, p map[string]any) string {
	return first(decodeTag(fragment), defaultName(p))
}

func unescape(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

// first 返回第一个去掉首尾空白后不为空的值。
func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func queryBool(q url.Values, keys ...string) bool {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(q.Get(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// splitList 按逗号拆开并去掉空项。
func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func splitUnescaped(input string, delimiter byte) []string {
	var parts []string
	start, escaped := 0, false
	for i := 0; i < len(input); i++ {
		switch {
		case escaped:
			escaped = false
		case input[i] == '\\':
			escaped = true
		case input[i] == delimiter:
			parts = append(parts, input[start:i])
			start = i + 1
		}
	}
	return append(parts, input[start:])
}

// rawQueryValue 从没解析过的查询串里取一个参数。不用 url.ParseQuery：插件参数里的分号会让它报错。
func rawQueryValue(rawQuery string, keys ...string) string {
	for _, pair := range strings.Split(rawQuery, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		for _, key := range keys {
			if strings.EqualFold(strings.TrimSpace(k), key) {
				if decoded, err := url.QueryUnescape(v); err == nil {
					v = decoded
				}
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func splitHostPort(hostport string) (string, int, bool) {
	hostport = strings.TrimSpace(hostport)
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil {
		i := strings.LastIndex(hostport, ":")
		if i <= 0 {
			return "", 0, false
		}
		host, portText = hostport[:i], hostport[i+1:]
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if host == "" || err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

// decodeBase64 宽松地解码：标准和 URL 两种字母表、带不带填充都接受；结果不是合法文本时算失败。
func decodeBase64(input string) (string, bool) {
	s := strings.TrimRight(strings.TrimSpace(input), "=")
	if s == "" {
		return "", false
	}
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, err := enc.DecodeString(s); err == nil && utf8.Valid(decoded) {
			return string(decoded), true
		}
	}
	return "", false
}

// decodeWhole 判断整段内容是不是一个 base64 订阅，是的话返回解码后的文本。
func decodeWhole(text string) (string, bool) {
	compact := strings.Join(strings.Fields(text), "")
	if len(compact) < 24 || len(compact)%4 == 1 {
		return "", false
	}
	for _, r := range compact {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '-' || r == '_' || r == '=':
		default:
			return "", false
		}
	}
	return decodeBase64(compact)
}

// ---- 从 vmess 的 JSON 里取值：同一个字段有的客户端写成字符串，有的写成数字 ----

func mapString(v map[string]any, keys ...string) string {
	for _, key := range keys {
		switch x := v[key].(type) {
		case string:
			if x = strings.TrimSpace(x); x != "" {
				return x
			}
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			return strconv.Itoa(x)
		}
	}
	return ""
}

func mapInt(v map[string]any, keys ...string) (int, bool) {
	if s := mapString(v, keys...); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

func mapBool(v map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch x := v[key].(type) {
		case bool:
			if x {
				return true
			}
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "1", "true", "yes", "on":
				return true
			}
		case float64:
			if x != 0 {
				return true
			}
		}
	}
	return false
}
