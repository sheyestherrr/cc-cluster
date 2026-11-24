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

// DefaultEchoURLs 是出口探测地址，逐个尝试。
// 前三个返回 JSON，除出口 IP 外还带所在时区、国家和运营商；落地没有设置时区时就用它们
// 给出的时区。多人共用的出口上 ipinfo.io 常被限流，所以后面还有两个同类的来源。
// 最后一个是 Cloudflare 的 trace：基本不限流，给出 IP 和国家代码但没有时区，
// 前三个都失败时靠它让槽位仍能启动，时区用 FallbackTZ。
var DefaultEchoURLs = []string{
	"https://ipinfo.io/json",
	"https://ipwho.is/",
	"https://ipapi.co/json/",
	"https://www.cloudflare.com/cdn-cgi/trace",
}

// FallbackTZ 是所有探测地址都给不出时区、槽位和落地也都没有指定时用的时区。
// 它只是兜底：记在落地上时带 TZFallback 标记，之后探测到真实时区就换掉。
const FallbackTZ = "America/Los_Angeles"

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
	// NoExitReport 关掉启动槽位时顺带进行的出口评估查询（它经该槽的链路访问第三方接口）。
	// 手动的重新查询不受影响。
	NoExitReport bool `json:"no_exit_report,omitempty"`
}

// Node 是一个代理节点：Mihomo 原生 proxy 片段加上角色。
type Node struct {
	ID string `json:"id"`
	// Note 是给人看的备注（中文、下划线、空格都可以），只用于展示。
	Note string `json:"note,omitempty"`
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
	// Direct 表示这是“直出”的出口：订阅里的一个节点直接当出口用，后面没有专门的落地。
	// 它是多人共用的出口；被固定给一个槽位，不会自动换成别的节点。
	Direct bool `json:"direct,omitempty"`
	// TZ 与 Lang 是落地所在地区的时区和语言；绑定它的槽位没有自己指定时沿用，
	// 让槽内的时区与出口地区一致。
	TZ   string `json:"tz,omitempty"`
	Lang string `json:"lang,omitempty"`
	// TZAuto 表示 TZ 是首次启动槽位时按出口探测结果自动设置的。
	TZAuto bool `json:"tz_auto,omitempty"`
	// TZFallback 表示 TZ 是探测不到时区时填的兜底值（FallbackTZ），之后探测到真实时区就换掉。
	TZFallback bool `json:"tz_fallback,omitempty"`
	// ExitReport 是第三方对这个落地出口 IP 的评估，只用于展示，不参与任何判断。
	ExitReport *ExitReport `json:"exit_report,omitempty"`
}

// ExitReport 是第三方对一个出口 IP 的评估：所在地、所属网络，以及各家数据是否把它
// 标成机房、代理、滥用来源等。内容来自外部接口（见 Sources），只用于展示。
type ExitReport struct {
	IP        string    `json:"ip"`
	CheckedAt time.Time `json:"checked_at"`
	// Sources 是这次取到了应答的接口。
	Sources []string `json:"sources"`

	Country     string `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
	Timezone    string `json:"timezone,omitempty"`

	ASN         int    `json:"asn,omitempty"`
	ASOrg       string `json:"as_org,omitempty"`
	ASNKind     string `json:"asn_kind,omitempty"`
	Company     string `json:"company,omitempty"`
	CompanyType string `json:"company_type,omitempty"`
	// UsageType 是 IP2Location 的用途类型代码（DCH 机房、ISP 宽带、MOB 移动网络等）。
	UsageType string `json:"usage_type,omitempty"`
	// UserType 与 ConnectionType 是 MaxMind 的用户类型（residential、business、hosting 等）和接入方式。
	UserType       string `json:"user_type,omitempty"`
	ConnectionType string `json:"connection_type,omitempty"`

	Residential bool `json:"residential"`
	Mobile      bool `json:"mobile"`

	// AbuserScore 是接口给出的原文，例如 "0.0002 (Very Low)"。
	AbuserScore string `json:"abuser_score,omitempty"`
	// TrustScore 是 ip.net.coffee 的信任分（0 到 100，越高越好）；FraudScore 是 IP2Location 的
	// 欺诈分（0 到 100，越低越好）；StaticIPScore 与 UserCount 是 MaxMind 对这个地址有多固定、
	// 背后大约有多少用户的估计。接口没有给出的为 nil。
	TrustScore    *int     `json:"trust_score,omitempty"`
	FraudScore    *int     `json:"fraud_score,omitempty"`
	StaticIPScore *float64 `json:"static_ip_score,omitempty"`
	UserCount     *int     `json:"user_count,omitempty"`

	// Flags 是至少有一家数据命中的不利标记。
	Flags []ExitFlag `json:"flags"`
}

// ExitFlag 是一项不利标记：哪几家数据命中了它，以及一共有几家对这一项给出了判断。
// 各家的判断经常不一致，所以不合并成一个"是"或"否"。
type ExitFlag struct {
	Name string   `json:"name"`
	By   []string `json:"by"`
	Of   int      `json:"of"`
}

// NodeSource 指向订阅里的一个节点。
type NodeSource struct {
	Sub  string `json:"sub"`
	Name string `json:"name"`
}

// Subscription 是一个订阅及其最近一次解析出的节点列表。来源有两种：远程订阅给的是地址（URL），
// 定期去拉取；本地订阅是把订阅内容或分享链接直接贴进来（Content），没有地址。
// 订阅地址里通常带有令牌，内容和节点片段里有凭据，整个对象按机密保存。
type Subscription struct {
	ID string `json:"id"`
	// Note 是给人看的备注，只用于展示。
	Note string `json:"note,omitempty"`
	URL  string `json:"url"`
	// Content 是本地订阅的原文。
	Content   string    `json:"content,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	// EveryMin 是自动刷新的间隔（分钟），0 表示只手动刷新；只有远程订阅用得上。
	// 自动刷新随持续检测一起做（cc-cluster web 运行期间，或 cc-cluster watch）。
	EveryMin int `json:"every_min,omitempty"`
	// CheckedAt 是最近一次尝试取订阅的时间，LastError 是那次失败的原因；成功时 CheckedAt 与 FetchedAt 相同、LastError 为空。
	CheckedAt time.Time `json:"checked_at"`
	LastError string    `json:"last_error,omitempty"`
	// Format 是最近一次解析认出的格式（clash 或 links）；Skipped 是分享链接里没能解析的行数。
	Format  string `json:"format,omitempty"`
	Skipped int    `json:"skipped,omitempty"`
	// TestedAt 是最近一次测延迟的时间；没测过时为零值。
	TestedAt time.Time  `json:"tested_at"`
	Proxies  []SubProxy `json:"proxies"`
}

// Local 报告这是不是本地订阅（内容是贴进来的，没有地址可拉取）。
func (s Subscription) Local() bool { return s.URL == "" }

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
	// Note 是给人看的备注，只用于展示。
	Note string `json:"note,omitempty"`
	// Transit 是中转节点 ID；为空表示不经中转，各槽位直连自己的落地。
	Transit string `json:"transit,omitempty"`
	// Backups 是候选中转，按顺序：主用的 Transit 不通时核心自动改用其中第一个通的，
	// 主用的恢复后切回。换中转不改变出口（出口由落地决定）。没有 Transit 时必须为空。
	Backups       []string `json:"transit_backups,omitempty"`
	EgressNetwork string   `json:"egress_network"`
	Pool          string   `json:"pool"`
	DNS           []string `json:"dns"`
	EchoURLs      []string `json:"echo_urls"`
	// ConnectURLs 是连通性检查访问的地址；不填用 DefaultConnectURLs。
	ConnectURLs []string `json:"connect_urls,omitempty"`
	Image       string   `json:"image,omitempty"`
	LogLevel    string   `json:"log_level,omitempty"`
}

// MaxTransitBackups 是一个网关的候选中转数量上限。
const MaxTransitBackups = 8

// Transits 返回全部中转：主用的在前，候选的按顺序在后。
func (g Gateway) Transits() []string {
	if g.Transit == "" {
		return nil
	}
	return append([]string{g.Transit}, g.Backups...)
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
	ID string `json:"id"`
	// Note 是给人看的备注，只用于展示；不写进容器。
	Note    string `json:"note,omitempty"`
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
	// 设了 CPU 限额时，容器还会被绑到前 N 个核上，/sys 里在线核的范围也按 N 显示。
	FakeProc bool `json:"fake_proc,omitempty"`
	// TmuxBridge 开启宿主 tmux 桥接：把该槽专用的 tmux socket 目录挂进 worker，
	// 槽内的 tmux-mcp 经它操作宿主上的 tmux。默认关闭。
	// 经桥接执行的命令使用宿主的权限和宿主网络，不受该槽的网络约束。
	TmuxBridge bool `json:"tmux_bridge,omitempty"`
	// TmuxTools 是开放给 tmux-mcp 的工具范围，写法与它的 TMUX_MCP_TOOLS 相同：
	// "deny:项,项" 或 "allow:项,项"，项是工具名或 @组名。为空表示 DefaultTmuxTools。
	TmuxTools string `json:"tmux_tools,omitempty"`
	// TmuxShell 是宿主 pane 里的 shell 类型（bash、zsh 或 fish），tmux-mcp 靠它跟踪命令的结束与退出码。
	// 开启桥接时按当时的 SHELL 记下；为空按 bash。
	TmuxShell string `json:"tmux_shell,omitempty"`
}

// DefaultTmuxTools 是桥接默认的工具范围：除了结束 session / window / pane 和踢掉客户端以外的全部。
const DefaultTmuxTools = "deny:@kill"

// EffectiveTmuxTools 返回该槽实际使用的工具范围。
func (s Slot) EffectiveTmuxTools() string {
	if s.TmuxTools == "" {
		return DefaultTmuxTools
	}
	return s.TmuxTools
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

// Watch 是持续检测对一个槽位最近一次的结果。
type Watch struct {
	Slot string    `json:"slot"`
	At   time.Time `json:"at"`
	// Since 是进入当前状态的时间。
	Since time.Time `json:"since"`
	// Status 是 ok、unreachable 或 changed。
	Status string `json:"status"`
	// Expect 是接线时核对过的出口，ExitIP 是这次看到的（连不上时为空）。
	Expect string `json:"expect,omitempty"`
	ExitIP string `json:"exit_ip,omitempty"`
	Detail string `json:"detail,omitempty"`
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
