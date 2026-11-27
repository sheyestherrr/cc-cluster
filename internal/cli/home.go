package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
)

// 不带参数运行 cc-cluster 时的那一屏：在当前目录里选一个槽位进去干活。
// 日常使用只需要这一个入口；增删槽位、节点和订阅在网页界面里做，完整的命令行是给脚本和排查用的。

const (
	modeClaude = "claude"
	modeShell  = "shell"
)

// lastChoice 是上一次在某个目录里选的槽位和进入方式。
type lastChoice struct {
	Slot string `json:"slot"`
	Mode string `json:"mode"`
}

// lastChoices 记在数据目录的 last.json 里：按目录记，另有一份不分目录的作为没记录过的目录的默认值。
type lastChoices struct {
	Any  lastChoice            `json:"any"`
	Dirs map[string]lastChoice `json:"dirs,omitempty"`
}

const maxLastDirs = 200

func lastPath(dataDir string) string { return filepath.Join(dataDir, "last.json") }

func loadLast(dataDir string) lastChoices {
	var l lastChoices
	if b, err := os.ReadFile(lastPath(dataDir)); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

// For 返回在 dir 里的默认选择：这个目录记过就用它，否则用最近一次的。
func (l lastChoices) For(dir string) lastChoice {
	if c, ok := l.Dirs[dir]; ok {
		return c
	}
	return l.Any
}

// saveLast 记下这次的选择。记不下来不算错：下次只是没有默认值。
func saveLast(dataDir, dir string, c lastChoice) {
	l := loadLast(dataDir)
	if l.Dirs == nil || len(l.Dirs) >= maxLastDirs {
		l.Dirs = map[string]lastChoice{}
	}
	l.Any, l.Dirs[dir] = c, c
	if b, err := json.Marshal(l); err == nil {
		_ = os.WriteFile(lastPath(dataDir), b, 0o600)
	}
}

var slotStateText = map[string]string{
	engine.StateReady: "已接线", engine.StateOffline: "未接线", engine.StateStopped: "已停止",
	engine.StateAbsent: "未启动", engine.StateHold: "手动断网",
}

// pickIndex 把使用者的回答换成下标：可以是编号，也可以是名字。
func pickIndex(ans string, names []string) (int, bool) {
	if n, err := strconv.Atoi(ans); err == nil {
		return n - 1, n >= 1 && n <= len(names)
	}
	for i, name := range names {
		if name == ans {
			return i, true
		}
	}
	return 0, false
}

func (a *app) home(ctx context.Context) error {
	if err := a.panelDataDir(); err != nil {
		return err
	}
	lctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	eng, _, err := a.open(lctx, true)
	if err != nil {
		return err
	}
	sts, err := eng.SlotStatuses(lctx)
	if err != nil {
		return err
	}
	if len(sts) == 0 {
		fmt.Fprintln(a.out, "暂无槽位。槽位、节点与订阅在网页界面中管理：")
		fmt.Fprintln(a.out)
		return a.cmdWeb(ctx, nil)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	last := loadLast(a.dataDir).For(cwd)

	fmt.Fprintf(a.out, "当前目录 %s\n\n", cwd)
	names := make([]string, len(sts))
	// 默认选上次用的；没有记录时选第一个已接线的。
	def, defSet := 0, false
	w := a.table()
	for i, s := range sts {
		names[i] = s.Slot.ID
		if !defSet && last.Slot == "" && s.State == engine.StateReady {
			def, defSet = i, true
		}
		where := s.ExitWhere
		if where == "" {
			where = "落地 " + s.Slot.Landing
		}
		var notes []string
		if s.Slot.Note != "" {
			notes = append(notes, s.Slot.Note)
		}
		switch {
		case s.State == engine.StateHold:
			notes = append(notes, "需先在网页界面中解除断网")
		case s.State != engine.StateReady:
			notes = append(notes, "选择后先启动")
		}
		if s.Slot.ID == last.Slot {
			def = i
			notes = append(notes, "上次使用")
		}
		fmt.Fprintf(w, "  %d)\t%s\t%s\t%s\t%s\n", i+1, s.Slot.ID, slotStateText[s.State], where, strings.Join(notes, "，"))
	}
	_ = w.Flush()
	fmt.Fprintln(a.out, "  w)  网页界面（管理槽位、节点、订阅）")
	fmt.Fprintln(a.out, "  q)  退出")
	fmt.Fprintln(a.out)

	slot := ""
	for slot == "" {
		ans, err := a.ask("请选择", strconv.Itoa(def+1))
		if err != nil {
			return err
		}
		switch strings.ToLower(ans) {
		case "q":
			return nil
		case "w":
			return a.cmdWeb(ctx, nil)
		}
		if i, ok := pickIndex(ans, names); ok {
			slot = names[i]
		} else {
			fmt.Fprintf(a.out, "无效的选项 %q，请输入编号、槽位名、w 或 q\n", ans)
		}
	}

	mode, defMode := "", "1"
	if last.Mode == modeShell {
		defMode = "2"
	}
	for mode == "" {
		ans, err := a.ask("进入方式：1) Claude  2) 终端", defMode)
		if err != nil {
			return err
		}
		switch strings.ToLower(ans) {
		case "1", "c", modeClaude:
			mode = modeClaude
		case "2", "s", modeShell:
			mode = modeShell
		case "q":
			return nil
		default:
			fmt.Fprintln(a.out, "请输入 1 或 2")
		}
	}
	saveLast(a.dataDir, cwd, lastChoice{Slot: slot, Mode: mode})
	return a.enter(lctx, enterRequest{Slot: slot, Dir: cwd, Claude: mode == modeClaude})
}
