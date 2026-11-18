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
)

// DefaultEchoURLs 是出口 IP 探测地址，逐个尝试，取第一个返回 IPv4 的结果。
var DefaultEchoURLs = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
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
	// ConnectIPv4 在 server 是域名且该节点作首跳时必填，写入核心的 hosts 与网关的首跳路由。
	ConnectIPv4 string `json:"connect_ipv4,omitempty"`
	// ExpectExitIP 是落地的预期出口地址；填了就必须与实测一致。
	ExpectExitIP string `json:"expect_exit_ip,omitempty"`
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
	Image         string   `json:"image,omitempty"`
	LogLevel      string   `json:"log_level,omitempty"`
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
}

// Identity 是槽位的稳定身份，首次生成后不再改动。
type Identity struct {
	MachineID string    `json:"machine_id"`
	Hostname  string    `json:"hostname"`
	MAC       string    `json:"mac"`
	CreatedAt time.Time `json:"created_at"`
}

// Applied 记录一次成功应用的结果。
type Applied struct {
	Digest      string    `json:"digest"`
	WorkerID    string    `json:"worker_container"`
	GatewayID   string    `json:"gateway_container"`
	ExitIP      string    `json:"exit_ip"`
	AppliedAt   time.Time `json:"applied_at"`
	EchoURLUsed string    `json:"echo_url,omitempty"`
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
