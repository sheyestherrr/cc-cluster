package engine

import (
	"context"
	"encoding/json"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/mihomo"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// 各槽位的流量与连接。两个来源，都在网关容器里读：
//   - 流量：每个槽位在网关里有自己的一端网卡，内核的累计计数是精确的，不会漏掉很短的连接；
//     重新接线会重建网卡，计数从 0 开始。
//   - 连接：核心控制接口的当前连接快照，靠来源地址落在哪个槽位的子网归到槽位。
//     已经关闭的连接不在快照里，所以连接历史靠定时快照拼出来，比采样间隔还短的连接可能看不到。
//
// 看得到的是连接这一层（目标域名与端口、流量、时长、链路），看不到 HTTPS 的路径和内容。

const (
	// DefaultSampleInterval 是采样的默认间隔。
	DefaultSampleInterval = 10 * time.Second
	// HistoryBucket 是流量历史落盘的粒度。
	HistoryBucket = 5 * time.Minute
	// HistoryKeep 是历史记录保留的时间。
	HistoryKeep = 30 * 24 * time.Hour
)

// Conn 是一条当前的连接。
type Conn struct {
	ID string `json:"id"`
	// Net 是 tcp 或 udp；Host 是域名，没有时是目标地址。
	Net   string    `json:"net"`
	Host  string    `json:"host"`
	Port  int       `json:"port"`
	Up    int64     `json:"up"`
	Down  int64     `json:"down"`
	Start time.Time `json:"start"`
	// Chain 是走的链路，从靠近槽位的一跳到出口，用 → 连接。
	Chain string `json:"chain,omitempty"`
}

// SlotSample 是一次采样里一个已接线槽位的读数。
type SlotSample struct {
	Slot    string
	Gateway string
	// Up / Down 是网卡自接线以来的累计字节：槽位发出的和收到的。
	Up, Down int64
	// HasConns 为假表示这次没能读到核心的连接快照（核心没就绪），Conns 不可用。
	HasConns bool
	Conns    []Conn
}

// 一次 exec 读完：先是各槽位网卡的计数（每行“网卡名 rx tx”），分隔行之后是核心的连接快照。
// 网关一侧的 rx 是槽位发出的，tx 是槽位收到的。
const trafficScript = `for d in /sys/class/net/cs*; do [ -r "$d/statistics/rx_bytes" ] && echo "${d##*/} $(cat "$d/statistics/rx_bytes") $(cat "$d/statistics/tx_bytes")"; done; echo ---; curl -sS -m 4 --unix-socket ` + mihomo.SocketPath + ` http://localhost/connections 2>/dev/null || true`

// SampleTraffic 读一遍各运行中网关里已接线槽位的流量计数与当前连接。
// 没在运行的网关和没接线的槽位不在结果里。
func (e *Engine) SampleTraffic(ctx context.Context) ([]SlotSample, error) {
	gws, err := e.st.Gateways()
	if err != nil {
		return nil, err
	}
	slots, err := e.st.Slots()
	if err != nil {
		return nil, err
	}
	var out []SlotSample
	for _, gw := range gws {
		pool, err := netip.ParsePrefix(gw.Pool)
		if err != nil {
			continue
		}
		byIf := map[string]string{}
		nets := map[string]netip.Prefix{}
		for _, sl := range slots {
			if sl.Gateway != gw.ID {
				continue
			}
			subnet, err := netip.ParsePrefix(sl.Subnet)
			if err != nil {
				continue
			}
			link, err := netplan.Locate(pool, subnet)
			if err != nil {
				continue
			}
			byIf[link.GatewayIf()] = sl.ID
			nets[sl.ID] = subnet
		}
		if len(byIf) == 0 {
			continue
		}
		name := e.GatewayContainer(gw.ID)
		c, ok, err := e.inspect(ctx, name)
		if err != nil {
			return out, err
		}
		if !ok || !c.State.Running {
			continue
		}
		raw, err := e.sh(ctx, name, trafficScript)
		if err != nil {
			continue
		}
		out = append(out, parseTraffic(raw, gw.ID, byIf, nets)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out, nil
}

// flexInt 接受 JSON 里写成数字或带引号的数字（核心把端口写成字符串）。
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	n, err := strconv.Atoi(strings.Trim(string(b), `"`))
	if err == nil {
		*f = flexInt(n)
	}
	return nil
}

type coreConnections struct {
	Connections []struct {
		ID       string `json:"id"`
		Metadata struct {
			Network         string  `json:"network"`
			SourceIP        string  `json:"sourceIP"`
			DestinationIP   string  `json:"destinationIP"`
			DestinationPort flexInt `json:"destinationPort"`
			Host            string  `json:"host"`
			SniffHost       string  `json:"sniffHost"`
		} `json:"metadata"`
		Upload   int64     `json:"upload"`
		Download int64     `json:"download"`
		Start    time.Time `json:"start"`
		Chains   []string  `json:"chains"`
	} `json:"connections"`
}

// parseTraffic 解析 trafficScript 的输出。byIf 是网卡名到槽位，nets 是槽位到它的子网。
func parseTraffic(raw, gateway string, byIf map[string]string, nets map[string]netip.Prefix) []SlotSample {
	counters, snapshot, _ := strings.Cut(raw, "\n---\n")
	if strings.HasPrefix(raw, "---\n") { // 一块网卡都没有
		counters, snapshot = "", strings.TrimPrefix(raw, "---\n")
	}
	samples := map[string]*SlotSample{}
	for _, line := range strings.Split(counters, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		slot, ok := byIf[f[0]]
		if !ok {
			continue
		}
		rx, err1 := strconv.ParseInt(f[1], 10, 64)
		tx, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		samples[slot] = &SlotSample{Slot: slot, Gateway: gateway, Up: rx, Down: tx}
	}

	var core coreConnections
	hasConns := json.Unmarshal([]byte(strings.TrimSpace(snapshot)), &core) == nil && strings.TrimSpace(snapshot) != ""
	for _, c := range core.Connections {
		src, err := netip.ParseAddr(c.Metadata.SourceIP)
		if err != nil {
			continue
		}
		for slot, pfx := range nets {
			s := samples[slot]
			if s == nil || !pfx.Contains(src) {
				continue
			}
			host := c.Metadata.Host
			if host == "" {
				host = c.Metadata.SniffHost
			}
			if host == "" {
				host = c.Metadata.DestinationIP
			}
			s.Conns = append(s.Conns, Conn{
				ID: c.ID, Net: c.Metadata.Network, Host: host, Port: int(c.Metadata.DestinationPort),
				Up: c.Upload, Down: c.Download, Start: c.Start.UTC(), Chain: chainText(c.Chains),
			})
			break
		}
	}
	out := make([]SlotSample, 0, len(samples))
	for _, s := range samples {
		s.HasConns = hasConns
		sort.Slice(s.Conns, func(i, j int) bool {
			if !s.Conns[i].Start.Equal(s.Conns[j].Start) {
				return s.Conns[i].Start.After(s.Conns[j].Start)
			}
			return s.Conns[i].ID < s.Conns[j].ID
		})
		out = append(out, *s)
	}
	return out
}

// chainText 把核心给出的链路（从出口一侧往回列）转成从槽位一侧到出口的顺序，并去掉配置里的名字前缀。
func chainText(chains []string) string {
	parts := make([]string, 0, len(chains))
	for i := len(chains) - 1; i >= 0; i-- {
		parts = append(parts, strings.TrimPrefix(chains[i], "land-"))
	}
	return strings.Join(parts, " → ")
}

// ---- 采样循环在内存里的状态 ----

// historyStore 是采样循环用到的存储操作（*store.Store 满足它）。
type historyStore interface {
	LockSampler() (unlock func(), ok bool, err error)
	Counters() (map[string]model.Counter, error)
	SaveCounters(map[string]model.Counter) error
	AppendMetrics([]model.Metric) error
	AppendConns([]model.ConnRecord) error
	PruneHistory(before time.Time) (int, error)
}

// SlotLive 是一个槽位此刻的流量。
type SlotLive struct {
	// UpBps / DownBps 是最近两次采样之间的平均速率（字节每秒）。
	UpBps   float64 `json:"up_bps"`
	DownBps float64 `json:"down_bps"`
	// Up / Down 是网卡自接线以来的累计字节。
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
	Conns int   `json:"conns"`
}

// TrafficSnapshot 是各槽位此刻的流量。At 是最近一次采样的时间；Recording 表示这个进程在写历史。
type TrafficSnapshot struct {
	At        time.Time           `json:"at"`
	Recording bool                `json:"recording"`
	Slots     map[string]SlotLive `json:"slots"`
}

type slotState struct {
	seen     bool // 这个进程采到过它
	live     bool // 最近一次采样里有它
	at       time.Time
	up, down int64
	upBps    float64
	downBps  float64
	conns    []Conn
	open     map[string]Conn

	// 还没落盘的这一段。
	since          time.Time
	accUp, accDown int64
	peak           int
}

// Traffic 是采样循环在内存里的状态：各槽位此刻的速率与连接，以及还没落盘的那一段流量。
// 一个进程一份；同一个数据目录上有几个进程在采样时，只有拿到互斥的那个写历史。
type Traffic struct {
	mu        sync.Mutex
	at        time.Time
	slots     map[string]*slotState
	recording bool
	unlock    func()
	base      map[string]model.Counter
	pruned    time.Time
}

// NewTraffic 返回空的采样状态。
func NewTraffic() *Traffic {
	return &Traffic{slots: map[string]*slotState{}, base: map[string]model.Counter{}}
}

// Snapshot 返回各槽位此刻的流量。
func (t *Traffic) Snapshot() TrafficSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := TrafficSnapshot{At: t.at, Recording: t.recording, Slots: map[string]SlotLive{}}
	for id, s := range t.slots {
		if !s.live {
			continue
		}
		out.Slots[id] = SlotLive{UpBps: s.upBps, DownBps: s.downBps, Up: s.up, Down: s.down, Conns: len(s.conns)}
	}
	return out
}

// Conns 返回一个槽位当前的连接，新的在前。
func (t *Traffic) Conns(slot string) []Conn {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.slots[slot]
	if s == nil || !s.live {
		return []Conn{}
	}
	return append([]Conn{}, s.conns...)
}

func delta(cur, prev int64) int64 {
	if cur >= prev {
		return cur - prev
	}
	return cur // 计数变小：网卡重建过，从 0 算起
}

// observe 记下一次采样，并把到了时间的那一段流量、已经结束的连接写进历史。
func (t *Traffic) observe(now time.Time, samples []SlotSample, st historyStore) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.recording && st != nil {
		if unlock, ok, err := st.LockSampler(); err == nil && ok {
			t.recording, t.unlock = true, unlock
			if base, err := st.Counters(); err == nil {
				t.base = base
			}
		}
	}

	var metrics []model.Metric
	var ended []model.ConnRecord
	closeConns := func(slot string, s *slotState, still map[string]Conn) {
		for id, c := range s.open {
			if _, ok := still[id]; ok {
				continue
			}
			ended = append(ended, model.ConnRecord{Slot: slot, Start: c.Start, End: now.UTC(), Net: c.Net, Host: c.Host, Port: c.Port, Up: c.Up, Down: c.Down, Chain: c.Chain})
		}
		s.open = still
	}

	present := map[string]bool{}
	for _, sm := range samples {
		present[sm.Slot] = true
		s := t.slots[sm.Slot]
		if s == nil {
			s = &slotState{open: map[string]Conn{}}
			t.slots[sm.Slot] = s
		}
		var dUp, dDown int64
		if s.seen {
			dUp, dDown = delta(sm.Up, s.up), delta(sm.Down, s.down)
			if dt := now.Sub(s.at).Seconds(); dt > 0 {
				s.upBps, s.downBps = float64(dUp)/dt, float64(dDown)/dt
			}
		} else if b, ok := t.base[sm.Slot]; ok && t.recording {
			// 这个进程第一次看到它：没在采样的那一段里走的流量，按上次落盘时的位置补一条，Span 是空档的长度。
			gapUp, gapDown := sm.Up, sm.Down
			if sm.Up >= b.Up && sm.Down >= b.Down {
				gapUp, gapDown = sm.Up-b.Up, sm.Down-b.Down
			}
			if span := int(now.Sub(b.At).Seconds()); gapUp+gapDown > 0 && span > 0 {
				metrics = append(metrics, model.Metric{T: now.UTC(), Kind: model.MetricTraffic, Slot: sm.Slot, Span: span, Up: gapUp, Down: gapDown})
			}
			t.base[sm.Slot] = model.Counter{At: now.UTC(), Up: sm.Up, Down: sm.Down}
		}
		// 既没采到过也没有落盘的位置：从现在的值算起，之前的不计（不知道是什么时候走的）。
		s.seen, s.live, s.at, s.up, s.down = true, true, now, sm.Up, sm.Down
		if s.since.IsZero() {
			s.since = now
		}
		s.accUp += dUp
		s.accDown += dDown
		if sm.HasConns {
			s.conns = sm.Conns
			still := make(map[string]Conn, len(sm.Conns))
			for _, c := range sm.Conns {
				still[c.ID] = c
			}
			closeConns(sm.Slot, s, still)
		}
		if n := len(s.conns); n > s.peak {
			s.peak = n
		}
	}
	// 这次没采到的槽位：撤线、停掉或网关不在了，它的连接也就结束了。
	for id, s := range t.slots {
		if present[id] || !s.live {
			continue
		}
		s.live, s.upBps, s.downBps, s.conns = false, 0, 0, nil
		closeConns(id, s, map[string]Conn{})
	}
	t.at = now

	metrics = append(metrics, t.flush(now, false)...)
	t.write(now, st, metrics, ended)
}

// flush 取出到了时间（force 时是全部）的那一段流量，并把落盘的位置挪到现在。
func (t *Traffic) flush(now time.Time, force bool) []model.Metric {
	var out []model.Metric
	for id, s := range t.slots {
		if s.since.IsZero() || (!force && now.Sub(s.since) < HistoryBucket) {
			continue
		}
		if span := int(now.Sub(s.since).Seconds()); span > 0 && (s.accUp+s.accDown > 0 || s.peak > 0) {
			out = append(out, model.Metric{T: now.UTC(), Kind: model.MetricTraffic, Slot: id, Span: span, Up: s.accUp, Down: s.accDown, Conns: s.peak})
		}
		s.accUp, s.accDown, s.peak = 0, 0, len(s.conns)
		s.since = now
		if !s.live {
			s.since = time.Time{}
		}
		t.base[id] = model.Counter{At: now.UTC(), Up: s.up, Down: s.down}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

func (t *Traffic) write(now time.Time, st historyStore, metrics []model.Metric, ended []model.ConnRecord) {
	if !t.recording || st == nil {
		return
	}
	if len(metrics) > 0 {
		_ = st.AppendMetrics(metrics)
		_ = st.SaveCounters(t.base)
	}
	if len(ended) > 0 {
		sort.Slice(ended, func(i, j int) bool { return ended[i].Start.Before(ended[j].Start) })
		_ = st.AppendConns(ended)
	}
	if now.Sub(t.pruned) > time.Hour {
		t.pruned = now
		_, _ = st.PruneHistory(now.Add(-HistoryKeep))
	}
}

// close 在采样循环结束时把还没落盘的写下去，并交还互斥。还开着的连接不记：它们没有结束。
func (t *Traffic) close(now time.Time, st historyStore) {
	t.mu.Lock()
	defer t.mu.Unlock()
	metrics := t.flush(now, true)
	if t.recording && st != nil {
		if len(metrics) > 0 {
			_ = st.AppendMetrics(metrics)
		}
		_ = st.SaveCounters(t.base)
	}
	if t.unlock != nil {
		t.unlock()
		t.unlock, t.recording = nil, false
	}
}

// SampleInto 采样一次并记进 t：更新各槽位此刻的速率与连接，把到了时间的那一段流量和已经结束的连接写进历史。
func (e *Engine) SampleInto(ctx context.Context, t *Traffic) error {
	samples, err := e.SampleTraffic(ctx)
	if err != nil {
		return err
	}
	t.observe(time.Now(), samples, e.st)
	return nil
}

// SampleLoop 每隔 interval 采样一次，直到 ctx 结束。open 打开数据目录与 Docker；
// 打开之后一直沿用（采样很频繁，不值得每次重连），出错时下一轮重新打开。
func SampleLoop(ctx context.Context, t *Traffic, interval time.Duration, open func(context.Context) (*Engine, error)) {
	if interval <= 0 {
		interval = DefaultSampleInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var eng *Engine
	var st historyStore
	for {
		// 一轮采样出错（包括崩溃）时丢开这个连接，下一轮重新打开。
		if !guard("流量采样", func() {
			if eng == nil {
				if e, err := open(ctx); err == nil {
					eng, st = e, e.st
				}
			}
			if eng != nil {
				rctx, cancel := context.WithTimeout(ctx, interval)
				defer cancel()
				if err := eng.SampleInto(rctx, t); err != nil {
					eng = nil
				}
			}
		}) {
			eng = nil
		}
		select {
		case <-ctx.Done():
			if st != nil {
				t.close(time.Now(), st)
			}
			return
		case <-tick.C:
		}
	}
}
