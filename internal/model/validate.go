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
// server 是域名时必须由用户给出 connect_ipv4，管理器不在宿主上替它解析。
func (n Node) FirstHopIP() (netip.Addr, error) {
	if n.ConnectIPv4 != "" {
		return netip.ParseAddr(n.ConnectIPv4)
	}
	a, err := netip.ParseAddr(n.Server())
	if err != nil {
		return netip.Addr{}, fmt.Errorf("节点 %s 的 server 是域名，作首跳时需要用 --connect-ipv4 指定它的地址", n.ID)
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
	for _, e := range g.EchoURLs {
		u, err := url.Parse(e)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("网关 %s 的出口探测地址 %q 不合法", g.ID, e)
		}
		if strings.ContainsAny(e, " '\"\\$`") {
			return fmt.Errorf("网关 %s 的出口探测地址 %q 含有不允许的字符", g.ID, e)
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

// Normalize 给未填的字段补默认值。
func (s *Slot) Normalize() {
	if s.Shell == "" {
		s.Shell = DefaultShell
	}
	if s.PidsLimit == 0 {
		s.PidsLimit = DefaultPidsLimit
	}
	if s.Lang == "" {
		s.Lang = "C.UTF-8"
	}
}

// worker 内由管理器占用的路径，不允许被用户挂载覆盖。
var reservedTargets = []string{"/", "/proc", "/sys", "/dev", "/etc", "/etc/machine-id", "/etc/passwd", "/etc/group", "/etc/hostname", "/etc/hosts", "/etc/resolv.conf", WorkerHome}

// WorkerHome 是 worker 内的 HOME 路径。
const WorkerHome = "/home/dev"

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
	if !path.IsAbs(s.Shell) {
		return fmt.Errorf("槽位 %s 的 shell 必须是绝对路径", s.ID)
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
		t := path.Clean(m.Target)
		if !path.IsAbs(m.Target) || t != m.Target {
			return fmt.Errorf("槽位 %s 的挂载目标 %q 必须是规范的绝对路径", s.ID, m.Target)
		}
		for _, r := range reservedTargets {
			if t == r {
				return fmt.Errorf("槽位 %s 的挂载目标 %q 由管理器占用", s.ID, m.Target)
			}
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
		return err
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
