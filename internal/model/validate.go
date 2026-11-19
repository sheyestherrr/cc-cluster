package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// ValidateID 检查对象 ID：小写字母、数字和连字符，最长 31 个字符。
// ID 会出现在容器名、文件名和核心配置里的代理名中。
func ValidateID(kind, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("%s ID %q 不合法：只能用小写字母、数字和连字符，以字母或数字开头，最长 31 个字符", kind, id)
	}
	return nil
}

// Normalize 给未填的字段补默认值。
func (c *Cluster) Normalize() {
	if c.Name == "" {
		c.Name = DefaultClusterName
	}
	if c.GatewayImage == "" {
		c.GatewayImage = DefaultGatewayImage
	}
	if c.WorkerImage == "" {
		c.WorkerImage = DefaultWorkerImage
	}
}

// Validate 检查集群设置。
func (c Cluster) Validate() error {
	if err := ValidateID("集群", c.Name); err != nil {
		return err
	}
	if c.GatewayImage == "" || c.WorkerImage == "" {
		return errors.New("镜像名不能为空")
	}
	return nil
}

// 由管理器控制、节点片段里不允许自带的字段。
var forbiddenProxyKeys = []string{"dialer-proxy", "interface-name", "routing-mark"}

// Validate 检查节点。
func (n Node) Validate() error {
	if err := ValidateID("节点", n.ID); err != nil {
		return err
	}
	if n.Role != RoleTransit && n.Role != RoleLanding {
		return fmt.Errorf("节点 %s 的角色必须是 %s 或 %s", n.ID, RoleTransit, RoleLanding)
	}
	if t, _ := n.Proxy["type"].(string); t == "" {
		return fmt.Errorf("节点 %s 的片段缺少 type", n.ID)
	}
	if n.Server() == "" {
		return fmt.Errorf("节点 %s 的片段缺少 server", n.ID)
	}
	if p := n.Port(); p < 1 || p > 65535 {
		return fmt.Errorf("节点 %s 的片段 port 不合法", n.ID)
	}
	for _, k := range forbiddenProxyKeys {
		if _, ok := n.Proxy[k]; ok {
			return fmt.Errorf("节点 %s 的片段不能自带 %s（由管理器控制）", n.ID, k)
		}
	}
	if n.ConnectIPv4 != "" {
		if err := checkIPv4(n.ConnectIPv4); err != nil {
			return fmt.Errorf("节点 %s 的 connect_ipv4：%w", n.ID, err)
		}
	}
	if n.ExpectExitIP != "" {
		if n.Role != RoleLanding {
			return fmt.Errorf("节点 %s：只有落地节点可以设置 expect_exit_ip", n.ID)
		}
		if err := checkIPv4(n.ExpectExitIP); err != nil {
			return fmt.Errorf("节点 %s 的 expect_exit_ip：%w", n.ID, err)
		}
	}
	if (n.TZ != "" || n.Lang != "") && n.Role != RoleLanding {
		return fmt.Errorf("节点 %s：只有落地节点可以设置时区和语言", n.ID)
	}
	if err := checkLocale(n.TZ, n.Lang); err != nil {
		return fmt.Errorf("节点 %s：%w", n.ID, err)
	}
	return nil
}

// Server 返回片段里的 server 字段。
func (n Node) Server() string {
	s, _ := n.Proxy["server"].(string)
	return strings.TrimSpace(s)
}

// Port 返回片段里的 port 字段；无法识别时返回 0。
func (n Node) Port() int {
	switch v := n.Proxy["port"].(type) {
	case float64:
		if v == float64(int(v)) {
			return int(v)
		}
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return 0
}

// ServerIsDomain 报告 server 是否为域名（不是 IP 字面量）。
func (n Node) ServerIsDomain() bool {
	_, err := netip.ParseAddr(n.Server())
	return err != nil
}

// FirstHopIP 返回该节点作首跳时网关要直连的 IPv4 地址。
// server 是域名时取 connect_ipv4：它由用户指定，或由管理器在登记时解析一次后记下。
func (n Node) FirstHopIP() (netip.Addr, error) {
	if n.ConnectIPv4 != "" {
		return netip.ParseAddr(n.ConnectIPv4)
	}
	a, err := netip.ParseAddr(n.Server())
	if err != nil {
		return netip.Addr{}, fmt.Errorf("节点 %s 的 server 是域名，作首跳时需要它的地址：执行 node resolve %s 解析，或用 --connect-ipv4 指定", n.ID, n.ID)
	}
	if !a.Is4() {
		return netip.Addr{}, fmt.Errorf("节点 %s 的 server 不是 IPv4 地址；首版只支持 IPv4 首跳", n.ID)
	}
	return a, nil
}

// Normalize 给未填的字段补默认值。
func (g *Gateway) Normalize() {
	if g.EgressNetwork == "" {
		g.EgressNetwork = DefaultEgressNetwork
	}
	if g.Pool == "" {
		g.Pool = DefaultPool
	}
	if len(g.DNS) == 0 {
		g.DNS = []string{DefaultDNS}
	}
	if len(g.EchoURLs) == 0 {
		g.EchoURLs = append([]string(nil), DefaultEchoURLs...)
	}
	if g.LogLevel == "" {
		g.LogLevel = DefaultLogLevel
	}
}

// Validate 检查网关。
func (g Gateway) Validate() error {
	if err := ValidateID("网关", g.ID); err != nil {
		return err
	}
	if g.Transit != "" {
		if err := ValidateID("节点", g.Transit); err != nil {
			return err
		}
	}
	if g.EgressNetwork == "" {
		return fmt.Errorf("网关 %s 缺少出口网络名", g.ID)
	}
	p, err := netip.ParsePrefix(g.Pool)
	if err != nil || !p.Addr().Is4() || p.Bits() > 30 || p != p.Masked() {
		return fmt.Errorf("网关 %s 的地址池 %q 不合法：需要一个不小于 /30 的 IPv4 网段", g.ID, g.Pool)
	}
	if !p.Addr().IsPrivate() {
		return fmt.Errorf("网关 %s 的地址池 %q 必须是私网网段", g.ID, g.Pool)
	}
	if len(g.DNS) == 0 {
		return fmt.Errorf("网关 %s 至少需要一个 DNS 服务器", g.ID)
	}
	for _, d := range g.DNS {
		if err := checkDNS(d); err != nil {
			return fmt.Errorf("网关 %s 的 DNS %q：%w", g.ID, d, err)
		}
	}
	if len(g.EchoURLs) == 0 {
		return fmt.Errorf("网关 %s 至少需要一个出口探测地址", g.ID)
	}
	for _, e := range append(append([]string(nil), g.EchoURLs...), g.ConnectURLs...) {
		u, err := url.Parse(e)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("网关 %s 的探测地址 %q 不合法", g.ID, e)
		}
		if strings.ContainsAny(e, " '\"\\$`") {
			return fmt.Errorf("网关 %s 的探测地址 %q 含有不允许的字符", g.ID, e)
		}
	}
	switch g.LogLevel {
	case "", "silent", "error", "warning", "info", "debug":
	default:
		return fmt.Errorf("网关 %s 的日志级别 %q 不合法", g.ID, g.LogLevel)
	}
	return nil
}

// checkDNS 要求 DNS 服务器写成 IP 字面量：核心在网关里没有可用的引导解析，
// 域名形式的 DoH 地址解析不出来。'#' 后缀由管理器追加，用来指定这次查询走哪条链。
func checkDNS(s string) error {
	if strings.Contains(s, "#") {
		return errors.New("不能自带 # 后缀")
	}
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "https", "tls", "tcp":
	default:
		return errors.New("只支持 https://、tls://、tcp:// 三种形式（业务 UDP 不转发）")
	}
	if _, err := netip.ParseAddr(u.Hostname()); err != nil {
		return errors.New("主机部分必须是 IP 地址")
	}
	return nil
}

// Normalize 给未填的字段补默认值。时区和语言留空表示沿用落地的设置，见 EffectiveLocale。
func (s *Slot) Normalize() {
	if s.Shell == "" {
		s.Shell = DefaultShell
	}
	if s.PidsLimit == 0 {
		s.PidsLimit = DefaultPidsLimit
	}
}

// EffectiveLocale 返回槽位实际使用的时区和语言：槽位自己的设置优先，其次是落地的，
// 语言最后落到 DefaultLang。时区可能为空，此时槽内是 UTC。
func EffectiveLocale(s Slot, landing Node) (tz, lang string) {
	tz, lang = s.TZ, s.Lang
	if tz == "" {
		tz = landing.TZ
	}
	if lang == "" {
		lang = landing.Lang
	}
	if lang == "" {
		lang = DefaultLang
	}
	return tz, lang
}

var (
	tzRe   = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+){0,2}$`)
	langRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)
)

// CheckTZ 检查一个时区名的写法；空值也不合格。
func CheckTZ(tz string) error {
	if !tzRe.MatchString(tz) {
		return fmt.Errorf("时区 %q 不合法：应形如 Asia/Tokyo", tz)
	}
	return nil
}

// checkLocale 检查时区与语言的写法；空值表示未设置。时区是否存在取决于镜像里的 tzdata。
func checkLocale(tz, lang string) error {
	if tz != "" {
		if err := CheckTZ(tz); err != nil {
			return err
		}
	}
	if lang != "" && !langRe.MatchString(lang) {
		return fmt.Errorf("语言 %q 不合法：应形如 en_US.UTF-8", lang)
	}
	return nil
}

// worker 内不允许被用户挂载覆盖的目录树：系统目录与由管理器写入身份文件的位置。
var reservedTrees = []string{"/proc", "/sys", "/dev", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/run", "/var/lib/dbus"}

// CheckMountTarget 检查一个挂载目标：规范的绝对路径，且不落在系统目录里。
// home 是该槽在 worker 内的 HOME；非空时还要求目标不是 HOME 本身或它的上级目录。
func CheckMountTarget(target, home string) error {
	t := path.Clean(target)
	if !path.IsAbs(target) || t != target {
		return fmt.Errorf("挂载目标 %q 必须是规范的绝对路径", target)
	}
	if t == "/" {
		return errors.New("不能挂载到根目录")
	}
	for _, r := range reservedTrees {
		if t == r || strings.HasPrefix(t, r+"/") {
			return fmt.Errorf("挂载目标 %q 在系统目录 %s 里", target, r)
		}
	}
	if home != "" && (t == home || strings.HasPrefix(home, t+"/")) {
		return fmt.Errorf("挂载目标 %q 会盖住该槽的 HOME（%s）", target, home)
	}
	return nil
}

// Validate 检查槽位。
func (s Slot) Validate() error {
	if err := ValidateID("槽位", s.ID); err != nil {
		return err
	}
	if err := ValidateID("网关", s.Gateway); err != nil {
		return err
	}
	if err := ValidateID("节点", s.Landing); err != nil {
		return err
	}
	p, err := netip.ParsePrefix(s.Subnet)
	if err != nil || !p.Addr().Is4() || p.Bits() != 30 || p != p.Masked() {
		return fmt.Errorf("槽位 %s 的网段 %q 不合法：需要一个 IPv4 /30", s.ID, s.Subnet)
	}
	if s.UID <= 0 || s.GID <= 0 {
		return fmt.Errorf("槽位 %s 的 UID/GID 必须是非 root 用户", s.ID)
	}
	if !path.IsAbs(s.Shell) || strings.ContainsAny(s.Shell, ":\n ") {
		return fmt.Errorf("槽位 %s 的 shell 必须是不含空白和冒号的绝对路径", s.ID)
	}
	if err := checkLocale(s.TZ, s.Lang); err != nil {
		return fmt.Errorf("槽位 %s：%w", s.ID, err)
	}
	for _, m := range s.Mounts {
		if !filepath.IsAbs(m.Source) {
			return fmt.Errorf("槽位 %s 的挂载源 %q 必须是绝对路径", s.ID, m.Source)
		}
		if filepath.Base(m.Source) == "docker.sock" {
			return fmt.Errorf("槽位 %s：不允许把 Docker socket 挂进 worker", s.ID)
		}
		if strings.ContainsAny(m.Source+m.Target, ":\n") {
			return fmt.Errorf("槽位 %s 的挂载路径不能含冒号或换行", s.ID)
		}
		if err := CheckMountTarget(m.Target, ""); err != nil {
			return fmt.Errorf("槽位 %s：%w", s.ID, err)
		}
	}
	for k := range s.Env {
		if IsProxyEnv(k) {
			return fmt.Errorf("槽位 %s：不允许设置代理环境变量 %s（流量由网关透明转发）", s.ID, k)
		}
		if k == "" || strings.ContainsAny(k, "= \n") {
			return fmt.Errorf("槽位 %s 的环境变量名 %q 不合法", s.ID, k)
		}
	}
	if s.MemoryBytes < 0 || s.CPUs < 0 || s.PidsLimit < 0 {
		return fmt.Errorf("槽位 %s 的资源限额不能为负", s.ID)
	}
	if s.FakeProc && s.CPUs == 0 && s.MemoryBytes == 0 {
		return fmt.Errorf("槽位 %s：/proc 信息按限额显示，需要先设置 CPU 或内存上限", s.ID)
	}
	return nil
}

// IsProxyEnv 报告一个环境变量名是否属于代理变量。
func IsProxyEnv(k string) bool {
	switch strings.ToLower(k) {
	case "http_proxy", "https_proxy", "all_proxy", "no_proxy", "ftp_proxy":
		return true
	}
	return false
}

func checkIPv4(s string) error {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return fmt.Errorf("%q 不是合法的 IP 地址", s)
	}
	if !a.Is4() {
		return errors.New("需要 IPv4 地址")
	}
	return nil
}

// ParseBytes 解析形如 512m、8g 的内存大小；纯数字按字节计。
func ParseBytes(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k':
		mult = 1 << 10
	case 'm':
		mult = 1 << 20
	case 'g':
		mult = 1 << 30
	}
	if mult != 1 {
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("无法解析大小 %q", s)
	}
	return int64(v * float64(mult)), nil
}
