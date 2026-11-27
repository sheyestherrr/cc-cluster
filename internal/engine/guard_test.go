package engine

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 后台循环里的一轮崩溃时只跳过这一轮：循环继续，原因写进日志。
func TestBackgroundLoopsSurvivePanics(t *testing.T) {
	loops := map[string]func(context.Context, func(context.Context) (*Engine, error)){
		"持续检测": func(ctx context.Context, open func(context.Context) (*Engine, error)) {
			Watch(ctx, time.Millisecond, open)
		},
		"流量采样": func(ctx context.Context, open func(context.Context) (*Engine, error)) {
			SampleLoop(ctx, NewTraffic(), time.Millisecond, open)
		},
	}
	for what, run := range loops {
		var out bytes.Buffer
		old := guardOut
		guardOut = &out
		ctx, cancel := context.WithCancel(context.Background())
		var rounds atomic.Int32
		open := func(context.Context) (*Engine, error) {
			if rounds.Add(1) >= 3 {
				cancel()
			}
			panic("模拟的故障")
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			run(ctx, open)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s：循环没有在取消后结束", what)
		}
		cancel()
		guardOut = old
		if n := rounds.Load(); n < 3 {
			t.Errorf("%s：崩溃之后应继续下一轮，只进行了 %d 轮", what, n)
		}
		if log := out.String(); !strings.Contains(log, what+"出错") || !strings.Contains(log, "模拟的故障") {
			t.Errorf("%s：日志里应有崩溃的原因: %q", what, log)
		}
	}
}

func TestGuardReportsWhetherItCompleted(t *testing.T) {
	var out bytes.Buffer
	old := guardOut
	guardOut = &out
	defer func() { guardOut = old }()
	if !guard("测试", func() {}) {
		t.Error("正常结束时应返回 true")
	}
	if guard("测试", func() { panic("x") }) {
		t.Error("崩溃时应返回 false")
	}
}
