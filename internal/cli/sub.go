package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
)

// cmdSub 管理订阅：远程的订阅地址，或者贴进来的订阅内容与分享链接。
func (a *app) cmdSub(ctx context.Context, args []string) error {
	sub, rest, err := a.subcommand("sub", "add|list|nodes|note|set|test|refresh|rm", args)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.subAdd(ctx, rest)
	case "note":
		return a.setNote(engine.KindSub, "sub", "订阅", rest)
	case "list", "ls":
		if err := parse(a.newFlags("sub list", "sub list"), rest); err != nil {
			return err
		}
		_, st, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		subs, err := st.Subs()
		if err != nil {
			return err
		}
		w := a.table()
		fmt.Fprintln(w, "订阅\t来源\t节点数\t最近更新\t自动刷新\t状态\t备注")
		for _, s := range subs {
			source, every := "远程", engine.EveryText(s)
			if s.Local() {
				source, every = "本地", "-"
			}
			status := "正常"
			if s.LastError != "" {
				status = fmt.Sprintf("%s 刷新失败：%s", s.CheckedAt.Local().Format("01-02 15:04"), s.LastError)
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n", s.ID, source, len(s.Proxies), s.FetchedAt.Local().Format("2006-01-02 15:04:05"), every, status, orDash(s.Note))
		}
		return w.Flush()
	case "nodes":
		id, err := parseWithID(a.newFlags("sub nodes", "sub nodes <订阅>"), rest)
		if err != nil {
			return err
		}
		_, st, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		s, err := st.Sub(id)
		if err != nil {
			return err
		}
		w := a.table()
		fmt.Fprintln(w, "序号\t名称\t类型\t服务器\t延迟")
		for i, d := range engine.SubDelays(s) {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", i+1, d.Name, d.Type, d.Server, d.DelayText())
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if s.TestedAt.IsZero() {
			fmt.Fprintf(a.out, "还没有测过延迟；用 sub test %s 测一遍\n", id)
		} else {
			fmt.Fprintf(a.out, "延迟测于 %s；用 sub test %s 重测\n", s.TestedAt.Local().Format("2006-01-02 15:04:05"), id)
		}
		return nil
	case "test":
		fs := a.newFlags("sub test", "sub test <订阅> [参数]")
		var opt engine.DelayOptions
		fs.StringVar(&opt.URL, "url", "", "测速时访问的地址（默认 "+engine.DefaultDelayURL+"）")
		fs.DurationVar(&opt.Timeout, "timeout", 0, "单个节点的时限，超过算不通（默认 5s，接落地时 8s）")
		fs.StringVar(&opt.Network, "egress-network", "", "从哪个 Docker 网络发起，应与网关的出口网络相同（默认 <容器名前缀>-egress）")
		fs.StringVar(&opt.Landing, "landing", "", "接上这个落地节点测整条链路：节点自己能用不代表它肯转发到落地，挑中转时用它")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		delays, err := a.testSub(ctx, id, opt)
		if err != nil {
			return err
		}
		w := a.table()
		fmt.Fprintln(w, "延迟\t名称\t类型\t服务器")
		ok := 0
		for _, d := range delays {
			if d.Usable() {
				ok++
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.DelayText(), d.Name, d.Type, d.Server)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%d 个节点里 %d 个测通", len(delays), ok)
		if opt.Landing != "" {
			fmt.Fprintf(a.out, "（经节点再到落地 %s 的整条链路）", opt.Landing)
		}
		if best, has := engine.Fastest(delays); has {
			fmt.Fprintf(a.out, "；延迟最低的是 %s（%s）", best.Name, best.DelayText())
		}
		fmt.Fprintln(a.out)
		return nil
	case "refresh":
		id, err := parseWithID(a.newFlags("sub refresh", "sub refresh <订阅>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		res, err := eng.SubRefresh(ctx, id)
		if err != nil {
			return err
		}
		a.printRefresh(id, res)
		return nil
	case "set":
		fs := a.newFlags("sub set", "sub set <订阅> [--url-file 文件 | --file 文件] [--append] [--every 间隔]")
		urlFile := fs.String("url-file", "", urlFileHelp)
		file := fs.String("file", "", contentFileHelp)
		appendTo := fs.Bool("append", false, "把 --file 的分享链接接在本地订阅现有内容的后面，而不是整体替换")
		every := fs.String("every", "", "远程订阅自动刷新的间隔，例如 12h、30m；off 表示不自动刷新。自动刷新在 cc-cluster web 或 cc-cluster watch 运行期间进行")
		id, err := parseWithID(fs, rest)
		if err != nil {
			return err
		}
		var interval time.Duration
		if *every != "" && *every != "off" {
			if interval, err = time.ParseDuration(*every); err != nil || interval <= 0 {
				return fmt.Errorf("--every 的写法不对：%q；用 12h、30m 这样的间隔，或 off", *every)
			}
		}
		eng, _, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		if *urlFile != "" || *file != "" || *every == "" {
			in, err := a.subInput(*urlFile, *file, false)
			if err != nil {
				return err
			}
			in.Append = *appendTo
			res, err := eng.SubSet(ctx, id, in)
			if err != nil {
				return err
			}
			a.printRefresh(id, res)
		}
		if *every != "" {
			if err := eng.SubSetEvery(id, interval); err != nil {
				return err
			}
			if interval == 0 {
				fmt.Fprintf(a.out, "订阅 %s 不再自动刷新\n", id)
			} else {
				fmt.Fprintf(a.out, "订阅 %s 每 %s 自动刷新一次（在 cc-cluster web 或 cc-cluster watch 运行期间）\n", id, interval)
			}
		}
		return nil
	case "rm":
		id, err := parseWithID(a.newFlags("sub rm", "sub rm <订阅>"), rest)
		if err != nil {
			return err
		}
		eng, _, err := a.open(ctx, false)
		if err != nil {
			return err
		}
		return eng.SubRemove(id)
	}
	fmt.Fprintf(a.errw, "未知的 sub 子命令 %q\n", sub)
	return errUsage
}

func (a *app) subAdd(ctx context.Context, args []string) error {
	fs := a.newFlags("sub add", "sub add <订阅> [--url-file 文件 | --file 文件] [--note 备注]")
	urlFile := fs.String("url-file", "", urlFileHelp)
	file := fs.String("file", "", contentFileHelp)
	note := fs.String("note", "", noteHelp)
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	in, err := a.subInput(*urlFile, *file, true)
	if err != nil {
		return err
	}
	return a.addSub(ctx, id, strings.TrimSpace(*note), in)
}

const (
	urlFileHelp     = "远程订阅：存放订阅地址的文件（地址里通常带令牌，不从命令行传）"
	contentFileHelp = "本地订阅：存放订阅内容的文件，一行一个分享链接（ss、vmess、vless、trojan、hysteria2、socks5、http），或者 base64 订阅、Clash 配置；写 - 表示从标准输入读"
)

// subInput 按参数读出订阅的来源。两个文件都没给时：askURL 为真就问（或从标准输入读一行）订阅地址，否则报错。
func (a *app) subInput(urlFile, file string, askURL bool) (in engine.SubInput, err error) {
	switch {
	case urlFile != "" && file != "":
		return in, errors.New("--url-file 和 --file 只能用一个")
	case urlFile != "":
		b, err := os.ReadFile(urlFile)
		if err != nil {
			return in, err
		}
		in.URL = strings.TrimSpace(string(b))
	case file == "-":
		if a.in == nil {
			return in, errors.New("没有标准输入可读")
		}
		b, err := io.ReadAll(io.LimitReader(a.in, engine.MaxSubContent+1))
		if err != nil {
			return in, err
		}
		in.Content = string(b)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return in, err
		}
		in.Content = string(b)
	case !askURL:
		return in, errors.New("用 --url-file 给出新的订阅地址，或用 --file 给出新的订阅内容")
	case a.interactive:
		if in.URL, err = a.askSecret("订阅地址"); err != nil {
			return in, err
		}
	default:
		if in.URL, err = a.readLine(); err != nil {
			return in, errors.New("没有读到订阅地址：用 --url-file 给出地址，或用 --file 给出订阅内容")
		}
	}
	return in, nil
}

// addSub 登记订阅并报告结果。拉取远程订阅是本机直接访问订阅服务器。
func (a *app) addSub(ctx context.Context, id, note string, in engine.SubInput) error {
	eng, _, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	if in.URL != "" {
		fmt.Fprintln(a.errw, "正在从本机拉取订阅…")
	}
	s, err := eng.SubAdd(ctx, id, note, in)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已登记订阅 %s：%d 个节点%s。用 sub nodes %s 查看，用 node add <节点> --role transit --sub %s 选用\n", id, len(s.Proxies), skippedText(s.Skipped), id, id)
	return nil
}

func skippedText(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("（另有 %d 行没能解析：协议不支持或写法不对）", n)
}

// printRefresh 报告刷新或修改订阅之后的结果。
func (a *app) printRefresh(id string, res engine.RefreshResult) {
	fmt.Fprintf(a.out, "订阅 %s 现有 %d 个节点%s\n", id, res.Total, skippedText(res.Skipped))
	if len(res.Updated) > 0 {
		fmt.Fprintf(a.out, "已更新的节点：%s；用到它们的网关在下次 gateway up / slot up 时应用\n", strings.Join(res.Updated, "、"))
	}
	if len(res.Renamed) > 0 {
		fmt.Fprintf(a.out, "订阅里改了名、已跟着改过来的节点：%s\n", strings.Join(res.Renamed, "、"))
	}
	if len(res.Missing) > 0 {
		fmt.Fprintf(a.out, "订阅里已经找不到、保持原样的节点：%s\n", strings.Join(res.Missing, "、"))
	}
}
