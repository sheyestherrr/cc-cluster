// Package model 定义 cc-cluster 的期望配置对象及其校验。
//
// 这些对象原样保存在数据目录里；"待应用"由期望配置的摘要与已应用摘要比较得出，
// 不单独存储。
package model

import "time"

// 节点角色。
const (
	RoleTransit = "transit" // 中转：共享网关的共同首跳
	RoleLanding = "landing" // 落地：槽位的出口
)

// 各对象的默认值。
const (
	DefaultClusterName   = "cc"
	DefaultGatewayImage  = "cc-cluster-gateway:dev"
	DefaultWorkerImage   = "cc-cluster-worker:dev"
	DefaultEgressNetwork = "cc-egress"
	DefaultPool          = "10.213.250.0/24"
	DefaultDNS           = "https://1.1.1.1/dns-query"
	DefaultShell         = "/bin/bash"
	DefaultLogLevel      = "warning"
	DefaultPidsLimit     = 4096
	// MaxSlotsPerLanding 是同一个落地最多同时接线的槽位数。
	MaxSlotsPerLanding = 5
	// DefaultLang 在槽位和落地都没有指定语言时使用；默认的 worker 镜像里生成了这个 locale。
	DefaultLang = "en_US.UTF-8"
	// LegacyUser 是早期版本里所有槽共用的用户名；没有记录用户名的旧身份继续用它，
	// 这样已有 HOME 里保存的路径不会失效。
	LegacyUser = "dev"
)

// DefaultEchoURLs 是出口探测地址，逐个尝试，取第一个给出 IPv4 的结果。
// 排在最前的 ipinfo.io 返回 JSON，除出口 IP 外还带所在时区、国家和运营商；
// 落地没有设置时区时就用它给出的时区。其余几个只返回 IP，作为后备。
var DefaultEchoURLs = []string{
	"https://ipinfo.io/json",
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

// DefaultConnectURLs 是检查链路连通性时访问的地址：应答只有一个状态码，各地都能访问。
// 出口探测之前先用它们分段检查，链路不通时可以指出是中转还是落地这一段的问题。
var DefaultConnectURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://cp.cloudflare.com/generate_204",
}

// Cluster 是数据目录级别的设置。
type Cluster struct {
	// Name 是容器名前缀，用来区分同一台机器上的多个数据目录。
	Name         string `json:"name"`
	GatewayImage string `json:"gateway_image"`
	WorkerImage  string `json:"worker_image"`
}

// Node 是一个代理节点：Mihomo 原生 proxy 片段加上角色。
type Node struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	// Proxy 是 Mihomo 原生 proxy 片段，不含 name；由管理器控制的字段不允许自带。
	Proxy map[string]any `json:"proxy"`
	// ConnectIPv4 是该节点作首跳时网关直连的地址，写入核心的 hosts 与网关的首跳路由。
	// server 是域名时需要它：可以手工指定，否则由管理器在宿主上解析一次并记在这里。
	ConnectIPv4 string `json:"connect_ipv4,omitempty"`
	// ConnectAuto 表示 ConnectIPv4 是管理器解析出来的，可以用 node resolve 重新解析。
	ConnectAuto bool `json:"connect_auto,omitempty"`
	// Source 记录该节点取自哪个订阅的哪一项；刷新订阅时据此更新片段。
	Source *NodeSource `json:"source,omitempty"`
	// ExpectExitIP 是落地的预期出口地址；填了就必须与实测一致。
	ExpectExitIP string `json:"expect_exit_ip,omitempty"`
	// TZ 与 Lang 是落地所在地区的时区和语言；绑定它的槽位没有自己指定时沿用，
	// 让槽内的时区与出口地区一致。
	TZ   string `json:"tz,omitempty"`
	Lang string `json:"lang,omitempty"`
	// TZAuto 表示 TZ 是首次启动槽位时按出口探测结果自动设置的。
	TZAuto bool `json:"tz_auto,omitempty"`
}

// NodeSource 指向订阅里的一个节点。
type NodeSource struct {
	Sub  string `json:"sub"`
	Name string `json:"name"`
}

// Subscription 是一个 Clash 格式的订阅及其最近一次拉取的节点列表。
// 订阅地址里通常带有令牌，节点片段里有凭据，整个对象按机密保存。
type Subscription struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	// TestedAt 是最近一次测延迟的时间；没测过时为零值。
	TestedAt time.Time  `json:"tested_at"`
	Proxies  []SubProxy `json:"proxies"`
}

// DelayFailed 是 SubProxy.DelayMS 里表示"测过但不通"的值。
const DelayFailed = -1

// SubProxy 是订阅里的一个节点：名称加 Mihomo 原生 proxy 片段（不含 name）。
type SubProxy struct {
	Name  string         `json:"name"`
	Proxy map[string]any `json:"proxy"`
	// DelayMS 是最近一次测得的延迟（毫秒）；0 表示没测过，DelayFailed 表示不通。
	DelayMS int `json:"delay_ms,omitempty"`
}

// Find 按名称查找订阅里的节点。
func (s Subscription) Find(name string) (SubProxy, bool) {
	for _, p := range s.Proxies {
		if p.Name == name {
			return p, true
		}
	}
	return SubProxy{}, false
}

// Gateway 是一个网关容器的期望配置。
type Gateway struct {
	ID string `json:"id"`
	// Transit 是中转节点 ID；为空表示单跳（直连落地），此时该网关只能接一个槽。
	Transit       string   `json:"transit,omitempty"`
	EgressNetwork string   `json:"egress_network"`
	Pool          string   `json:"pool"`
	DNS           []string `json:"dns"`
	EchoURLs      []string `json:"echo_urls"`
	// ConnectURLs 是连通性检查访问的地址；不填用 DefaultConnectURLs。
	ConnectURLs []string `json:"connect_urls,omitempty"`
	Image       string   `json:"image,omitempty"`
	LogLevel    string   `json:"log_level,omitempty"`
}

// ConnectivityURLs 返回连通性检查要访问的地址。
func (g Gateway) ConnectivityURLs() []string {
	if len(g.ConnectURLs) == 0 {
		return DefaultConnectURLs
	}
	return g.ConnectURLs
}

// Mount 是 worker 的一个 bind 挂载。
type Mount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Slot 是一个工作槽位的期望配置。
type Slot struct {
	ID      string `json:"id"`
	Gateway string `json:"gateway"`
	Landing string `json:"landing"`
	// Subnet 是该槽在网关地址池里的 /30，创建时分配，之后不变。
	Subnet      string            `json:"subnet"`
	Image       string            `json:"image,omitempty"`
	Shell       string            `json:"shell"`
	UID         int               `json:"uid"`
	GID         int               `json:"gid"`
	Mounts      []Mount           `json:"mounts,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	TZ          string            `json:"tz,omitempty"`
	Lang        string            `json:"lang,omitempty"`
	MemoryBytes int64             `json:"memory_bytes,omitempty"`
	CPUs        float64           `json:"cpus,omitempty"`
	PidsLimit   int64             `json:"pids_limit,omitempty"`
	// FakeDMI 用该槽身份里的机型信息覆盖 DMI 目录；默认关闭，此时 DMI 目录被遮成空的。
	FakeDMI bool `json:"fake_dmi,omitempty"`
	// FakeProc 让 /proc/cpuinfo 与 /proc/meminfo 按该槽的 CPU / 内存限额显示；默认关闭。
	// 覆盖后的内容是创建容器时生成的静态文件，不随实际用量变化。
	FakeProc bool `json:"fake_proc,omitempty"`
}

// Identity 是槽位的稳定身份，首次生成后不再改动。
type Identity struct {
	MachineID string `json:"machine_id"`
	Hostname  string `json:"hostname"`
	MAC       string `json:"mac"`
	// User 是 worker 内的用户名，HOME 为 /home/<User>。为空表示早期版本生成的身份。
	User string `json:"user,omitempty"`
	// DMI 是该槽的机型信息（sysfs 的 DMI 字段名 → 值），只在槽位开启 FakeDMI 时使用。
	DMI       map[string]string `json:"dmi,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// UserName 返回 worker 内的用户名。
func (i Identity) UserName() string {
	if i.User == "" {
		return LegacyUser
	}
	return i.User
}

// Home 返回 worker 内的 HOME 路径。
func (i Identity) Home() string { return "/home/" + i.UserName() }

// Applied 记录一次成功应用的结果。
type Applied struct {
	Digest      string    `json:"digest"`
	WorkerID    string    `json:"worker_container"`
	GatewayID   string    `json:"gateway_container"`
	ExitIP      string    `json:"exit_ip"`
	AppliedAt   time.Time `json:"applied_at"`
	EchoURLUsed string    `json:"echo_url,omitempty"`
	// 以下三项来自返回 JSON 的探测地址，只用于显示。
	ExitCountry string `json:"exit_country,omitempty"`
	ExitOrg     string `json:"exit_org,omitempty"`
	ExitTZ      string `json:"exit_tz,omitempty"`
}

// Hold 是手动断网的持久标记；存在时拒绝接线。
type Hold struct {
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// Op 是操作记录的一行，不含密钥。
type Op struct {
	Time   time.Time `json:"time"`
	Op     string    `json:"op"`
	Target string    `json:"target,omitempty"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	Detail string    `json:"detail,omitempty"`
}
