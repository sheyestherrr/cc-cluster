package cli

import "testing"

func TestLastChoices(t *testing.T) {
	dir := t.TempDir()
	if c := loadLast(dir).For("/p/a"); c.Slot != "" {
		t.Fatalf("没有记录时不应有默认值: %+v", c)
	}
	saveLast(dir, "/p/a", lastChoice{Slot: "work", Mode: modeClaude})
	saveLast(dir, "/p/b", lastChoice{Slot: "spare", Mode: modeShell})
	l := loadLast(dir)
	if c := l.For("/p/a"); c.Slot != "work" || c.Mode != modeClaude {
		t.Fatalf("应按目录记住: %+v", c)
	}
	if c := l.For("/p/never"); c.Slot != "spare" || c.Mode != modeShell {
		t.Fatalf("没记过的目录用最近一次的: %+v", c)
	}
}

func TestPickIndex(t *testing.T) {
	names := []string{"work", "research", "2"}
	for ans, want := range map[string]int{"1": 0, "3": 2, "research": 1} {
		if i, ok := pickIndex(ans, names); !ok || i != want {
			t.Errorf("%q 应选中第 %d 项: %d %v", ans, want+1, i, ok)
		}
	}
	// 数字一律当编号，不当名字。
	if i, ok := pickIndex("2", names); !ok || i != 1 {
		t.Errorf("数字应当按编号解释: %d %v", i, ok)
	}
	for _, bad := range []string{"0", "4", "nope", ""} {
		if _, ok := pickIndex(bad, names); ok {
			t.Errorf("%q 不应选中任何一项", bad)
		}
	}
}
