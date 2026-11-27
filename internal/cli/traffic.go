package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
)

// cmdTraffic 列出各槽位的流量；给了槽位时列出它当前的连接和这段时间里连过的目标。
// 只能看到连接这一层：目标域名与端口、流量、时长，看不到 HTTPS 的路径和内容。
func (a *app) cmdTraffic(ctx context.Context, args []string) error {
	fs := a.newFlags("traffic", "traffic [槽位] [--hours 24]")
	hours := fs.Int("hours", 24, "统计最近多少小时的历史（最多 720）")
	top := fs.Int("top", 15, "指定槽位时，列出流量最多的目标个数")
	slot := ""
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		slot, args = args[0], args[1:]
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return err
	}
	span := time.Duration(*hours) * time.Hour
	now := time.Now()

	// 此刻的速率要两次读数：隔两秒读一遍。
	first, err := eng.SampleTraffic(ctx)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	second, err := eng.SampleTraffic(ctx)
	if err != nil {
		return err
	}
	prev := map[string]engine.SlotSample{}
	for _, s := range first {
		prev[s.Slot] = s
	}
	live := map[string]engine.SlotSample{}
	for _, s := range second {
		live[s.Slot] = s
	}
	rate := func(s engine.SlotSample) (up, down float64) {
		p, ok := prev[s.Slot]
		if !ok || s.Up < p.Up || s.Down < p.Down {
			return 0, 0
		}
		return float64(s.Up-p.Up) / 2, float64(s.Down-p.Down) / 2
	}

	if slot != "" {
		log, err := eng.ConnLog(slot, now, span, *top)
		if err != nil {
			return err
		}
		s, wired := live[slot]
		switch {
		case !wired:
			fmt.Fprintf(a.out, "槽位 %s 未接线，无连接。\n", slot)
		case !s.HasConns:
			fmt.Fprintf(a.out, "槽位 %s 所在网关的核心无响应，无法读取当前连接。\n", slot)
		case len(s.Conns) == 0:
			fmt.Fprintf(a.out, "槽位 %s 当前无连接。\n", slot)
		default:
			up, down := rate(s)
			fmt.Fprintf(a.out, "槽位 %s 当前有 %d 条连接，上行 %s/s，下行 %s/s：\n", slot, len(s.Conns), bytesText(int64(up)), bytesText(int64(down)))
			w := a.table()
			fmt.Fprintln(w, "目标\t上行\t下行\t已持续\t链路")
			for _, c := range s.Conns {
				fmt.Fprintf(w, "%s:%d\t%s\t%s\t%s\t%s\n", c.Host, c.Port, bytesText(c.Up), bytesText(c.Down), spanText(now.Sub(c.Start)), orDash(c.Chain))
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}
		fmt.Fprintf(a.out, "\n最近 %d 小时内结束的连接共 %d 条", *hours, log.Total)
		if log.Total == 0 {
			fmt.Fprintln(a.out, "。连接历史仅在 cc-cluster web 或 cc-cluster watch 运行期间记录。")
			return nil
		}
		fmt.Fprintln(a.out, "，按流量排序：")
		w := a.table()
		fmt.Fprintln(w, "目标\t连接数\t上行\t下行\t最近一次")
		for _, h := range log.Hosts {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", h.Host, h.Count, bytesText(h.Up), bytesText(h.Down), h.Last.Local().Format("01-02 15:04"))
		}
		return w.Flush()
	}

	hist, err := eng.History(now, span)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for id := range live {
		ids[id] = true
	}
	for id := range hist.Totals {
		ids[id] = true
	}
	if len(ids) == 0 {
		fmt.Fprintln(a.out, "没有已接线的槽位，也没有流量历史。流量历史仅在 cc-cluster web 或 cc-cluster watch 运行期间记录。")
		return nil
	}
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	w := a.table()
	fmt.Fprintf(w, "槽位\t当前上行\t当前下行\t连接\t近 %d 小时上行\t近 %d 小时下行\n", *hours, *hours)
	for _, id := range names {
		up, down, conns := "-", "-", "-"
		if s, ok := live[id]; ok {
			u, d := rate(s)
			up, down = bytesText(int64(u))+"/s", bytesText(int64(d))+"/s"
			if s.HasConns {
				conns = fmt.Sprint(len(s.Conns))
			}
		}
		t := hist.Totals[id]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, up, down, conns, bytesText(t.Up), bytesText(t.Down))
	}
	return w.Flush()
}

// bytesText 把字节数写成带单位的短形式。
func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n)/unit, 0
	for ; v >= unit && i < 3; i++ {
		v /= unit
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGT"[i])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGT"[i])
}

// spanText 把一段时间写成“3 秒”“5 分”“2 小时”这样的短形式。
func spanText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1f 小时", d.Hours())
	}
}
