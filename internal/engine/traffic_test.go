package engine

import (
	"net/netip"
	"testing"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

func TestParseTraffic(t *testing.T) {
	byIf := map[string]string{"cs0": "alice", "cs1": "bob", "cs2": "carol"}
	nets := map[string]netip.Prefix{
		"alice": netip.MustParsePrefix("10.200.0.0/30"),
		"bob":   netip.MustParsePrefix("10.200.0.4/30"),
		"carol": netip.MustParsePrefix("10.200.0.8/30"),
	}
	// carol 没接线（没有网卡）；cs9 不是登记过的槽位；198.18.0.1 来的连接不属于任何槽位（管理器自己的探测）。
	raw := `cs0 1200 34000
cs1 0 0
cs9 5 5
---
{"downloadTotal":1,"uploadTotal":2,"connections":[
 {"id":"c1","metadata":{"network":"tcp","type":"Tun","sourceIP":"10.200.0.2","destinationIP":"198.18.0.9","sourcePort":"40000","destinationPort":"443","host":"api.example.com","sniffHost":""},"upload":300,"download":9000,"start":"2026-11-25T03:00:00.5Z","chains":["land-home-a"],"rule":"SRC-IP-CIDR"},
 {"id":"c2","metadata":{"network":"tcp","sourceIP":"10.200.0.2","destinationIP":"203.0.113.7","destinationPort":80,"host":"","sniffHost":"sniffed.example.com"},"upload":1,"download":2,"start":"2026-11-25T03:00:05Z","chains":["land-home-a","transit"]},
 {"id":"c3","metadata":{"network":"tcp","sourceIP":"10.200.0.6","destinationIP":"203.0.113.8","destinationPort":"22","host":""},"upload":7,"download":8,"start":"2026-11-25T03:00:01Z","chains":[]},
 {"id":"c4","metadata":{"network":"tcp","sourceIP":"198.18.0.1","destinationIP":"203.0.113.9","destinationPort":"443","host":"probe.example.com"},"upload":1,"download":1,"start":"2026-11-25T03:00:01Z","chains":[]},
 {"id":"c5","metadata":{"network":"tcp","sourceIP":"10.200.0.10","destinationIP":"203.0.113.9","destinationPort":"443","host":"x.example.com"},"upload":1,"download":1,"start":"2026-11-25T03:00:01Z","chains":[]}
],"memory":0}
`
	got := map[string]SlotSample{}
	for _, s := range parseTraffic(raw, "main", byIf, nets) {
		got[s.Slot] = s
	}
	if len(got) != 2 {
		t.Fatalf("应只有接了线的两个槽位: %+v", got)
	}
	a := got["alice"]
	if a.Gateway != "main" || a.Up != 1200 || a.Down != 34000 || !a.HasConns || len(a.Conns) != 2 {
		t.Fatalf("alice 的读数不对: %+v", a)
	}
	// 新的在前；域名取 host，没有时取嗅探到的；链路从槽位一侧到出口。
	if c := a.Conns[0]; c.ID != "c2" || c.Host != "sniffed.example.com" || c.Port != 80 || c.Chain != "transit → home-a" {
		t.Errorf("第一条连接不对: %+v", c)
	}
	if c := a.Conns[1]; c.ID != "c1" || c.Host != "api.example.com" || c.Port != 443 || c.Up != 300 || c.Down != 9000 || c.Chain != "home-a" || c.Start.IsZero() {
		t.Errorf("第二条连接不对: %+v", c)
	}
	if b := got["bob"]; len(b.Conns) != 1 || b.Conns[0].Host != "203.0.113.8" || b.Conns[0].Port != 22 {
		t.Errorf("bob 的连接不对: %+v", b)
	}

	// 核心没就绪：只有计数，连接不可用。
	for _, s := range parseTraffic("cs0 10 20\n---\n", "main", byIf, nets) {
		if s.Slot != "alice" || s.HasConns || s.Up != 10 {
			t.Errorf("没有连接快照时: %+v", s)
		}
	}
	if got := parseTraffic("---\n{\"connections\":[]}\n", "main", byIf, nets); len(got) != 0 {
		t.Errorf("一块网卡都没有时不应有读数: %+v", got)
	}
}

func TestTrafficObserve(t *testing.T) {
	st, err := store.Init(t.TempDir(), model.Cluster{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	conn := func(id, host string, up, down int64) Conn {
		return Conn{ID: id, Net: "tcp", Host: host, Port: 443, Up: up, Down: down, Start: t0, Chain: "home-a"}
	}
	sample := func(up, down int64, conns ...Conn) []SlotSample {
		return []SlotSample{{Slot: "alice", Gateway: "main", Up: up, Down: down, HasConns: true, Conns: conns}}
	}

	tr := NewTraffic()
	// 第一次看到：之前走过的 5000 / 90000 不知道是什么时候的，不计。
	tr.observe(at(0), sample(5000, 90000, conn("c1", "a.example.com", 10, 20)), st)
	tr.observe(at(10), sample(6000, 190000, conn("c1", "a.example.com", 500, 90000), conn("c2", "b.example.com", 1, 1)), st)
	snap := tr.Snapshot()
	if s := snap.Slots["alice"]; !snap.Recording || s.UpBps != 100 || s.DownBps != 10000 || s.Conns != 2 || s.Up != 6000 {
		t.Fatalf("实时的数不对: %+v", snap)
	}
	if got := tr.Conns("alice"); len(got) != 2 {
		t.Fatalf("当前连接不对: %+v", got)
	}

	// 第二个进程同时在采样：拿不到互斥，只有内存里的数，不写历史。
	other := NewTraffic()
	other.observe(at(10), sample(6000, 190000), st)
	if other.Snapshot().Recording {
		t.Fatal("同一个数据目录上只应有一个进程写历史")
	}

	// c1 结束了；核心一时没应答的那一次（HasConns 为假）不能把还开着的连接当成结束。
	tr.observe(at(20), []SlotSample{{Slot: "alice", Gateway: "main", Up: 6000, Down: 190000}}, st)
	tr.observe(at(30), sample(6100, 190100, conn("c2", "b.example.com", 50, 60)), st)
	conns, err := st.Conns("alice", t0, at(3600))
	if err != nil || len(conns) != 1 || conns[0].Host != "a.example.com" || conns[0].Up != 500 || conns[0].Down != 90000 || !conns[0].End.Equal(at(30)) {
		t.Fatalf("结束的连接应记下最后看到的流量: %+v %v", conns, err)
	}

	// 满 5 分钟落一档：这一段里的增量和连接数的峰值。
	if m, _ := st.Metrics(t0, at(3600)); len(m) != 0 {
		t.Fatalf("没到时间不应落盘: %+v", m)
	}
	tr.observe(at(300), sample(7000, 200000, conn("c2", "b.example.com", 50, 60)), st)
	m, err := st.Metrics(t0, at(3600))
	if err != nil || len(m) != 1 || m[0].Kind != model.MetricTraffic || m[0].Slot != "alice" || m[0].Span != 300 || m[0].Up != 2000 || m[0].Down != 110000 || m[0].Conns != 2 {
		t.Fatalf("落盘的一档不对: %+v %v", m, err)
	}

	// 网卡重建过（计数变小）：从 0 算起。然后槽位撤线：连接结束，速率清零。
	tr.observe(at(310), sample(40, 50, conn("c2", "b.example.com", 60, 70)), st)
	tr.observe(at(320), nil, st)
	if s, ok := tr.Snapshot().Slots["alice"]; ok {
		t.Fatalf("撤线之后不应还有实时的数: %+v", s)
	}
	if conns, _ := st.Conns("alice", t0, at(3600)); len(conns) != 2 || conns[1].Host != "b.example.com" {
		t.Fatalf("撤线时还开着的连接应记为结束: %+v", conns)
	}
	// 进程退出：没满一档的也写下去，交还互斥。
	tr.close(at(330), st)
	m, _ = st.Metrics(t0, at(3600))
	if len(m) != 2 || m[1].Up != 40 || m[1].Down != 50 || m[1].Span != 30 {
		t.Fatalf("退出时应把剩下的写下去: %+v", m)
	}

	// 重新启动：网卡没重建，期间又走了 960 / 9950，按空档的长度补一条。
	tr = NewTraffic()
	tr.observe(at(930), sample(1000, 10000), st)
	m, _ = st.Metrics(t0, at(3600))
	if !tr.Snapshot().Recording || len(m) != 3 || m[2].Up != 960 || m[2].Down != 9950 || m[2].Span != 600 || m[2].Conns != 0 {
		t.Fatalf("空档里的流量应补上: %+v", m)
	}
	tr.close(at(940), st)
}

func TestBuildHistory(t *testing.T) {
	from := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	min := func(m int) time.Time { return from.Add(time.Duration(m) * time.Minute) }
	recs := []model.Metric{
		// 正好一档。
		{T: min(5), Kind: model.MetricTraffic, Slot: "alice", Span: 300, Up: 300, Down: 3000, Conns: 4},
		// 跨两档各一半。
		{T: min(12).Add(30 * time.Second), Kind: model.MetricTraffic, Slot: "bob", Span: 300, Up: 1000, Down: 2000, Conns: 1},
		// 采样停了 20 分钟之后补的一条：摊到四档里。
		{T: min(40), Kind: model.MetricTraffic, Slot: "alice", Span: 1200, Up: 4000, Down: 8000},
		{T: min(1), Kind: model.MetricWatch, Slot: "alice", Status: WatchOK, ExitIP: "198.51.100.20"},
		{T: min(6), Kind: model.MetricWatch, Slot: "alice", Status: WatchOK, ExitIP: "198.51.100.20"},
		{T: min(11), Kind: model.MetricWatch, Slot: "alice", Status: WatchUnreachable},
		// 隔了很久才又检测：不和上一段连起来。
		{T: min(50), Kind: model.MetricWatch, Slot: "alice", Status: WatchUnreachable},
		{T: min(3), Kind: model.MetricSub, Sub: "air", Usable: 12, Total: 30},
	}
	h := buildHistory(from, min(44), 5*time.Minute, recs)
	if h.Step != 300 || len(h.Traffic) != 9 {
		t.Fatalf("分档不对: step=%d n=%d", h.Step, len(h.Traffic))
	}
	if p := h.Traffic[0]; p.Up != 300 || p.Down != 3000 || p.Conns != 4 || p.Slots["alice"].Up != 300 {
		t.Errorf("第 0 档: %+v", p)
	}
	if p1, p2 := h.Traffic[1], h.Traffic[2]; p1.Slots["bob"].Up != 500 || p2.Slots["bob"].Up != 500 || p1.Slots["bob"].Down != 1000 || p1.Conns != 1 {
		t.Errorf("跨档的应各一半: %+v %+v", p1, p2)
	}
	for i := 4; i <= 7; i++ {
		if p := h.Traffic[i]; p.Up != 1000 || p.Down != 2000 {
			t.Errorf("第 %d 档应摊到四分之一: %+v", i, p)
		}
	}
	if tot := h.Totals["alice"]; tot.Up != 4300 || tot.Down != 11000 || tot.Conns != 4 {
		t.Errorf("alice 的合计: %+v", tot)
	}
	w := h.Watch["alice"]
	if len(w) != 3 || w[0].Status != WatchOK || w[0].Checks != 2 || !w[0].To.Equal(min(11)) || w[1].Status != WatchUnreachable || !w[1].To.Equal(min(11)) || !w[2].From.Equal(min(50)) {
		t.Errorf("检测的时间线: %+v", w)
	}
	if s := h.Subs["air"]; len(s) != 1 || s[0].Usable != 12 || s[0].Total != 30 {
		t.Errorf("订阅的可用数: %+v", s)
	}
}

func TestBuildConnLog(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	rec := func(host string, endSec int, up, down int64) model.ConnRecord {
		return model.ConnRecord{Slot: "alice", Start: t0, End: t0.Add(time.Duration(endSec) * time.Second), Host: host, Port: 443, Up: up, Down: down}
	}
	log := buildConnLog(t0, t0.Add(time.Hour), []model.ConnRecord{rec("a.example.com", 10, 1, 100), rec("b.example.com", 20, 5, 5000), rec("a.example.com", 30, 2, 200)}, 2)
	if log.Total != 3 || len(log.Records) != 2 || log.Records[0].End.Sub(t0) != 30*time.Second || log.Records[1].Host != "b.example.com" {
		t.Fatalf("明细应是最近的两条，新的在前: %+v", log.Records)
	}
	if len(log.Hosts) != 2 || log.Hosts[0].Host != "b.example.com" || log.Hosts[1].Count != 2 || log.Hosts[1].Down != 300 || log.Hosts[1].Last.Sub(t0) != 30*time.Second {
		t.Fatalf("目标应按流量排: %+v", log.Hosts)
	}
}
