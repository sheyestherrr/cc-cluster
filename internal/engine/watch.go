package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 持续检测：隔一段时间经各槽的链路看一眼连通性和出口。
//
// 两条规则：
//   - 出口变了（与接线时核对过的不是同一个地址）：立即给该槽断网并保持（hold），等人确认。
//     有落地的槽位出口由落地决定，直出的槽位出口就是那个固定的节点；两种情况下出口都不该变。
//   - 连不上：只记下来。此时槽位本来就没网（流量只能从它的出口走，没有别的路），
//     出口恢复且地址没变时自己就恢复了，不需要人处理。
//
// 检测只在已就绪的槽位上做，不启动任何东西；网关没在运行、槽位没接线的直接跳过。

// 检测结果的几种状态。
const (
	WatchOK          = "ok"          // 连得通，出口与接线时一致
	WatchUnreachable = "unreachable" // 经链路访问不了探测地址
	WatchChanged     = "changed"     // 出口变了，已断网保持
)

// DefaultWatchInterval 是持续检测的默认间隔。
const DefaultWatchInterval = 5 * time.Minute

// WatchOnce 检测一遍所有已就绪的槽位，返回各自的结果（没有检测的槽位不在其中）。
func (e *Engine) WatchOnce(ctx context.Context) ([]model.Watch, error) {
	slots, err := e.st.Slots()
	if err != nil {
		return nil, err
	}
	var out []model.Watch
	for _, sl := range slots {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		w, checked, err := e.watchSlot(ctx, sl)
		if err != nil {
			e.logf("提醒：检测槽位 %s 时出错：%v", sl.ID, err)
			continue
		}
		if checked {
			out = append(out, w)
		}
	}
	return out, nil
}

func (e *Engine) watchSlot(ctx context.Context, sl model.Slot) (w model.Watch, checked bool, err error) {
	st, err := e.slotStatus(ctx, sl)
	if err != nil || st.State != StateReady || st.ExitIP == "" {
		return w, false, err
	}
	p, err := e.plan(sl.Gateway)
	if err != nil {
		return w, false, err
	}
	rt, err := e.gatewayRuntime(ctx, p.gw)
	if err != nil {
		return w, false, err
	}
	// 只要出口 IP：从清单的末尾问起，那里是纯文本、不容易被限流的保底地址；最多问两个，
	// 连不上的槽位不至于把一轮检测拖得很久。
	urls := make([]string, 0, 2)
	for i := len(p.gw.EchoURLs) - 1; i >= 0 && len(urls) < 2; i-- {
		urls = append(urls, p.gw.EchoURLs[i])
	}
	probe := func() (string, error) {
		exit, err := e.probeChain(ctx, rt, p.links[sl.ID], urls, false)
		return exit.IP, err
	}

	w = model.Watch{Slot: sl.ID, At: time.Now().UTC().Truncate(time.Second), Expect: st.ExitIP}
	ip, perr := probe()
	if perr != nil { // 偶尔一次超时不算数，隔一会儿再试一次
		select {
		case <-ctx.Done():
			return w, false, ctx.Err()
		case <-time.After(3 * time.Second):
		}
		ip, perr = probe()
	}
	switch {
	case perr != nil:
		w.Status, w.Detail = WatchUnreachable, "经链路访问不了探测地址；此时槽位没有网络，出口恢复后会自己恢复"
	case ip == st.ExitIP:
		w.Status, w.ExitIP = WatchOK, ip
	default:
		// 换一次再确认：有的探测地址偶尔会答错。两次都不是原来的地址才算变了。
		if again, aerr := probe(); aerr == nil && again == st.ExitIP {
			w.Status, w.ExitIP = WatchOK, again
			break
		}
		w.Status, w.ExitIP = WatchChanged, ip
		w.Detail = fmt.Sprintf("出口从 %s 变成了 %s", st.ExitIP, ip)
		if herr := e.holdIfUnchanged(ctx, sl.ID, st.ExitIP, "持续检测："+w.Detail+"，已自动断网；确认后 slot release 再 slot up"); herr != nil {
			w.Detail += "；自动断网没有成功：" + herr.Error()
		}
	}

	prev, had, _ := e.st.Watch(sl.ID)
	if had && prev.Status == w.Status {
		w.Since = prev.Since
	} else {
		w.Since = w.At
		switch w.Status { // 状态变化时才出声
		case WatchUnreachable:
			e.logf("槽位 %s：%s", sl.ID, w.Detail)
		case WatchChanged:
			e.logf("槽位 %s：%s，已自动断网", sl.ID, w.Detail)
		case WatchOK:
			if had {
				e.logf("槽位 %s：恢复正常，出口 %s", sl.ID, w.ExitIP)
			}
		}
	}
	// 每一轮的结果另记一笔历史，界面上画成时间线。
	e.recordMetric(model.Metric{T: w.At, Kind: model.MetricWatch, Slot: sl.ID, Status: w.Status, ExitIP: w.ExitIP})
	if err := e.st.SaveWatch(sl.ID, w); err != nil {
		return w, true, err
	}
	return w, true, nil
}

// holdIfUnchanged 在取到锁之后再核对一遍：槽位还是刚才检测时的那次接线（期间没有人重新 slot up
// 或已经 hold），才断网保持。
func (e *Engine) holdIfUnchanged(ctx context.Context, id, expect, reason string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	slot, err := e.st.Slot(id)
	if err != nil {
		return err
	}
	if _, held, _ := e.st.Hold(id); held {
		return nil
	}
	if a, ok, _ := e.st.Applied(id); !ok || a.ExitIP != expect {
		return nil
	}
	defer func() { e.record("slot.hold", id, err, reason) }()
	// 与 SlotHold 相同：先落标记再撤线。
	if err := e.st.SetHold(id, model.Hold{Reason: reason, At: time.Now().UTC()}); err != nil {
		return err
	}
	return e.disconnect(ctx, slot, false)
}

// Watch 每隔 interval 检测一遍，直到 ctx 结束；每一轮之后顺带刷新到了时间的远程订阅。
// open 每一轮新开一个 Engine（数据目录与 Docker 连接），
// 用完由调用方回收；这样长时间运行时不握着旧的状态。第一轮在启动后稍等片刻就做。
func Watch(ctx context.Context, interval time.Duration, open func(context.Context) (*Engine, error)) {
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	wait := 20 * time.Second
	if wait > interval {
		wait = interval
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = interval
		eng, err := open(ctx)
		if err != nil {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, _ = eng.WatchOnce(rctx)
		cancel()
		// 顺带刷新到了时间的远程订阅：管理器没有别的常驻进程可以做这件事。
		rctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		eng.RefreshDueSubs(rctx)
		cancel()
	}
}
