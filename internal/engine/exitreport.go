package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// 出口评估：第三方对落地出口 IP 的看法（所在地、所属网络、是否被标成机房、代理、滥用来源等）。
// 只用于展示，不参与任何判断；取不到时槽位照常启动。
//
// 两个来源，都是检测页面背后的接口，没有公开的文档，哪天变了这里只会取不到：
//   - ip.net.coffee 的 /api/iprisk/<ip>：机房、代理、VPN、Tor、滥用标记，滥用分与信任分。
//   - meowvps.com 的 /api/ip-aggregator/<ip>：汇总了 MaxMind（用户类型、匿名网络标记）、
//     IP2Location（用途类型、代理标记、欺诈分）和 ip-api 的结果。这个接口只应答带着
//     它自己检测页面 Referer 的请求。
//
// 查询和出口探测一样经该槽的链路发出，不从宿主直连，所以对方看到的来源就是被查询的那个出口。
const (
	reportCoffeeURL   = "https://ip.net.coffee/api/iprisk/"
	reportMeowURL     = "https://meowvps.com/api/ip-aggregator/"
	reportMeowReferer = "https://meowvps.com/tools/ip-check/"
	reportSep         = "----cc-cluster-report----"
	// 同一个出口 IP 的评估在这段时间内不重复查询；只取到一个来源的，过了较短的时间就再试。
	exitReportTTL        = 24 * time.Hour
	exitReportPartialTTL = time.Hour
	// 启动槽位时顺带查询只等这么多秒：汇总接口第一次查一个新地址要十几秒，等不到就先用取到的，
	// 之后再补。手动重测时等得久一些。
	exitReportQuickWait  = 6
	exitReportManualWait = 20
)

// 评估里各家数据的名字。
const (
	srcCoffee = "ip.net.coffee"
	srcMeow   = "meowvps.com"
	byCoffee  = "ip.net.coffee"
	byMaxMind = "MaxMind"
	byIP2L    = "IP2Location"
	byIPAPI   = "ip-api"
)

// refreshExitReport 在需要时查询落地出口 ip 的评估并记在落地上。force 为 false 时（启动槽位时
// 顺带查询），已有同一个 IP 的、还没过期的结果就不再查，等待的时间也短。失败只返回错误，不改动落地。
func (e *Engine) refreshExitReport(ctx context.Context, rt *gatewayRuntime, link netplan.Link, landing *model.Node, ip string, force bool) error {
	if !force && !e.ExitReportAuto() {
		return nil
	}
	old := landing.ExitReport
	if old != nil && old.IP != ip {
		old = nil
	}
	wait := exitReportManualWait
	if !force {
		wait = exitReportQuickWait
		if old != nil {
			ttl := exitReportTTL
			if len(old.Sources) < 2 {
				ttl = exitReportPartialTTL
			}
			if time.Since(old.CheckedAt) < ttl {
				return nil
			}
		}
	}
	// ip 来自 parseExitIP，只可能是点分十进制的 IPv4。汇总接口每次都要十几秒，只在手动重测时查，
	// 与另一个同时发出；ip.net.coffee 失败时再试一次。末尾一段是两个 curl 的退出码，查不到时用来说明原因。
	proxy := fmt.Sprintf("socks5h://127.0.0.1:%d", link.ProbePort())
	meowCmd := "true"
	if force {
		meowCmd = fmt.Sprintf("curl -s -m %d -x %s -o \"$t\" -e '%s' '%s%s'", wait, proxy, reportMeowReferer, reportMeowURL, ip)
	}
	coffeeCmd := fmt.Sprintf("curl -s -m %d -x %s '%s%s'", wait, proxy, reportCoffeeURL, ip)
	script := fmt.Sprintf("t=$(mktemp); %[1]s & p=$!; %[2]s || { sleep 1; %[2]s; }; c=$?; echo; echo '%[3]s'; "+
		"wait $p; m=$?; cat \"$t\"; rm -f \"$t\"; echo; echo '%[3]s'; echo \"curl 退出码 ip.net.coffee=$c meowvps=$m\"",
		meowCmd, coffeeCmd, reportSep)
	out, err := e.sh(ctx, rt.name, script)
	if err == nil {
		coffee, rest, _ := strings.Cut(out, reportSep)
		meow, codes, _ := strings.Cut(rest, reportSep)
		var rep model.ExitReport
		if rep, err = buildExitReport(ip, coffee, meow); err != nil {
			err = fmt.Errorf("%w，%s", err, strings.TrimSpace(codes))
		} else {
			// 这次取到的来源比已有的还少（另一个接口暂时没应答）时，留着原来那份更完整的。
			if old != nil && len(rep.Sources) < len(old.Sources) && time.Since(old.CheckedAt) < exitReportTTL {
				e.logf("出口评估：这次只有 %s 应答，沿用 %s 的那份", strings.Join(rep.Sources, "、"), old.CheckedAt.Local().Format("01-02 15:04"))
				return nil
			}
			rep.CheckedAt = time.Now().UTC().Truncate(time.Second)
			landing.ExitReport = &rep
			if err = e.st.SaveNode(*landing, false); err == nil {
				e.logf("出口评估（%s）：%s", strings.Join(rep.Sources, "、"), ExitReportSummary(rep))
				if force && len(rep.Sources) < 2 {
					e.logf("提醒：有一个接口这次没有给出应答（%s）", strings.TrimSpace(codes))
				}
				return nil
			}
		}
	}
	e.logf("提醒：没有取到出口 %s 的评估（%v）；不影响使用", ip, err)
	return err
}

// RefreshExitReport 重新查询一个落地出口 IP 的评估。查询要经链路发出，
// 所以需要有一个使用该落地的槽位所在的网关正在运行。
func (e *Engine) RefreshExitReport(ctx context.Context, nodeID string) (rep *model.ExitReport, err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer func() { e.record("node.report", nodeID, err, "") }()

	landing, err := e.st.Node(nodeID)
	if err != nil {
		return nil, err
	}
	if landing.Role != model.RoleLanding {
		return nil, fmt.Errorf("节点 %s 不是落地：出口评估只针对落地", nodeID)
	}
	slots, err := e.st.Slots()
	if err != nil {
		return nil, err
	}
	var last error
	for _, sl := range slots {
		if sl.Landing != nodeID {
			continue
		}
		p, err := e.plan(sl.Gateway)
		if err != nil {
			last = err
			continue
		}
		rt, err := e.gatewayRuntime(ctx, p.gw)
		if err != nil {
			last = err
			continue
		}
		link := p.links[sl.ID]
		exit, err := e.probeChain(ctx, rt, link, p.gw.EchoURLs, false)
		if err != nil {
			last = fmt.Errorf("经槽位 %s 的链路探测出口没有成功：%w", sl.ID, err)
			continue
		}
		if err := e.refreshExitReport(ctx, rt, link, &landing, exit.IP, true); err != nil {
			return nil, err
		}
		return landing.ExitReport, nil
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("没有槽位在使用落地 %s：出口评估要经链路查询，先登记并启动一个使用它的槽位", nodeID)
}

// jsonObj 是解析后的 JSON 对象，带几个取值的办法：取不到或类型不对时返回零值。
type jsonObj map[string]any

func parseObj(body string) jsonObj {
	var m map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &m) != nil {
		return nil
	}
	return m
}

// coffeeCovers 判断 ip.net.coffee 的应答是不是针对 ip 的。它按网段缓存：问同一个 /24 里的另一个
// 地址时，答回来的 "ip" 可能是先前被问过的那个邻居（2026-11-20 实测），内容是整个网段的，
// 所以应答里的 "cidr" 包含 ip 也算数。
func coffeeCovers(c jsonObj, ip string) bool {
	if got, _ := parseExitIP(c.str("ip")); got == ip {
		return true
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	pfx, err := netip.ParsePrefix(c.str("cidr"))
	return err == nil && pfx.Bits() >= 16 && pfx.Contains(addr)
}

func (o jsonObj) obj(path ...string) jsonObj {
	for _, k := range path {
		next, _ := o[k].(map[string]any)
		o = next
	}
	return o
}

// str 取一段文字：去掉控制字符并截短，因为它会显示在终端和网页上。
func (o jsonObj) str(k string) string {
	s, _ := o[k].(string)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return truncate(strings.TrimSpace(s), 80)
}

func (o jsonObj) num(k string) (float64, bool) {
	v, ok := o[k].(float64)
	return v, ok
}

// flag 取一项标记：known 为 false 表示这家没有给出判断（字段不存在或为 null）。
// 标记不全是布尔值（例如命中时是爬虫的名字），非空、非 false、非 0 都算命中。
func (o jsonObj) flag(keys ...string) (on, known bool) {
	for _, k := range keys {
		switch v := o[k].(type) {
		case bool:
			on, known = on || v, true
		case string:
			on, known = on || (v != "" && v != "false" && v != "-"), true
		case float64:
			on, known = on || v != 0, true
		}
	}
	return on, known
}

// buildExitReport 把两个接口的应答合成一份评估。至少要有一个是针对 ip 的有效应答。
func buildExitReport(ip, coffeeBody, meowBody string) (model.ExitReport, error) {
	rep := model.ExitReport{IP: ip, Flags: []model.ExitFlag{}}
	names := []string{"机房", "代理", "住宅代理", "VPN", "Tor", "滥用"}
	flags := make([]model.ExitFlag, len(names))
	for i, n := range names {
		flags[i].Name = n
	}
	vote := func(i int, by string, on, known bool) {
		if !known {
			return
		}
		flags[i].Of++
		if on {
			flags[i].By = append(flags[i].By, by)
		}
	}

	if c := parseObj(coffeeBody); c != nil {
		if coffeeCovers(c, ip) {
			rep.Sources = append(rep.Sources, srcCoffee)
			rep.Country, rep.CountryCode, rep.Region, rep.City = c.str("country"), strings.ToUpper(c.str("countryCode")), c.str("region"), c.str("city")
			rep.ASOrg, rep.ASNKind, rep.Company, rep.CompanyType = c.str("asOrganization"), c.str("asn_kind"), c.str("company_name"), c.str("company_type")
			rep.AbuserScore = c.str("abuser_score")
			rep.Residential, _ = c.flag("isResidential")
			rep.Mobile, _ = c.flag("is_mobile")
			if tz := c.str("timezone"); model.CheckTZ(tz) == nil {
				rep.Timezone = tz
			}
			if v, ok := c.num("asn"); ok && v > 0 && v < 1<<32 {
				rep.ASN = int(v)
			}
			if v, ok := c.num("trust_score"); ok && v >= 0 && v <= 100 {
				n := int(v)
				rep.TrustScore = &n
			}
			for i, k := range []string{"is_datacenter", "is_proxy", "", "is_vpn", "is_tor", "is_abuser"} {
				if k != "" {
					on, known := c.flag(k)
					vote(i, byCoffee, on, known)
				}
			}
		}
	}

	if m := parseObj(meowBody); m != nil {
		if got, _ := parseExitIP(m.str("ip")); got == ip {
			rep.Sources = append(rep.Sources, srcMeow)
			core := m.obj("core_data")
			if rep.Country == "" {
				rep.Country, rep.CountryCode = core.str("country"), strings.ToUpper(core.str("country_code"))
			}
			if rep.ASOrg == "" {
				rep.ASOrg = core.str("as_name")
			}

			// MaxMind：匿名网络的几项在它没有数据时是 null，那时不算它给出了判断。
			traits, anon := m.obj("minfraud", "traits"), m.obj("minfraud", "anonymizer")
			rep.UserType, rep.ConnectionType = traits.str("user_type"), traits.str("connection_type")
			if rep.Timezone == "" {
				if tz := m.obj("minfraud", "location").str("time_zone"); model.CheckTZ(tz) == nil {
					rep.Timezone = tz
				}
			}
			if v, ok := traits.num("static_ip_score"); ok && v >= 0 {
				rep.StaticIPScore = &v
			}
			if v, ok := traits.num("user_count"); ok && v >= 0 && v < 1<<31 {
				n := int(v)
				rep.UserCount = &n
			}
			for i, k := range []string{"is_hosting_provider", "is_public_proxy", "is_residential_proxy", "is_anonymous_vpn", "is_tor_exit_node", ""} {
				if k != "" {
					on, known := anon.flag(k)
					vote(i, byMaxMind, on, known)
				}
			}

			// IP2Location。
			l := m.obj("api4", "ip2location")
			lp := l.obj("proxy")
			rep.UsageType = l.str("usage_type")
			if v, ok := l.num("fraud_score"); ok && v >= 0 && v <= 100 {
				n := int(v)
				rep.FraudScore = &n
			}
			for i, keys := range [][]string{{"is_data_center"}, {"is_public_proxy", "is_web_proxy"}, {"is_residential_proxy"}, {"is_vpn"}, {"is_tor"}, {"is_spammer", "is_botnet", "is_scanner"}} {
				on, known := lp.flag(keys...)
				vote(i, byIP2L, on, known)
			}

			// ip-api。
			a := m.obj("risk_assessment", "ipapi")
			on, known := a.flag("hosting")
			vote(0, byIPAPI, on, known)
			on, known = a.flag("proxy")
			vote(1, byIPAPI, on, known)
			if mob, ok := a.flag("mobile"); ok && mob {
				rep.Mobile = true
			}
		}
	}

	if len(rep.Sources) == 0 {
		return rep, errors.New("两个接口都没有给出针对这个地址的应答")
	}
	if !countryCodeRe.MatchString(rep.CountryCode) {
		rep.CountryCode = ""
	}
	for _, f := range flags {
		if len(f.By) > 0 {
			rep.Flags = append(rep.Flags, f)
		}
	}
	return rep, nil
}

// ExitReportSummary 把一份评估写成一行，例如
// "United States Dallas，NTT America, Inc.（hosting），信任分 77，欺诈分 0，标记：机房 3/4"。
func ExitReportSummary(r model.ExitReport) string {
	var parts []string
	if where := strings.TrimSpace(r.Country + " " + r.City); where != "" {
		parts = append(parts, where)
	}
	if org := firstNonEmpty(r.Company, r.ASOrg); org != "" {
		if kind := firstNonEmpty(r.UserType, r.CompanyType); kind != "" {
			org += "（" + kind + "）"
		}
		parts = append(parts, org)
	}
	if r.TrustScore != nil {
		parts = append(parts, fmt.Sprintf("信任分 %d", *r.TrustScore))
	}
	if r.FraudScore != nil {
		parts = append(parts, fmt.Sprintf("欺诈分 %d", *r.FraudScore))
	}
	if len(r.Flags) == 0 {
		return strings.Join(append(parts, "没有不利标记"), "，")
	}
	var fs []string
	for _, f := range r.Flags {
		fs = append(fs, fmt.Sprintf("%s %d/%d", f.Name, len(f.By), f.Of))
	}
	return strings.Join(append(parts, "标记（命中的家数/给出判断的家数）："+strings.Join(fs, "、")), "，")
}
