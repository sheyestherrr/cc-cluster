package engine

import (
	"sort"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 历史趋势：把 history/ 下的记录按时间分档汇总，给界面画图用。

// TrafficPoint 是一档里的流量：Up / Down 是这一档里的字节，Conns 是同时连接数的峰值。
type TrafficPoint struct {
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
	Conns int   `json:"conns"`
}

// HistoryPoint 是一档：全部槽位的合计，和各槽位各自的。T 是这一档开始的时间。
type HistoryPoint struct {
	T time.Time `json:"t"`
	TrafficPoint
	Slots map[string]TrafficPoint `json:"slots,omitempty"`
}

// WatchSpan 是持续检测里状态没有变的一段。
type WatchSpan struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Status string    `json:"status"`
	ExitIP string    `json:"exit_ip,omitempty"`
	// Checks 是这一段里检测过几次。
	Checks int `json:"checks"`
}

// SubPoint 是一次测延迟之后订阅里测通的个数。
type SubPoint struct {
	T      time.Time `json:"t"`
	Usable int       `json:"usable"`
	Total  int       `json:"total"`
}

// History 是一段时间里的趋势。
type History struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Step 是每一档的秒数。
	Step    int                     `json:"step"`
	Traffic []HistoryPoint          `json:"traffic"`
	Totals  map[string]TrafficPoint `json:"totals"`
	Watch   map[string][]WatchSpan  `json:"watch"`
	Subs    map[string][]SubPoint   `json:"subs"`
}

// historyStep 按时间跨度选一档多长：一天以内 5 分钟，一周以内 1 小时，再长 6 小时。
func historyStep(span time.Duration) time.Duration {
	switch {
	case span <= 24*time.Hour:
		return HistoryBucket
	case span <= 7*24*time.Hour:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}

// watchGap 是两次检测之间最长隔多久还算连着的一段；超过就当作中间没有在检测。
const watchGap = 20 * time.Minute

// History 汇总 now 之前 span 这么长时间里的历史记录。
func (e *Engine) History(now time.Time, span time.Duration) (History, error) {
	if span <= 0 || span > HistoryKeep {
		span = 24 * time.Hour
	}
	step := historyStep(span)
	to := now.UTC()
	from := to.Add(-span).Truncate(step)
	recs, err := e.st.Metrics(from, to)
	if err != nil {
		return History{}, err
	}
	return buildHistory(from, to, step, recs), nil
}

func buildHistory(from, to time.Time, step time.Duration, recs []model.Metric) History {
	n := int(to.Sub(from)/step) + 1
	type cell struct{ up, down float64 }
	total := make([]cell, n)
	peaks := make([]map[string]int, n)
	bySlot := map[string][]cell{}
	h := History{From: from, To: to, Step: int(step / time.Second), Totals: map[string]TrafficPoint{}, Watch: map[string][]WatchSpan{}, Subs: map[string][]SubPoint{}}

	sort.SliceStable(recs, func(i, j int) bool { return recs[i].T.Before(recs[j].T) })
	for _, r := range recs {
		switch r.Kind {
		case model.MetricTraffic:
			// 这条记录覆盖 [T-Span, T]：按与各档重叠的时间把字节摊进去。
			end := r.T
			start := end.Add(-time.Duration(r.Span) * time.Second)
			if r.Span <= 0 {
				start = end.Add(-time.Second)
			}
			length := end.Sub(start).Seconds()
			cells := bySlot[r.Slot]
			if cells == nil {
				cells = make([]cell, n)
				bySlot[r.Slot] = cells
			}
			first := int(start.Sub(from) / step)
			if first < 0 {
				first = 0
			}
			for i := first; i < n; i++ {
				b0 := from.Add(time.Duration(i) * step)
				b1 := b0.Add(step)
				if !b0.Before(end) {
					break
				}
				lo, hi := start, end
				if b0.After(lo) {
					lo = b0
				}
				if b1.Before(hi) {
					hi = b1
				}
				share := hi.Sub(lo).Seconds() / length
				if share <= 0 {
					continue
				}
				cells[i].up += float64(r.Up) * share
				cells[i].down += float64(r.Down) * share
				total[i].up += float64(r.Up) * share
				total[i].down += float64(r.Down) * share
				if r.Conns > 0 {
					if peaks[i] == nil {
						peaks[i] = map[string]int{}
					}
					if r.Conns > peaks[i][r.Slot] {
						peaks[i][r.Slot] = r.Conns
					}
				}
			}
		case model.MetricWatch:
			spans := h.Watch[r.Slot]
			if k := len(spans) - 1; k >= 0 && spans[k].Status == r.Status && spans[k].ExitIP == r.ExitIP && r.T.Sub(spans[k].To) <= watchGap {
				spans[k].To = r.T
				spans[k].Checks++
			} else {
				// 状态变了：上一段延伸到这一刻（中间没有断档的话），新的一段从这里开始。
				if k >= 0 && r.T.Sub(spans[k].To) <= watchGap {
					spans[k].To = r.T
				}
				spans = append(spans, WatchSpan{From: r.T, To: r.T, Status: r.Status, ExitIP: r.ExitIP, Checks: 1})
			}
			h.Watch[r.Slot] = spans
		case model.MetricSub:
			h.Subs[r.Sub] = append(h.Subs[r.Sub], SubPoint{T: r.T, Usable: r.Usable, Total: r.Total})
		}
	}

	h.Traffic = make([]HistoryPoint, n)
	for i := range h.Traffic {
		p := HistoryPoint{T: from.Add(time.Duration(i) * step)}
		p.Up, p.Down = int64(total[i].up+0.5), int64(total[i].down+0.5)
		for slot, cells := range bySlot {
			c := cells[i]
			peak := peaks[i][slot]
			if c.up+c.down < 0.5 && peak == 0 {
				continue
			}
			if p.Slots == nil {
				p.Slots = map[string]TrafficPoint{}
			}
			p.Slots[slot] = TrafficPoint{Up: int64(c.up + 0.5), Down: int64(c.down + 0.5), Conns: peak}
			p.Conns += peak
		}
		h.Traffic[i] = p
	}
	for slot, cells := range bySlot {
		var t TrafficPoint
		var up, down float64
		for i, c := range cells {
			up += c.up
			down += c.down
			if peaks[i][slot] > t.Conns {
				t.Conns = peaks[i][slot]
			}
		}
		t.Up, t.Down = int64(up+0.5), int64(down+0.5)
		h.Totals[slot] = t
	}
	return h
}

// HostStat 是一个槽位在一段时间里对一个目标的连接合计。
type HostStat struct {
	Host  string    `json:"host"`
	Count int       `json:"count"`
	Up    int64     `json:"up"`
	Down  int64     `json:"down"`
	Last  time.Time `json:"last"`
}

// ConnLog 是一个槽位的连接历史：最近结束的若干条（新的在前），和这段时间里按流量排的目标。
type ConnLog struct {
	From    time.Time          `json:"from"`
	To      time.Time          `json:"to"`
	Total   int                `json:"total"`
	Records []model.ConnRecord `json:"records"`
	Hosts   []HostStat         `json:"hosts"`
}

// ConnLog 读出一个槽位在 now 之前 span 这么长时间里结束的连接；limit 是最多返回几条明细和几个目标。
func (e *Engine) ConnLog(slot string, now time.Time, span time.Duration, limit int) (ConnLog, error) {
	if _, err := e.st.Slot(slot); err != nil {
		return ConnLog{}, err
	}
	if span <= 0 || span > HistoryKeep {
		span = 24 * time.Hour
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	to := now.UTC()
	from := to.Add(-span)
	recs, err := e.st.Conns(slot, from, to)
	if err != nil {
		return ConnLog{}, err
	}
	return buildConnLog(from, to, recs, limit), nil
}

func buildConnLog(from, to time.Time, recs []model.ConnRecord, limit int) ConnLog {
	out := ConnLog{From: from, To: to, Total: len(recs), Records: []model.ConnRecord{}, Hosts: []HostStat{}}
	byHost := map[string]*HostStat{}
	for _, r := range recs {
		h := byHost[r.Host]
		if h == nil {
			h = &HostStat{Host: r.Host}
			byHost[r.Host] = h
		}
		h.Count++
		h.Up += r.Up
		h.Down += r.Down
		if r.End.After(h.Last) {
			h.Last = r.End
		}
	}
	for _, h := range byHost {
		out.Hosts = append(out.Hosts, *h)
	}
	sort.Slice(out.Hosts, func(i, j int) bool {
		a, b := out.Hosts[i], out.Hosts[j]
		if a.Up+a.Down != b.Up+b.Down {
			return a.Up+a.Down > b.Up+b.Down
		}
		return a.Host < b.Host
	})
	if len(out.Hosts) > limit {
		out.Hosts = out.Hosts[:limit]
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].End.After(recs[j].End) })
	if len(recs) > limit {
		recs = recs[:limit]
	}
	out.Records = append(out.Records, recs...)
	return out
}

// recordMetric 追加一条历史记录；写不进去不影响操作本身。
func (e *Engine) recordMetric(m model.Metric) {
	m.T = m.T.UTC()
	_ = e.st.AppendMetrics([]model.Metric{m})
}
