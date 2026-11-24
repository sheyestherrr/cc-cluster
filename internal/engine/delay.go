package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/mihomo"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/subscription"
)

// DefaultDelayURL 是测延迟时访问的地址：应答只有一个状态码，各地都能访问，
// 常见的代理客户端也用它。
const DefaultDelayURL = "https://www.gstatic.com/generate_204"

// DefaultDelayTimeout 是单个节点测延迟的时限，超过就算不通。
const DefaultDelayTimeout = 5 * time.Second

// DefaultChainDelayTimeout 是接上落地测整条链路时的时限。
const DefaultChainDelayTimeout = 8 * time.Second

// DelayOptions 是测延迟的选项，零值表示用默认值。
type DelayOptions struct {
	URL     string
	Timeout time.Duration
	// Network 是发起测速的 Docker 网络，应当与网关的出口网络相同，这样测到的就是网关连它的延迟。
	Network string
	// Landing 不为空时测的是整条链路：经订阅里的每个节点再到这个落地节点。
	// 节点自己能用不代表它肯转发到落地，挑中转时应当这样测。
	Landing string
	// Hosts 把域名固定到给出的地址（已登记节点的首跳地址），与网关里的做法一致。
	Hosts map[string]string
}

// NodeDelay 是订阅里一个节点的测速结果。
type NodeDelay struct {
	Name   string
	Type   string
	Server string
	// DelayMS 是经该节点访问测速地址的耗时（毫秒，含握手）；0 表示没测过，model.DelayFailed 表示不通。
	DelayMS int
	// Info 表示名称像订阅里的提示项（剩余流量、到期时间等），自动挑选时跳过。
	Info bool
	// Region 是从名称里认出的国家或地区（两位大写代码），认不出或是提示项时为空。
	Region string
	// Hash 是片段内容的指纹（model.ProxyHash），用来对上已经登记的节点。
	Hash string
}

// Usable 报告该节点是否测通。
func (d NodeDelay) Usable() bool { return d.DelayMS > 0 }

// DelayText 返回延迟的显示文本。
func (d NodeDelay) DelayText() string {
	switch {
	case d.DelayMS > 0:
		return fmt.Sprintf("%d ms", d.DelayMS)
	case d.DelayMS == model.DelayFailed:
		return "不通"
	}
	return "未测"
}

// SubDelays 返回订阅里各节点最近一次的测速结果：测通的按延迟从低到高排在前面，
// 其后是没测过的和不通的，各自保持订阅里的顺序。
func SubDelays(sub model.Subscription) []NodeDelay {
	res := make([]NodeDelay, len(sub.Proxies))
	for i, p := range sub.Proxies {
		n := model.Node{Proxy: p.Proxy}
		typ, _ := p.Proxy["type"].(string)
		res[i] = NodeDelay{
			Name: p.Name, Type: typ, Server: fmt.Sprintf("%s:%d", n.Server(), n.Port()),
			DelayMS: p.DelayMS, Info: subscription.IsInfoName(p.Name), Hash: model.ProxyHash(p.Proxy),
		}
		if !res[i].Info {
			res[i].Region = model.RegionFromName(p.Name)
		}
	}
	rank := func(d NodeDelay) int {
		switch {
		case d.DelayMS > 0:
			return 0
		case d.DelayMS == 0:
			return 1
		}
		return 2
	}
	sort.SliceStable(res, func(i, j int) bool {
		if ri, rj := rank(res[i]), rank(res[j]); ri != rj {
			return ri < rj
		}
		return res[i].DelayMS > 0 && res[i].DelayMS < res[j].DelayMS
	})
	return res
}

// Fastest 返回测通的节点里延迟最低的那个，跳过提示项。
func Fastest(delays []NodeDelay) (NodeDelay, bool) {
	for _, d := range delays { // 已按延迟排序
		if d.Usable() && !d.Info {
			return d, true
		}
	}
	return NodeDelay{}, false
}

// SubTest 对订阅里的全部节点测一次延迟并返回（排序同 SubDelays）。
// 只测节点本身时结果记在订阅上；指定了落地、测整条链路时结果只返回，不记录。
//
// 测速在一个一次性的容器里进行：用网关镜像里的同一个核心，接在出口网络上，
// 没有 TUN、没有任何能力，也不对外监听；节点配置经归档接口写入，不经过命令行。
func (e *Engine) SubTest(ctx context.Context, id string, opt DelayOptions) (res []NodeDelay, err error) {
	defer func() {
		ok := 0
		for _, d := range res {
			if d.Usable() {
				ok++
			}
		}
		detail := fmt.Sprintf("%d 个节点里 %d 个测通", len(res), ok)
		if opt.Landing != "" {
			detail += "（接落地 " + opt.Landing + "）"
		}
		e.record("sub.test", id, err, detail)
	}()
	sub, err := e.st.Sub(id)
	if err != nil {
		return nil, err
	}
	if len(sub.Proxies) == 0 {
		return nil, fmt.Errorf("订阅 %s 里没有节点", id)
	}
	var via map[string]any
	if opt.Landing != "" {
		landing, err := e.st.Node(opt.Landing)
		if err != nil {
			return nil, err
		}
		if landing.Role != model.RoleLanding {
			return nil, fmt.Errorf("节点 %s 的角色不是落地", landing.ID)
		}
		via = landing.Proxy
	}
	fragments := make([]map[string]any, len(sub.Proxies))
	for i, p := range sub.Proxies {
		fragments[i] = p.Proxy
	}
	if via != nil {
		e.logf("正在测订阅 %s 里 %d 个节点接落地 %s 的整条链路", id, len(fragments), opt.Landing)
	} else {
		e.logf("正在测订阅 %s 里 %d 个节点的延迟", id, len(fragments))
	}
	delays, err := e.measureDelays(ctx, fragments, via, opt)
	if err != nil {
		return nil, err
	}
	if via != nil {
		for i := range sub.Proxies {
			sub.Proxies[i].DelayMS = delays[i]
		}
		return SubDelays(sub), nil
	}

	// 测速期间不占着数据目录的锁；写回时重新读取，订阅在此期间被刷新过的话只更新仍然相同的节点。
	unlock, err := e.st.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	cur, err := e.st.Sub(id)
	if err != nil {
		return nil, err
	}
	for i := range cur.Proxies {
		for j, p := range sub.Proxies {
			if p.Name == cur.Proxies[i].Name && sameJSON(p.Proxy, cur.Proxies[i].Proxy) {
				cur.Proxies[i].DelayMS = delays[j]
				break
			}
		}
	}
	cur.TestedAt = time.Now().UTC()
	if err := e.st.SaveSub(cur, false); err != nil {
		return nil, err
	}
	return SubDelays(cur), nil
}

// measureDelays 返回与 proxies 一一对应的延迟（毫秒），不通的是 model.DelayFailed。
// via 不为空时每个节点后面接上这个落地，测整条链路。
func (e *Engine) measureDelays(ctx context.Context, proxies []map[string]any, via map[string]any, opt DelayOptions) ([]int, error) {
	if e.dk == nil {
		return nil, errors.New("测延迟需要连接 Docker")
	}
	target := opt.URL
	if target == "" {
		target = DefaultDelayURL
	}
	if u, err := url.Parse(target); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("测速地址 %q 不合法：应是 http 或 https 地址", target)
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultDelayTimeout
		if via != nil { // 两跳的握手更久
			timeout = DefaultChainDelayTimeout
		}
	}
	network := opt.Network
	if network == "" {
		network = model.DefaultEgressNetwork
	}
	image := e.cl.GatewayImage
	if has, err := e.dk.ImageExists(ctx, image); err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("本地没有网关镜像 %s；先执行 make images", image)
	}
	if err := e.ensureEgressNetwork(ctx, network); err != nil {
		return nil, err
	}
	config, names, err := mihomo.RenderDelayProbeHosts(proxies, via, opt.Hosts)
	if err != nil {
		return nil, err
	}

	id, err := e.dk.ContainerCreate(ctx, "", dockerapi.ContainerSpec{
		Image:      image,
		Entrypoint: []string{"mihomo"},
		Cmd:        []string{"-d", mihomo.HomeDir, "-f", mihomo.DelayConfigPath},
		Labels:     e.labels(roleHelper, ""),
		HostConfig: dockerapi.HostConfig{
			NetworkMode: network,
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
			Init:        dockerapi.Bool(true),
			PidsLimit:   512,
			LogConfig:   &dockerapi.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m"}},
		},
	})
	if err != nil {
		return nil, err
	}
	// 清理不随调用方的取消而中断：容器里有节点凭据，不能留下。
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		_ = e.dk.ContainerRemove(cctx, id)
	}()
	err = e.dk.CopyTo(ctx, id, mihomo.HomeDir, map[string]dockerapi.File{
		"delay.yaml": {Data: config, Mode: 0o600},
	})
	if err != nil {
		return nil, fmt.Errorf("写入测速配置：%w", err)
	}
	if err := e.dk.ContainerStart(ctx, id); err != nil {
		return nil, err
	}
	err = poll(ctx, 20*time.Second, 200*time.Millisecond, func() (bool, error) {
		c, err := e.dk.ContainerInspect(ctx, id)
		if err != nil {
			return false, err
		}
		if !c.State.Running {
			// 核心不接受配置时直接退出，多半是订阅里有它不认识的节点写法。
			stdout, stderr, _ := e.dk.ContainerLogs(ctx, id)
			return false, &configError{detail: lastLines(strings.TrimSpace(stdout+stderr), 3)}
		}
		_, err = e.sh(ctx, id, "test -S "+mihomo.SocketPath)
		var xe *exitError
		if errors.As(err, &xe) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return nil, fmt.Errorf("测速用的核心没有就绪：%w", err)
	}

	// 一次请求测完整组：核心并发地经每个节点访问测速地址，超时的不出现在结果里。
	query := url.Values{"url": {target}, "timeout": {fmt.Sprint(timeout.Milliseconds())}}
	out, err := e.sh(ctx, id, fmt.Sprintf("curl -sS -m %d --unix-socket %s 'http://localhost/group/%s/delay?%s'",
		int(timeout.Seconds())+10, mihomo.SocketPath, mihomo.DelayGroup, query.Encode()))
	if err != nil {
		return nil, fmt.Errorf("发起测速：%w", err)
	}
	got, err := parseDelays(out, names)
	if err != nil {
		return nil, err
	}
	return got, nil
}

// parseDelays 解析核心的整组测速应答：成功时是 "节点名 → 毫秒" 的对象，
// 全部不通时是带 message 的错误对象。
func parseDelays(body string, names []string) ([]int, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &m); err != nil {
		return nil, fmt.Errorf("测速应答无法解析：%s", truncate(strings.TrimSpace(body), 120))
	}
	res := make([]int, len(names))
	hit := false
	for i, name := range names {
		res[i] = model.DelayFailed
		if v, ok := m[name].(float64); ok && v > 0 {
			res[i], hit = int(v), true
		}
	}
	if _, isErr := m["message"]; !hit && !isErr && len(m) > 0 {
		return nil, fmt.Errorf("测速应答里没有认识的节点：%s", truncate(strings.TrimSpace(body), 120))
	}
	return res, nil
}
