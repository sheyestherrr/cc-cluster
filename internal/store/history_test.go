package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func TestHistoryFiles(t *testing.T) {
	s, err := Init(t.TempDir(), model.Cluster{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }

	// 还没有任何历史时，读取和清理都不报错。
	if m, err := s.Metrics(day(1, 0), day(30, 0)); err != nil || len(m) != 0 {
		t.Fatalf("空目录: %v %v", m, err)
	}
	if n, err := s.PruneHistory(day(30, 0)); err != nil || n != 0 {
		t.Fatalf("空目录的清理: %d %v", n, err)
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AppendMetrics([]model.Metric{
		{T: day(20, 23), Kind: model.MetricTraffic, Slot: "alice", Span: 300, Up: 1, Down: 2},
		{T: day(21, 1), Kind: model.MetricWatch, Slot: "alice", Status: "ok"},
		{T: day(25, 3), Kind: model.MetricSub, Sub: "air", Usable: 3, Total: 9},
	}))
	must(s.AppendConns([]model.ConnRecord{
		{Slot: "alice", Start: day(21, 1), End: day(21, 2), Host: "a.example.com", Port: 443, Up: 5, Down: 6},
		{Slot: "bob", Start: day(25, 1), End: day(25, 2), Host: "b.example.com"},
	}))
	// 文件按天分开，权限 0600；坏掉的行跳过。
	p := filepath.Join(s.Dir(), "history", "2026-11-21.jsonl")
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("历史文件应为 0600: %v %v", fi, err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	must(err)
	_, err = f.WriteString("{写了一半\n")
	must(err)
	must(f.Close())

	m, err := s.Metrics(day(20, 23), day(21, 12))
	if err != nil || len(m) != 2 || m[0].Up != 1 || m[1].Status != "ok" {
		t.Fatalf("按时间读取: %+v %v", m, err)
	}
	if m, _ := s.Metrics(day(21, 0), day(26, 0)); len(m) != 2 || m[1].Usable != 3 {
		t.Fatalf("范围的起点应把更早的排除: %+v", m)
	}
	if c, err := s.Conns("alice", day(1, 0), day(30, 0)); err != nil || len(c) != 1 || c[0].Host != "a.example.com" || c[0].Down != 6 {
		t.Fatalf("按槽位读连接: %+v %v", c, err)
	}
	if c, _ := s.Conns("", day(1, 0), day(30, 0)); len(c) != 2 {
		t.Fatalf("不给槽位时是全部: %+v", c)
	}

	// 计数的位置。
	if c, err := s.Counters(); err != nil || len(c) != 0 {
		t.Fatalf("还没有时是空表: %v %v", c, err)
	}
	must(s.SaveCounters(map[string]model.Counter{"alice": {At: day(25, 3), Up: 10, Down: 20}}))
	if c, _ := s.Counters(); c["alice"].Down != 20 {
		t.Fatalf("计数的位置没存下: %+v", c)
	}

	// 清理：22 日之前的整天删掉，计数与互斥的文件不动。
	if n, err := s.PruneHistory(day(22, 5)); err != nil || n != 3 {
		t.Fatalf("应删掉 20、21 两天的三份文件: %d %v", n, err)
	}
	if m, _ := s.Metrics(day(1, 0), day(30, 0)); len(m) != 1 || m[0].Sub != "air" {
		t.Fatalf("清理之后: %+v", m)
	}
	if c, _ := s.Counters(); len(c) != 1 {
		t.Fatal("清理不应动计数的位置")
	}

	// 互斥：先到的拿到，后到的不等待；交还之后可以再拿。
	unlock, ok, err := s.LockSampler()
	if err != nil || !ok {
		t.Fatalf("第一个应拿到: %v %v", ok, err)
	}
	if _, ok, err := s.LockSampler(); err != nil || ok {
		t.Fatalf("第二个不应拿到: %v %v", ok, err)
	}
	unlock()
	unlock, ok, _ = s.LockSampler()
	if !ok {
		t.Fatal("交还之后应能再拿到")
	}
	unlock()
}
