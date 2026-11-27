// Package update 查询 GitHub 上发布的版本，并把正在运行的二进制换成其中一个。
//
// 它只碰两样东西：发布页（只读）和二进制所在的目录。数据目录不归它管：
// 这个包里没有读写数据目录的代码，也不接受数据目录的路径。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// Repo 是发布所在的 GitHub 仓库。
	Repo = "sheyestherrr/cc-cluster"
	// ReleasesURL 是给人看的发布页。
	ReleasesURL = "https://github.com/" + Repo + "/releases"
	// EnvNoCheck 设为非空时，网页界面不会自己去查新版本，只有使用者点了才查。
	EnvNoCheck = "CC_CLUSTER_NO_UPDATE_CHECK"

	defaultAPI  = "https://api.github.com"
	defaultSite = "https://github.com"

	// 等应答头最多这么久。
	headerTimeout = 30 * time.Second
)

// apiTimeout 是 Check 里问接口最多用的时间，剩下的留给不走接口的退路。测试时调短。
var apiTimeout = 10 * time.Second

// errNotFound 是请求的地址返回 404。
var errNotFound = errors.New("资源不存在（HTTP 404）")

// ValidVersion 在 v 不是发布版的版本号时返回说明格式的错误。
func ValidVersion(v string) error {
	if !IsRelease(v) {
		return fmt.Errorf("版本号 %q 无效，格式应为 vX.Y.Z（如 v0.0.3）", v)
	}
	return nil
}

// versionRe 与发布工作流认的标签写法一致：v主.次.修订，可以带预发布段（alpha.1、rc.1）。
var versionRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)

type version struct {
	num [3]int
	pre []string
}

func parseVersion(s string) (version, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	var v version
	for i := range v.num {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		v.num[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// IsRelease 报告 v 是不是一个发布版的版本号。从源码构建的二进制，版本号来自 git describe
// （一个提交号、v0.0.2-3-gabc1234 或 v0.0.2-dirty），不算：它们没法与发布的版本比较，也不该被自动换掉。
func IsRelease(v string) bool {
	p, ok := parseVersion(v)
	return ok && !(len(p.pre) == 1 && p.pre[0] == "dirty")
}

// Compare 按语义化版本比较两个版本号：a 更新时为正，更旧时为负。写法不对的排在所有版本之前。
func Compare(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range pa.num {
		if d := pa.num[i] - pb.num[i]; d != 0 {
			return d
		}
	}
	// 带预发布段的排在同号的正式版之前。
	switch {
	case len(pa.pre) == 0 && len(pb.pre) == 0:
		return 0
	case len(pa.pre) == 0:
		return 1
	case len(pb.pre) == 0:
		return -1
	}
	for i := 0; i < len(pa.pre) && i < len(pb.pre); i++ {
		x, y := pa.pre[i], pb.pre[i]
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				return nx - ny
			}
		case ex == nil:
			return -1 // 数字段排在字母段之前
		case ey == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return len(pa.pre) - len(pb.pre)
}

// Newer 报告 candidate 是否比 current 新；任何一个不是合法的版本号时为假。
func Newer(candidate, current string) bool {
	_, ok1 := parseVersion(candidate)
	_, ok2 := parseVersion(current)
	return ok1 && ok2 && Compare(candidate, current) > 0
}

// Release 是一个发布的版本。
type Release struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	// URL 是这个版本的发布页。
	URL string `json:"url"`
	// Notes 是发布页上写的更新说明（Markdown）；只查到版本号时为空。
	Notes string `json:"notes,omitempty"`
}

// Client 访问发布页。零值不可用，用 NewClient 创建。
type Client struct {
	HTTP *http.Client
	// API 与 Site 是接口和下载的地址，测试时换成本地的。
	API  string
	Site string
	// UserAgent 随请求发出，里面有本程序的版本号。
	UserAgent string
}

// NewClient 创建访问 GitHub 的客户端；current 是本程序的版本号，只用在 User-Agent 里。
// 代理按环境变量 HTTPS_PROXY 等的设置走。
func NewClient(current string) *Client {
	hc := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("重定向次数过多")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("拒绝从 HTTPS 重定向到 HTTP")
		}
		return nil
	}}
	// 对方接了连接却迟迟不应答时不无限等下去；应答开始之后的时限由调用方的 context 管。
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = tr.Clone()
		tr.ResponseHeaderTimeout = headerTimeout
		hc.Transport = tr
	}
	return &Client{HTTP: hc, API: defaultAPI, Site: defaultSite, UserAgent: "cc-cluster/" + current}
}

// requestError 把一次请求的失败写成给人看的一句：超时和取消单独说，其余去掉标准库加的“Get "地址":”前缀。
func requestError(host string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("请求 %s 超时", host)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("请求 %s 已取消", host)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("请求 %s 失败：%w", host, err)
}

// get 发一个 GET 请求，只接受 200；调用方负责关闭 Body。
func (c *Client) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, requestError(req.URL.Host, err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w：%s", errNotFound, url)
	case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, errors.New("GitHub API 请求次数已达上限，请稍后重试")
	}
	return nil, fmt.Errorf("%s 返回 HTTP %d", req.URL.Host, resp.StatusCode)
}

// ghRelease 是 GitHub 接口里一个 Release 的字段。
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// release 只在标签是合法的版本号时调用。发布页的地址由版本号拼出，不采用接口返回的那个：
// 它会被做成链接给人点。
func (g ghRelease) release() Release {
	return Release{Version: g.TagName, PublishedAt: g.PublishedAt, URL: ReleasesURL + "/tag/" + g.TagName, Notes: cleanNotes(g.Body)}
}

// cleanNotes 统一换行，并去掉换行和制表符以外的控制字符：更新说明会原样打印到终端，
// 里面的转义序列不该有机会改写屏幕上的内容。
func cleanNotes(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.get(ctx, c.API+"/repos/"+Repo+path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("无法解析 GitHub 的响应：%w", err)
	}
	return nil
}

// Releases 返回最近的正式版，新的在前，带更新说明。草稿、预发布和标签写法不对的不算。
func (c *Client) Releases(ctx context.Context) ([]Release, error) {
	var list []ghRelease
	if err := c.getJSON(ctx, "/releases?per_page=30", &list); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, g := range list {
		if g.Draft || g.Prerelease || !IsRelease(g.TagName) {
			continue
		}
		out = append(out, g.release())
	}
	sort.SliceStable(out, func(i, j int) bool { return Compare(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

// Check 是一次查询的结果。
type Check struct {
	// Latest 是最新的正式版。
	Latest Release `json:"latest"`
	// Newer 表示 Latest 比查询时给的版本新。给的不是发布版的版本号（从源码构建的）时总是假。
	Newer bool `json:"newer"`
	// Releases 是值得给使用者看的更新说明，新的在前：有新版本时是比现在新的各个版本，
	// 已经是最新时是现在这个版本的那一份。接口用不了、只查到版本号时，里面的说明是空的。
	Releases []Release `json:"releases"`
}

// Check 查出最新的正式版，以及从 current 升到它要经过的各个版本。
// 接口用不了（多半是被限流）时改从发布页的跳转里读版本号，这样得到的结果没有发布时间和更新说明。
func (c *Client) Check(ctx context.Context, current string) (Check, error) {
	// 接口单独限时：它把 ctx 的时限耗尽的话，后面的退路一开始就已经超时了。
	actx, cancel := context.WithTimeout(ctx, apiTimeout)
	list, err := c.Releases(actx)
	cancel()
	if err != nil || len(list) == 0 {
		tag, terr := c.latestTag(ctx)
		if terr != nil {
			if err != nil {
				return Check{}, err
			}
			return Check{}, terr
		}
		latest := Release{Version: tag, URL: ReleasesURL + "/tag/" + tag}
		return Check{Latest: latest, Newer: Newer(tag, current), Releases: []Release{latest}}, nil
	}
	out := Check{Latest: list[0], Newer: Newer(list[0].Version, current), Releases: []Release{}}
	for _, r := range list {
		if (out.Newer && Newer(r.Version, current)) || (!out.Newer && r.Version == current) {
			out.Releases = append(out.Releases, r)
		}
	}
	if len(out.Releases) == 0 {
		out.Releases = append(out.Releases, out.Latest)
	}
	return out, nil
}

// latestTag 读 releases/latest 的跳转目标：地址的最后一段就是最新正式版的标签。不走接口，不受接口的限流影响。
func (c *Client) latestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Site+"/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return "", requestError(req.URL.Host, err)
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	tag := loc[strings.LastIndex(loc, "/")+1:]
	if resp.StatusCode/100 != 3 || !IsRelease(tag) {
		return "", errors.New("未找到已发布的版本")
	}
	return tag, nil
}
