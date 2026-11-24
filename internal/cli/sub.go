package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// cmdSub 管理 Clash 格式的订阅。
func (a *app) cmdSub(ctx context.Context, args []string) error {
	sub, rest, err := a.subcommand("sub", "add|list|nodes|note|test|refresh|rm", args)
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
		fmt.Fprintln(w, "订阅\t节点数\t最近拉取\t备注")
		for _, s := range subs {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", s.ID, len(s.Proxies), s.FetchedAt.Local().Format("2006-01-02 15:04:05"), orDash(s.Note))
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
		fs.StringVar(&opt.Network, "egress-network", "", "从哪个 Docker 网络发起，应与网关的出口网络相同（默认 "+model.DefaultEgressNetwork+"）")
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
		fmt.Fprintf(a.out, "订阅 %s 现有 %d 个节点\n", id, res.Total)
		if len(res.Updated) > 0 {
			fmt.Fprintf(a.out, "已更新的节点：%s；用到它们的网关在下次 gateway up / slot up 时应用\n", strings.Join(res.Updated, "、"))
		}
		if len(res.Renamed) > 0 {
			fmt.Fprintf(a.out, "订阅里改了名、已跟着改过来的节点：%s\n", strings.Join(res.Renamed, "、"))
		}
		if len(res.Missing) > 0 {
			fmt.Fprintf(a.out, "订阅里已经找不到、保持原样的节点：%s\n", strings.Join(res.Missing, "、"))
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
	fs := a.newFlags("sub add", "sub add <订阅> [--url-file 文件] [--note 备注]")
	urlFile := fs.String("url-file", "", "存放订阅地址的文件；不填则从标准输入读一行（地址里通常带令牌，不从命令行传）")
	note := fs.String("note", "", noteHelp)
	id, err := parseWithID(fs, args)
	if err != nil {
		return err
	}
	var url string
	switch {
	case *urlFile != "":
		b, err := os.ReadFile(*urlFile)
		if err != nil {
			return err
		}
		url = strings.TrimSpace(string(b))
	case a.interactive:
		if url, err = a.askSecret("订阅地址"); err != nil {
			return err
		}
	default:
		if url, err = a.readLine(); err != nil {
			return errors.New("没有读到订阅地址：用 --url-file，或从标准输入给出")
		}
	}
	return a.addSub(ctx, id, url, strings.TrimSpace(*note))
}

// addSub 登记订阅并报告结果。拉取订阅是本机直接访问订阅服务器。
func (a *app) addSub(ctx context.Context, id, url, note string) error {
	eng, _, err := a.open(ctx, false)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.errw, "正在从本机拉取订阅…")
	s, err := eng.SubAdd(ctx, id, url, note)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已登记订阅 %s：%d 个节点。用 sub nodes %s 查看，用 node add <节点> --role transit --sub %s 选用\n", id, len(s.Proxies), id, id)
	return nil
}
