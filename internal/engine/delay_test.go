package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/mihomo"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func TestParseDelays(t *testing.T) {
	names := []string{"n0", "n1", "n2"}
	got, err := parseDelays(`{"n0": 120, "n2": 45}`, names)
	if err != nil || got[0] != 120 || got[1] != model.DelayFailed || got[2] != 45 {
		t.Errorf("成功的记毫秒，没出现的记不通: %v %v", got, err)
	}
	// 全部不通时核心返回的是错误对象，不算测速本身失败。
	got, err = parseDelays(`{"message": "get delay: all proxies timeout"}`, names)
	if err != nil || got[0] != model.DelayFailed || got[1] != model.DelayFailed || got[2] != model.DelayFailed {
		t.Errorf("全部不通: %v %v", got, err)
	}
	for _, body := range []string{"", "<html>502</html>", `{"other": 12}`} {
		if got, err = parseDelays(body, names); err == nil {
			t.Errorf("parseDelays(%q) 应当报错: %v", body, got)
		}
	}
}

func TestSubDelaysOrderAndFastest(t *testing.T) {
	sub := model.Subscription{Proxies: []model.SubProxy{
		{Name: "没测过", Proxy: map[string]any{"type": "ss", "server": "a.example", "port": 1}},
		{Name: "不通", DelayMS: model.DelayFailed},
		{Name: "慢", DelayMS: 300},
		{Name: "套餐到期：2026-11-23", DelayMS: 20},
		{Name: "快", DelayMS: 80},
	}}
	delays := SubDelays(sub)
	var order []string
	for _, d := range delays {
		order = append(order, d.Name)
	}
	want := []string{"套餐到期：2026-11-23", "快", "慢", "没测过", "不通"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("顺序应是测通的按延迟、没测过的、不通的: %v", order)
		}
	}
	if delays[3].Type != "ss" || delays[3].Server != "a.example:1" {
		t.Errorf("类型与服务器取自片段: %+v", delays[3])
	}
	// 提示项即使延迟最低也不自动选用。
	if best, ok := Fastest(delays); !ok || best.Name != "快" {
		t.Errorf("Fastest = %+v, %v", best, ok)
	}
	if _, ok := Fastest(SubDelays(model.Subscription{Proxies: []model.SubProxy{{Name: "不通", DelayMS: model.DelayFailed}}})); ok {
		t.Error("没有测通的节点时不应选出任何一个")
	}
	if got := []string{delays[1].DelayText(), delays[3].DelayText(), delays[4].DelayText()}; got[0] != "80 ms" || got[1] != "未测试" || got[2] != "不可用" {
		t.Errorf("延迟的显示文本: %v", got)
	}
}

func TestMeasureDelaysNeedsDocker(t *testing.T) {
	e, _ := newTestEngine(t, nil, nil)
	if _, err := e.measureDelays(context.Background(), nil, nil, DelayOptions{URL: "https://example.com"}); err == nil {
		t.Error("没有 Docker 连接时应当报错")
	}
}

func TestRenderDelayProbe(t *testing.T) {
	src := []map[string]any{{"type": "socks5", "server": "a.example", "port": 1080}, {"type": "http", "server": "b.example", "port": 80}}
	cfg, names, err := mihomo.RenderDelayProbe(src, nil)
	if err != nil || len(names) != 2 || names[0] != "n0" || names[1] != "n1" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	if _, has := src[0]["name"]; has {
		t.Error("不应改动传入的片段")
	}
	text := string(cfg)
	for _, want := range []string{`"name":"n1"`, `"proxy-groups":[{"name":"all","proxies":["n0","n1"],"type":"select"}]`, `"rules":["MATCH,REJECT"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("配置里缺少 %s:\n%s", want, text)
		}
	}
	for _, banned := range []string{`"tun"`, `"listeners"`, `"mixed-port"`, `"port":7890`, `"external-controller":`} {
		if strings.Contains(text, banned) {
			t.Errorf("测速配置不应有入口 %s", banned)
		}
	}

	// 接上落地测整条链路：每个节点后面跟一个经它拨号的落地，测的是这些落地。
	via := map[string]any{"type": "socks5", "server": "land.example", "port": 1080, "dialer-proxy": "someone-else"}
	cfg, names, err = mihomo.RenderDelayProbe(src, via)
	if err != nil || names[0] != "c0" || names[1] != "c1" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	text = string(cfg)
	for _, want := range []string{`"dialer-proxy":"n0"`, `"dialer-proxy":"n1"`, `"proxies":["c0","c1"]`, `"name":"n1"`} {
		if !strings.Contains(text, want) {
			t.Errorf("链路测速配置里缺少 %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "someone-else") || via["dialer-proxy"] != "someone-else" {
		t.Error("落地片段自带的拨号方式不应进入配置，也不应改动传入的片段")
	}
}
