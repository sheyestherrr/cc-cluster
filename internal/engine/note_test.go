package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 备注可以用 ID 里不允许的字符；四类对象都能改、能清，并且不影响“槽位的期望配置是否已应用”的判断。
func TestSetNote(t *testing.T) {
	yaml := `proxies:
  - {name: "HK 01", type: ss, server: 203.0.113.2, port: 8388, cipher: aes-256-gcm, password: p}
`
	eng, st := newTestEngine(t, &yaml, nil)
	ctx := context.Background()
	proxy := map[string]any{"type": "socks5", "server": "203.0.113.9", "port": 1080}

	if err := eng.AddNode(ctx, model.Node{ID: "land", Role: model.RoleLanding, Proxy: proxy, Note: "韩国家宽_LG"}); err != nil {
		t.Fatalf("带备注登记节点失败: %v", err)
	}
	if err := eng.AddNode(ctx, model.Node{ID: "bad", Role: model.RoleLanding, Proxy: proxy, Note: "两行\n备注"}); err == nil {
		t.Fatal("带换行的备注应被拒绝")
	}
	if _, err := eng.SubAdd(ctx, "air", "机场 A", SubInput{URL: "https://sub.example.com/x"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AddGateway(model.Gateway{ID: "solo", Note: "直连"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.AddSlot(ctx, model.Slot{ID: "s", Gateway: "solo", Landing: "land", UID: 1000, GID: 1000, Note: "alice 的"}, SlotOptions{}); err != nil {
		t.Fatal(err)
	}

	digest := func() string {
		t.Helper()
		p, err := eng.plan("solo")
		if err != nil {
			t.Fatal(err)
		}
		sl, err := st.Slot("s")
		if err != nil {
			t.Fatal(err)
		}
		d, err := eng.slotDigest(sl, model.Identity{}, p)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	before := digest()

	for kind, id := range map[string]string{KindNode: "land", KindSub: "air", KindGateway: "solo", KindSlot: "s"} {
		if err := eng.SetNote(kind, id, "  改过的 备注_1  "); err != nil {
			t.Fatalf("修改 %s %s 的备注失败: %v", kind, id, err)
		}
		if err := eng.SetNote(kind, id, strings.Repeat("长", model.MaxNoteLen+1)); err == nil {
			t.Errorf("%s 的备注超长应被拒绝", kind)
		}
		if err := eng.SetNote(kind, "nope", "x"); err == nil {
			t.Errorf("给不存在的 %s 设备注应报错", kind)
		}
	}
	n, _ := st.Node("land")
	sub, _ := st.Sub("air")
	gw, _ := st.Gateway("solo")
	sl, _ := st.Slot("s")
	for what, got := range map[string]string{"节点": n.Note, "订阅": sub.Note, "网关": gw.Note, "槽位": sl.Note} {
		if got != "改过的 备注_1" {
			t.Errorf("%s的备注 = %q", what, got)
		}
	}
	// 改备注不动别的字段。
	if len(sub.Proxies) != 1 || n.Proxy["server"] != "203.0.113.9" || sl.Subnet == "" {
		t.Errorf("改备注不应影响其他内容: %+v %+v %+v", sub, n, sl)
	}
	if after := digest(); after != before {
		t.Error("改备注不应改变槽位的配置摘要：否则运行中的槽位会被当成需要重新应用")
	}

	if err := eng.SetNote(KindSlot, "s", ""); err != nil {
		t.Fatal(err)
	}
	if sl, _ := st.Slot("s"); sl.Note != "" {
		t.Errorf("备注应被清掉: %q", sl.Note)
	}
	if err := eng.SetNote("cluster", "t", "x"); err == nil {
		t.Error("不认识的对象种类应报错")
	}

	// 断网原因同样是给人看的一行文字：超长或带换行的不收，也不留下断网标记。
	for what, reason := range map[string]string{"超长": strings.Repeat("长", model.MaxHoldReasonLen+1), "带换行": "第一行\n第二行"} {
		if err := eng.SlotHold(ctx, "s", reason); err == nil {
			t.Errorf("%s的断网原因应被拒绝", what)
		}
	}
	if _, held, _ := st.Hold("s"); held {
		t.Error("原因被拒绝时不应写入断网标记")
	}
	// 操作记录里的说明有长度上限。
	eng.record("test.op", "s", nil, strings.Repeat("x", 5000))
	b, err := os.ReadFile(filepath.Join(st.Dir(), "ops.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if last := lines[len(lines)-1]; len(last) > 1000 {
		t.Errorf("操作记录的一行应被截短，实际 %d 字节", len(last))
	}
}
