package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// errCancelled 表示用户在交互中选择了取消。
var errCancelled = errors.New("已取消")

// isTerminal 报告 f 是否接在终端上。
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// readLine 读一行输入，去掉首尾空白。输入结束（Ctrl-D）按取消处理。
func (a *app) readLine() (string, error) {
	if a.in == nil {
		return "", errCancelled
	}
	s, err := a.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		fmt.Fprintln(a.out)
		return "", errCancelled
	}
	return strings.TrimSpace(s), nil
}

// ask 提问并返回回答；直接回车时取默认值。
func (a *app) ask(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(a.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(a.out, "%s: ", question)
	}
	s, err := a.readLine()
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

// askRequired 反复提问直到得到非空的回答。
func (a *app) askRequired(question string) (string, error) {
	for {
		s, err := a.ask(question, "")
		if err != nil {
			return "", err
		}
		if s != "" {
			return s, nil
		}
	}
}

// askSecret 读取一段不该显示在屏幕上的内容（口令、带令牌的订阅地址）。
// 关闭回显靠 stty，做不到时照常读取。
func (a *app) askSecret(question string) (string, error) {
	fmt.Fprintf(a.out, "%s（输入不显示）: ", question)
	restore := func() {}
	if a.interactive {
		if stty, err := exec.LookPath("stty"); err == nil {
			off := exec.Command(stty, "-echo")
			off.Stdin = os.Stdin
			if off.Run() == nil {
				restore = func() {
					on := exec.Command(stty, "echo")
					on.Stdin = os.Stdin
					_ = on.Run()
					fmt.Fprintln(a.out)
				}
			}
		}
	}
	s, err := a.readLine()
	restore()
	return s, err
}

// confirm 问一个是非题。
func (a *app) confirm(question string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Fprintf(a.out, "%s [%s]: ", question, hint)
		s, err := a.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes", "是":
			return true, nil
		case "n", "no", "否":
			return false, nil
		}
	}
}

// choose 列出选项让用户按编号选一个，返回下标。def 是直接回车时的选项（从 0 起，负数表示没有默认）。
// 输入 0 或 q 表示取消。
func (a *app) choose(title string, items []string, def int) (int, error) {
	if len(items) == 0 {
		return 0, errors.New("没有可选的项目")
	}
	fmt.Fprintln(a.out, title)
	for i, it := range items {
		fmt.Fprintf(a.out, "  %d) %s\n", i+1, it)
	}
	fmt.Fprintln(a.out, "  0) 取消")
	for {
		if def >= 0 {
			fmt.Fprintf(a.out, "请选择 [%d]: ", def+1)
		} else {
			fmt.Fprint(a.out, "请选择: ")
		}
		s, err := a.readLine()
		if err != nil {
			return 0, err
		}
		if s == "" && def >= 0 {
			return def, nil
		}
		if s == "0" || s == "q" {
			return 0, errCancelled
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(items) {
			return n - 1, nil
		}
	}
}

// newReader 包一层缓冲；nil 输入返回 nil。
func newReader(r io.Reader) *bufio.Reader {
	if r == nil {
		return nil
	}
	return bufio.NewReader(r)
}
