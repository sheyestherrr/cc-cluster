package model

import "time"

// 历史记录的种类。
const (
	MetricTraffic = "traffic"
	MetricWatch   = "watch"
	MetricSub     = "sub"
)

// Metric 是历史趋势里的一条记录，按天追加在数据目录的 history/ 下。
// 三种记录共用一个结构，各用各的字段：
//   - traffic：一个槽位在 [T-Span, T] 这一段里的上下行字节，和这一段里同时连接数的峰值；
//   - watch：一轮持续检测对一个槽位的结果；
//   - sub：一次测延迟之后订阅里测通的个数。
type Metric struct {
	T    time.Time `json:"t"`
	Kind string    `json:"k"`
	Slot string    `json:"slot,omitempty"`
	Sub  string    `json:"sub,omitempty"`

	// Span 是这条流量记录覆盖的秒数。采样进程没开的那段时间，流量在重新开始采样时一次记下，Span 就是那段空档的长度。
	Span  int   `json:"span,omitempty"`
	Up    int64 `json:"up,omitempty"`
	Down  int64 `json:"down,omitempty"`
	Conns int   `json:"conns,omitempty"`

	Status string `json:"status,omitempty"`
	ExitIP string `json:"exit_ip,omitempty"`

	Usable int `json:"usable,omitempty"`
	Total  int `json:"total,omitempty"`
}

// ConnRecord 是一条已经结束的连接：槽位连过哪个目标、多少流量、多久。
// 它来自对网关核心的定时快照：Up / Down 是最后一次看到时的累计值，End 是发现它不在了的时间，
// 存在时间比采样间隔还短的连接可能一次都没被看到。看不到 HTTPS 的路径和内容。
type ConnRecord struct {
	Slot  string    `json:"slot"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Net 是 tcp 或 udp；Host 是域名，没有时是目标地址。
	Net  string `json:"net,omitempty"`
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
	Up   int64  `json:"up,omitempty"`
	Down int64  `json:"down,omitempty"`
	// Chain 是走的链路，从靠近槽位的一跳到出口，用 → 连接。
	Chain string `json:"chain,omitempty"`
}

// Counter 是采样循环上一次落盘时一个槽位网卡的累计计数：进程重启后靠它算出没在采样的那段时间里走了多少流量。
type Counter struct {
	At   time.Time `json:"at"`
	Up   int64     `json:"up"`
	Down int64     `json:"down"`
}
