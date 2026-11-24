package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 历史数据放在数据目录的 history/ 下，按天（UTC）分文件，每行一条 JSON，只追加：
//
//	history/2026-11-25.jsonl        流量、持续检测、订阅测速的记录（model.Metric）
//	history/conn-2026-11-25.jsonl   已结束的连接（model.ConnRecord）
//	history/counters.json           各槽位网卡计数上一次落盘时的位置
//	history/sampler.lock            采样进程之间的互斥
//
// 过期的按整天删除。里面没有凭据，但连接记录是各槽位访问过的域名，文件权限同样是 0600。
const (
	historyDir  = "history"
	dayLayout   = "2006-01-02"
	metricStem  = ""
	connStem    = "conn-"
	historyExt  = ".jsonl"
	maxLineSize = 1 << 20
)

func (s *Store) historyFile(stem string, day time.Time) string {
	return s.path(historyDir, stem+day.UTC().Format(dayLayout)+historyExt)
}

// appendLines 把若干条记录追加到 history/ 下的一个文件里；一次写入，避免两个进程的行交错。
func (s *Store) appendLines(p string, recs []any) error {
	if len(recs) == 0 {
		return nil
	}
	var buf []byte
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf = append(append(buf, b...), '\n')
	}
	if err := os.MkdirAll(s.path(historyDir), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(buf)
	return err
}

// AppendMetrics 追加历史记录，按各自的时间落到当天的文件里。
func (s *Store) AppendMetrics(recs []model.Metric) error {
	byDay := map[string][]any{}
	for _, r := range recs {
		p := s.historyFile(metricStem, r.T)
		byDay[p] = append(byDay[p], r)
	}
	for p, list := range byDay {
		if err := s.appendLines(p, list); err != nil {
			return err
		}
	}
	return nil
}

// AppendConns 追加已结束的连接，按结束时间落到当天的文件里。
func (s *Store) AppendConns(recs []model.ConnRecord) error {
	byDay := map[string][]any{}
	for _, r := range recs {
		p := s.historyFile(connStem, r.End)
		byDay[p] = append(byDay[p], r)
	}
	for p, list := range byDay {
		if err := s.appendLines(p, list); err != nil {
			return err
		}
	}
	return nil
}

// scanDays 依次读 [from, to] 涉及的每一天的文件，把每一行交给 line；文件不存在就跳过，坏掉的行由 line 自己忽略。
func (s *Store) scanDays(stem string, from, to time.Time, line func([]byte)) error {
	day := from.UTC().Truncate(24 * time.Hour)
	for ; !day.After(to.UTC()); day = day.Add(24 * time.Hour) {
		f, err := os.Open(s.historyFile(stem, day))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), maxLineSize)
		for sc.Scan() {
			line(sc.Bytes())
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Metrics 读出时间在 [from, to] 之内的历史记录，按文件里的顺序（大体上是时间顺序）。
// 流量记录覆盖的是 T 之前的一段，所以调用方想要完整的一段时，from 要往前多取一点。
func (s *Store) Metrics(from, to time.Time) ([]model.Metric, error) {
	var out []model.Metric
	err := s.scanDays(metricStem, from, to, func(b []byte) {
		var m model.Metric
		if json.Unmarshal(b, &m) != nil || m.T.Before(from) || m.T.After(to) {
			return
		}
		out = append(out, m)
	})
	return out, err
}

// Conns 读出一个槽位在 [from, to] 之内结束的连接，按文件里的顺序；slot 为空时是全部槽位。
func (s *Store) Conns(slot string, from, to time.Time) ([]model.ConnRecord, error) {
	var out []model.ConnRecord
	err := s.scanDays(connStem, from, to, func(b []byte) {
		var c model.ConnRecord
		if json.Unmarshal(b, &c) != nil || c.End.Before(from) || c.End.After(to) || (slot != "" && c.Slot != slot) {
			return
		}
		out = append(out, c)
	})
	return out, err
}

// PruneHistory 删除早于 before 那一天的历史文件，返回删掉的个数。
func (s *Store) PruneHistory(before time.Time) (int, error) {
	names, err := listNames(s.path(historyDir), historyExt)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cut := before.UTC().Truncate(24 * time.Hour)
	n := 0
	for _, name := range names {
		day, err := time.Parse(dayLayout, strings.TrimPrefix(name, connStem))
		if err != nil || !day.Before(cut) {
			continue
		}
		if err := os.Remove(s.path(historyDir, name+historyExt)); err == nil {
			n++
		}
	}
	return n, nil
}

// Counters 读出各槽位网卡计数上一次落盘时的位置；还没有时返回空表。
func (s *Store) Counters() (map[string]model.Counter, error) {
	out := map[string]model.Counter{}
	err := readJSON(s.path(historyDir, "counters.json"), &out)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	return out, err
}

// SaveCounters 写入各槽位网卡计数的位置。
func (s *Store) SaveCounters(c map[string]model.Counter) error {
	if err := os.MkdirAll(s.path(historyDir), 0o700); err != nil {
		return err
	}
	return writeJSON(s.path(historyDir, "counters.json"), c)
}
