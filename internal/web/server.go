// Package web 提供 cc-cluster 的网页界面：一个只监听本机的 HTTP 服务，
// 由嵌入在二进制里的前端（web/ 目录的构建产物）和一组 JSON 接口组成。
//
// 接口与命令行做的是同一件事，都经 internal/engine；每个请求各自打开数据目录，
// 互斥仍由数据目录的锁保证，所以网页和命令行可以同时使用。
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/engine"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

// dist 是前端的构建产物。仓库里只有一个占位文件，执行 make web 之后才有真正的页面；
// 发布版在构建二进制之前先构建前端。
//
//go:embed all:dist
var distFS embed.FS

// HasUI 报告这个二进制里是否带有前端页面。
func HasUI() bool {
	_, err := fs.Stat(distFS, "dist/index.html")
	return err == nil
}

// TokenHeader 是携带访问令牌的请求头。浏览器的跨站请求带不上自定义请求头，
// 所以它同时挡住了别的网页对本机接口的调用。
const TokenHeader = "X-CC-Token"

// opTimeout 是单个接口请求的时限；启动槽位要拉起网关并做几轮探测。
const opTimeout = 10 * time.Minute

// Options 是网页服务的设置。
type Options struct {
	DataDir string
	Version string
	// Assets 替换嵌入的前端，测试用。
	Assets fs.FS
	// NoWatch 关闭服务运行期间的持续检测；WatchInterval 是它的间隔，0 用默认值。
	NoWatch       bool
	WatchInterval time.Duration
	// NoSample 关闭服务运行期间对各槽位流量与连接的采样，测试用。
	NoSample bool
}

// Server 是网页服务。
type Server struct {
	opt    Options
	token  string
	assets fs.FS
	// traffic 是采样循环在内存里的状态：各槽位此刻的速率与连接。
	traffic *engine.Traffic
}

// New 创建网页服务并生成这一次运行的访问令牌。
func New(opt Options) (*Server, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	s := &Server{opt: opt, token: hex.EncodeToString(b), assets: opt.Assets, traffic: engine.NewTraffic()}
	if s.assets == nil {
		sub, err := fs.Sub(distFS, "dist")
		if err != nil {
			return nil, err
		}
		s.assets = sub
	}
	return s, nil
}

// Token 返回访问令牌。它只应出现在打印给用户的地址里（放在 # 之后，不会发给服务器）。
func (s *Server) Token() string { return s.token }

// Serve 在 ln 上提供服务，直到 ctx 结束。ln 必须监听在回环地址上。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return errors.New("网页界面只能监听本机回环地址")
	}
	srv := &http.Server{Handler: s.Handler(addr.Port), ReadHeaderTimeout: 10 * time.Second}
	open := func(ctx context.Context) (*engine.Engine, error) {
		st, err := store.Open(s.opt.DataDir)
		if err != nil {
			return nil, err
		}
		dk, err := dockerapi.New(ctx, "")
		if err != nil {
			return nil, err
		}
		return engine.New(st, dk, io.Discard)
	}
	// 网页开着的时候顺带做持续检测：隔一段时间看一眼各槽的连通性与出口，出口变了就断网保持。
	if !s.opt.NoWatch {
		go engine.Watch(ctx, s.opt.WatchInterval, open)
	}
	// 也顺带采样各槽位的流量与连接：界面上的实时数和历史趋势都从这里来。
	sampled := make(chan struct{})
	if s.opt.NoSample {
		close(sampled)
	} else {
		go func() {
			defer close(sampled)
			engine.SampleLoop(ctx, s.traffic, 0, open)
		}()
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		case <-done:
		}
	}()
	err := srv.Serve(ln)
	close(done)
	if ctx.Err() != nil {
		// 等采样循环把还没落盘的那一段写完；等不到也不要紧，下次启动时按计数补上。
		select {
		case <-sampled:
		case <-time.After(3 * time.Second):
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Handler 返回 HTTP 处理器。port 是监听端口，用来核对 Host 头。
func (s *Server) Handler(port int) http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, withDocker bool, fn func(*call) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { s.serveAPI(w, r, withDocker, fn) })
	}
	api("GET /api/state", false, s.state)
	api("GET /api/doctor", true, func(c *call) (any, error) { return checks(c.eng.Doctor(c.ctx)), nil })
	api("GET /api/ops", false, s.ops)
	api("POST /api/settings/exit-report", false, func(c *call) (any, error) {
		var in struct {
			Auto bool `json:"auto"`
		}
		if err := c.body(&in); err != nil {
			return nil, err
		}
		return nil, c.eng.SetExitReportAuto(in.Auto)
	})

	// 备注：四类对象同一种写法，note 为空表示清掉。
	for kind, prefix := range map[string]string{
		engine.KindSub: "subs", engine.KindNode: "nodes", engine.KindGateway: "gateways", engine.KindSlot: "slots",
	} {
		api("POST /api/"+prefix+"/{id}/note", false, func(c *call) (any, error) {
			var in struct {
				Note string `json:"note"`
			}
			if err := c.body(&in); err != nil {
				return nil, err
			}
			return nil, c.eng.SetNote(kind, c.id, in.Note)
		})
	}

	api("POST /api/subs", false, s.subAdd)
	api("GET /api/subs/{id}/nodes", false, s.subNodes)
	api("POST /api/subs/{id}/refresh", false, func(c *call) (any, error) { return c.eng.SubRefresh(c.ctx, c.id) })
	api("POST /api/subs/{id}/source", false, s.subSet)
	api("POST /api/subs/{id}/every", false, func(c *call) (any, error) {
		var in struct {
			Minutes int `json:"minutes"`
		}
		if err := c.body(&in); err != nil {
			return nil, err
		}
		if in.Minutes < 0 {
			return nil, errors.New("间隔不能是负数")
		}
		return nil, c.eng.SubSetEvery(c.id, time.Duration(in.Minutes)*time.Minute)
	})
	api("POST /api/subs/{id}/test", true, s.subTest)
	api("DELETE /api/subs/{id}", false, func(c *call) (any, error) { return nil, c.eng.SubRemove(c.id) })

	api("POST /api/nodes", false, s.nodeAdd)
	api("POST /api/nodes/{id}/resolve", false, func(c *call) (any, error) {
		ip, err := c.eng.ResolveNode(c.ctx, c.id)
		return map[string]string{"connect_ipv4": ip}, err
	})
	api("POST /api/nodes/{id}/report", true, func(c *call) (any, error) { return c.eng.RefreshExitReport(c.ctx, c.id) })
	api("DELETE /api/nodes/{id}", false, func(c *call) (any, error) { return nil, c.eng.RemoveNode(c.id) })

	api("POST /api/gateways", false, s.gatewayAdd)
	api("POST /api/gateways/{id}/up", true, func(c *call) (any, error) { return nil, c.eng.GatewayUp(c.ctx, c.id) })
	api("POST /api/gateways/{id}/down", true, func(c *call) (any, error) {
		var in struct {
			Force bool `json:"force"`
		}
		if err := c.body(&in); err != nil {
			return nil, err
		}
		return nil, c.eng.GatewayDown(c.ctx, c.id, in.Force)
	})
	api("POST /api/gateways/{id}/transits", true, func(c *call) (any, error) {
		var in struct {
			Transits  []string `json:"transits"`
			SkipCheck bool     `json:"skip_check"`
		}
		if err := c.body(&in); err != nil {
			return nil, err
		}
		res, err := c.eng.SetGatewayTransits(c.ctx, c.id, in.Transits, engine.TransitOptions{SkipChainCheck: in.SkipCheck})
		if err != nil {
			return nil, err
		}
		return map[string]any{"reloaded": res.Reloaded, "rechecked": orEmpty(res.Rechecked), "failed": res.Failed}, nil
	})
	api("DELETE /api/gateways/{id}", true, func(c *call) (any, error) { return nil, c.eng.RemoveGateway(c.ctx, c.id) })

	api("POST /api/slots", false, s.slotAdd)
	api("POST /api/slots/{id}/up", true, s.slotUp)
	api("POST /api/slots/{id}/stop", true, func(c *call) (any, error) { return nil, c.eng.SlotStop(c.ctx, c.id) })
	api("POST /api/slots/{id}/hold", true, func(c *call) (any, error) {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := c.body(&in); err != nil {
			return nil, err
		}
		return nil, c.eng.SlotHold(c.ctx, c.id, in.Reason)
	})
	api("POST /api/slots/{id}/release", false, func(c *call) (any, error) { return nil, c.eng.SlotRelease(c.id) })
	api("DELETE /api/slots/{id}", true, func(c *call) (any, error) {
		return nil, c.eng.SlotRemove(c.ctx, c.id, c.r.URL.Query().Get("purge") == "1")
	})

	// 流量与连接：实时的数来自这个进程里的采样循环，历史来自数据目录。
	api("GET /api/traffic", false, func(c *call) (any, error) { return s.traffic.Snapshot(), nil })
	api("GET /api/slots/{id}/connections", false, func(c *call) (any, error) {
		if _, err := c.st.Slot(c.id); err != nil {
			return nil, err
		}
		return s.traffic.Conns(c.id), nil
	})
	api("GET /api/history", false, func(c *call) (any, error) { return c.eng.History(time.Now(), hoursParam(c.r)) })
	api("GET /api/slots/{id}/connlog", false, func(c *call) (any, error) {
		limit, _ := strconv.Atoi(c.r.URL.Query().Get("limit"))
		return c.eng.ConnLog(c.id, time.Now(), hoursParam(c.r), limit)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "没有这个接口"})
	})

	files := http.FileServerFS(s.assets)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		files.ServeHTTP(w, r)
	})

	hosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", port): true,
		fmt.Sprintf("localhost:%d", port): true,
		fmt.Sprintf("[::1]:%d", port):     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// 只认本机的名字：别的域名即使被解析到 127.0.0.1（DNS 重绑定），请求也到不了接口。
		if !hosts[r.Host] {
			http.Error(w, "只接受对本机地址的访问", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// call 是一次接口调用的上下文。
type call struct {
	ctx context.Context
	r   *http.Request
	id  string // 路径里的对象 ID
	eng *engine.Engine
	st  *store.Store
	log *bytes.Buffer
}

// body 把请求体解析到 v；没有请求体时保持 v 不变。
func (c *call) body(v any) error {
	data, err := io.ReadAll(io.LimitReader(c.r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return errors.New("请求内容不是合法的 JSON")
	}
	return nil
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request, withDocker bool, fn func(*call) (any, error)) {
	w.Header().Set("Cache-Control", "no-store")
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(TokenHeader)), []byte(s.token)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "访问令牌不对：请用 cc-cluster web 打印出来的地址打开"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), opTimeout)
	defer cancel()
	c := &call{ctx: ctx, r: r, id: r.PathValue("id"), log: &bytes.Buffer{}}
	var err error
	if c.st, err = store.Open(s.opt.DataDir); err == nil {
		var dk *dockerapi.Client
		if withDocker {
			dk, err = dockerapi.New(ctx, "")
		}
		if err == nil {
			c.eng, err = engine.New(c.st, dk, c.log)
		}
	}
	var out any
	if err == nil {
		out, err = fn(c)
	}
	lines := splitLines(c.log.String())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "log": lines})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "log": lines})
}

// hoursParam 读出查询串里的 hours（往回看多少小时）；没有或写得不对时是 0，由引擎取默认值。
func hoursParam(r *http.Request) time.Duration {
	h, err := strconv.Atoi(r.URL.Query().Get("hours"))
	if err != nil || h <= 0 {
		return 0
	}
	return time.Duration(h) * time.Hour
}

func splitLines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// ---- 读取现状 ----

type nodeView struct {
	Direct       bool              `json:"direct,omitempty"`
	ID           string            `json:"id"`
	Note         string            `json:"note,omitempty"`
	Role         string            `json:"role"`
	Type         string            `json:"type"`
	Server       string            `json:"server"`
	Port         int               `json:"port"`
	ConnectIPv4  string            `json:"connect_ipv4,omitempty"`
	ConnectAuto  bool              `json:"connect_auto,omitempty"`
	FakeIP       bool              `json:"fake_ip,omitempty"`
	ExpectExitIP string            `json:"expect_exit_ip,omitempty"`
	TZ           string            `json:"tz,omitempty"`
	TZAuto       bool              `json:"tz_auto,omitempty"`
	TZFallback   bool              `json:"tz_fallback,omitempty"`
	ExitReport   *model.ExitReport `json:"exit_report,omitempty"`
	Source       *model.NodeSource `json:"source,omitempty"`
	UsedBy       []string          `json:"used_by"`
	// Region 是国家或地区代码；RegionFrom 说明它来自出口评估（exit）还是从名称里认出来的（name）。
	Region     string `json:"region,omitempty"`
	RegionFrom string `json:"region_from,omitempty"`
}

type subView struct {
	ID        string     `json:"id"`
	Note      string     `json:"note,omitempty"`
	Count     int        `json:"count"`
	FetchedAt time.Time  `json:"fetched_at"`
	TestedAt  *time.Time `json:"tested_at,omitempty"`
	Usable    int        `json:"usable"`
	// Local 表示内容是贴进来的（没有地址可拉取）；地址和内容本身不随现状返回。
	Local bool `json:"local,omitempty"`
	// Format 是 clash 或 links；Skipped 是分享链接里没能解析的行数。
	Format  string `json:"format,omitempty"`
	Skipped int    `json:"skipped,omitempty"`
	// EveryMin 是自动刷新的间隔（分钟），0 表示只手动刷新；CheckedAt 是最近一次尝试的时间，
	// LastError 是那次失败的原因。
	EveryMin  int        `json:"every_min,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

type gatewayView struct {
	ID            string   `json:"id"`
	Note          string   `json:"note,omitempty"`
	Transit       string   `json:"transit,omitempty"`
	Backups       []string `json:"transit_backups,omitempty"`
	ActiveTransit string   `json:"active_transit,omitempty"`
	EgressNetwork string   `json:"egress_network"`
	Pool          string   `json:"pool"`
	Container     string   `json:"container"`
	CoreReady     bool     `json:"core_ready"`
	ConfigCurrent bool     `json:"config_current"`
	Slots         []string `json:"slots"`
}

type slotView struct {
	ID         string        `json:"id"`
	Note       string        `json:"note,omitempty"`
	Gateway    string        `json:"gateway"`
	Landing    string        `json:"landing"`
	State      string        `json:"state"`
	HoldReason string        `json:"hold_reason,omitempty"`
	ExitIP     string        `json:"exit_ip,omitempty"`
	ExitWhere  string        `json:"exit_where,omitempty"`
	AppliedAt  *time.Time    `json:"applied_at,omitempty"`
	Hostname   string        `json:"hostname"`
	User       string        `json:"user"`
	Home       string        `json:"home"`
	TZ         string        `json:"tz,omitempty"`
	Mounts     []model.Mount `json:"mounts"`
	Memory     int64         `json:"memory_bytes,omitempty"`
	CPUs       float64       `json:"cpus,omitempty"`
	FakeDMI    bool          `json:"fake_dmi,omitempty"`
	FakeProc   bool          `json:"fake_proc,omitempty"`
	// TmuxBridge 为真时，槽内可以经宿主 tmux 执行不受该槽网络约束的宿主命令；页面要明确显示。
	TmuxBridge bool `json:"tmux_bridge,omitempty"`
	TmuxAlive  bool `json:"tmux_alive,omitempty"`
	// Watch 是持续检测最近一次的结果。
	Watch *model.Watch `json:"watch,omitempty"`
	// EnterCommand 是在终端里进入这个槽位的命令；网页不提供终端。
	EnterCommand string `json:"enter_command"`
}

type stateView struct {
	Version      string        `json:"version"`
	DataDir      string        `json:"data_dir"`
	Cluster      model.Cluster `json:"cluster"`
	DockerError  string        `json:"docker_error,omitempty"`
	LandingLimit int           `json:"landing_limit"`
	Gateways     []gatewayView `json:"gateways"`
	Slots        []slotView    `json:"slots"`
	Nodes        []nodeView    `json:"nodes"`
	Subs         []subView     `json:"subs"`

	// ExitReportAuto 表示启动槽位时顺带查询出口评估。
	ExitReportAuto bool `json:"exit_report_auto"`
}

// StateUnknown 是连不上 Docker 时槽位与网关容器的状态。
const StateUnknown = "unknown"

func (s *Server) state(c *call) (any, error) {
	dataDir, err := filepath.Abs(s.opt.DataDir)
	if err != nil {
		dataDir = s.opt.DataDir
	}
	out := stateView{
		Version: s.opt.Version, DataDir: dataDir, Cluster: c.eng.Cluster(), ExitReportAuto: c.eng.ExitReportAuto(), LandingLimit: model.MaxSlotsPerLanding,
		Gateways: []gatewayView{}, Slots: []slotView{}, Nodes: []nodeView{}, Subs: []subView{},
	}
	slots, err := c.st.Slots()
	if err != nil {
		return nil, err
	}
	gateways, err := c.st.Gateways()
	if err != nil {
		return nil, err
	}

	// 现状要问 Docker；连不上时仍然给出配置，状态标成未知。
	var slotSt []engine.SlotStatus
	var gwSt []engine.GatewayStatus
	if dk, derr := dockerapi.New(c.ctx, ""); derr != nil {
		out.DockerError = derr.Error()
	} else if eng, eerr := engine.New(c.st, dk, nil); eerr != nil {
		return nil, eerr
	} else {
		if slotSt, err = eng.SlotStatuses(c.ctx); err != nil {
			return nil, err
		}
		if gwSt, err = eng.GatewayStatuses(c.ctx); err != nil {
			return nil, err
		}
	}
	if out.DockerError != "" {
		for _, sl := range slots {
			ident, _, _ := c.st.Identity(sl.ID)
			slotSt = append(slotSt, engine.SlotStatus{Slot: sl, Identity: ident, State: StateUnknown})
		}
		for _, g := range gateways {
			gs := engine.GatewayStatus{Gateway: g, Container: StateUnknown}
			for _, sl := range slots {
				if sl.Gateway == g.ID {
					gs.Slots = append(gs.Slots, sl.ID)
				}
			}
			gwSt = append(gwSt, gs)
		}
	}

	nodes, err := c.st.Nodes()
	if err != nil {
		return nil, err
	}
	landings := map[string]model.Node{}
	for _, n := range nodes {
		landings[n.ID] = n
	}
	for _, g := range gwSt {
		out.Gateways = append(out.Gateways, gatewayView{
			ID: g.Gateway.ID, Note: g.Gateway.Note, Transit: g.Gateway.Transit, Backups: g.Gateway.Backups, ActiveTransit: g.ActiveTransit,
			EgressNetwork: g.Gateway.EgressNetwork, Pool: g.Gateway.Pool,
			Container: g.Container, CoreReady: g.CoreReady, ConfigCurrent: g.ConfigCurrent, Slots: orEmpty(g.Slots),
		})
	}
	for _, st := range slotSt {
		sl := st.Slot
		tz, _ := model.EffectiveLocale(sl, landings[sl.Landing])
		v := slotView{
			ID: sl.ID, Note: sl.Note, Gateway: sl.Gateway, Landing: sl.Landing, State: st.State, HoldReason: st.HoldReason,
			ExitIP: st.ExitIP, ExitWhere: st.ExitWhere, Hostname: st.Identity.Hostname,
			User: st.Identity.UserName(), Home: st.Identity.Home(), TZ: tz,
			Mounts: sl.Mounts, Memory: sl.MemoryBytes, CPUs: sl.CPUs, FakeDMI: sl.FakeDMI, FakeProc: sl.FakeProc,
			TmuxBridge: sl.TmuxBridge, TmuxAlive: st.TmuxAlive, Watch: st.Watch,
			EnterCommand: enterCommand(dataDir, sl.ID),
		}
		if v.Mounts == nil {
			v.Mounts = []model.Mount{}
		}
		if !st.AppliedAt.IsZero() {
			at := st.AppliedAt
			v.AppliedAt = &at
		}
		out.Slots = append(out.Slots, v)
	}
	for _, n := range nodes {
		typ, _ := n.Proxy["type"].(string)
		v := nodeView{
			ID: n.ID, Note: n.Note, Role: n.Role, Type: typ, Server: n.Server(), Port: n.Port(),
			ConnectIPv4: n.ConnectIPv4, ConnectAuto: n.ConnectAuto, FakeIP: engine.IsFakeIP(n.ConnectIPv4),
			ExpectExitIP: n.ExpectExitIP, TZ: n.TZ, TZAuto: n.TZAuto, TZFallback: n.TZFallback, ExitReport: n.ExitReport, Direct: n.Direct,
			Source: n.Source, UsedBy: []string{},
		}
		v.Region, v.RegionFrom = n.Region()
		for _, g := range gateways {
			for i, t := range g.Transits() {
				if t != n.ID {
					continue
				}
				if i == 0 {
					v.UsedBy = append(v.UsedBy, "网关 "+g.ID)
				} else {
					v.UsedBy = append(v.UsedBy, "网关 "+g.ID+"（候选）")
				}
			}
		}
		for _, sl := range slots {
			if sl.Landing == n.ID {
				v.UsedBy = append(v.UsedBy, "槽位 "+sl.ID)
			}
		}
		out.Nodes = append(out.Nodes, v)
	}
	subs, err := c.st.Subs()
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		v := subView{ID: sub.ID, Note: sub.Note, Count: len(sub.Proxies), FetchedAt: sub.FetchedAt, Local: sub.Local(), Format: sub.Format, Skipped: sub.Skipped, EveryMin: sub.EveryMin, LastError: sub.LastError}
		if !sub.CheckedAt.IsZero() {
			at := sub.CheckedAt
			v.CheckedAt = &at
		}
		if !sub.TestedAt.IsZero() {
			at := sub.TestedAt
			v.TestedAt = &at
		}
		for _, p := range sub.Proxies {
			if p.DelayMS > 0 {
				v.Usable++
			}
		}
		out.Subs = append(out.Subs, v)
	}
	return out, nil
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// enterCommand 是在终端里进入槽位的命令；数据目录就是默认的那个时不用带 --data-dir。
func enterCommand(dataDir, slot string) string {
	if filepath.Clean(dataDir) == store.DefaultDir() {
		return "cc-cluster enter " + slot
	}
	return fmt.Sprintf("cc-cluster --data-dir %s enter %s", shellQuote(dataDir), slot)
}

// shellQuote 给路径加上单引号，除非它只含不需要引号的字符。
func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:@", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type checkView struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func checks(in []engine.Check) []checkView {
	out := make([]checkView, len(in))
	for i, c := range in {
		out[i] = checkView{c.Name, c.OK, c.Detail}
	}
	return out
}

// ops 返回最近的操作记录，新的在前。
func (s *Server) ops(c *call) (any, error) {
	data, err := os.ReadFile(filepath.Join(c.st.Dir(), "ops.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return []model.Op{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := []model.Op{}
	for i := len(lines) - 1; i >= 0 && len(out) < 200; i-- {
		var op model.Op
		if json.Unmarshal([]byte(lines[i]), &op) == nil {
			out = append(out, op)
		}
	}
	return out, nil
}

// ---- 订阅 ----

type delayView struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Server  string `json:"server"`
	DelayMS int    `json:"delay_ms"`
	Info    bool   `json:"info,omitempty"`
	// Region 是从名称里认出的国家或地区代码；Nodes 是配置与它相同的已登记节点。
	Region string   `json:"region,omitempty"`
	Nodes  []string `json:"nodes,omitempty"`
}

func (c *call) delays(in []engine.NodeDelay) ([]delayView, error) {
	nodes, err := c.st.Nodes()
	if err != nil {
		return nil, err
	}
	registered := map[string][]string{}
	for _, n := range nodes {
		if h := model.ProxyHash(n.Proxy); h != "" {
			registered[h] = append(registered[h], n.ID)
		}
	}
	out := make([]delayView, len(in))
	for i, d := range in {
		out[i] = delayView{Name: d.Name, Type: d.Type, Server: d.Server, DelayMS: d.DelayMS, Info: d.Info, Region: d.Region, Nodes: registered[d.Hash]}
	}
	return out, nil
}

func (s *Server) subAdd(c *call) (any, error) {
	var in struct {
		ID      string `json:"id"`
		URL     string `json:"url"`
		Content string `json:"content"`
		Note    string `json:"note"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	sub, err := c.eng.SubAdd(c.ctx, in.ID, strings.TrimSpace(in.Note), engine.SubInput{URL: in.URL, Content: in.Content})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": sub.ID, "count": len(sub.Proxies), "skipped": sub.Skipped}, nil
}

// subSet 修改订阅的来源。现有的地址和内容不提供读取：换地址就填新的，本地订阅可以整体替换或往后追加。
func (s *Server) subSet(c *call) (any, error) {
	var in struct {
		URL     string `json:"url"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	return c.eng.SubSet(c.ctx, c.id, engine.SubInput{URL: in.URL, Content: in.Content, Append: in.Append})
}

func (s *Server) subNodes(c *call) (any, error) {
	sub, err := c.st.Sub(c.id)
	if err != nil {
		return nil, err
	}
	return c.delays(engine.SubDelays(sub))
}

func (s *Server) subTest(c *call) (any, error) {
	var in struct {
		Landing string `json:"landing"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	res, err := c.eng.SubTest(c.ctx, c.id, engine.DelayOptions{Landing: in.Landing})
	if err != nil {
		return nil, err
	}
	return c.delays(res)
}

// ---- 节点、网关、槽位 ----

func (s *Server) nodeAdd(c *call) (any, error) {
	var in struct {
		ID           string `json:"id"`
		Note         string `json:"note"`
		Role         string `json:"role"`
		TZ           string `json:"tz"`
		ExpectExitIP string `json:"expect_exit_ip"`
		// 取自订阅
		Sub  string `json:"sub"`
		Name string `json:"name"`
		// 手工填写
		Type     string `json:"type"`
		Server   string `json:"server"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	n := model.Node{ID: in.ID, Note: strings.TrimSpace(in.Note), Role: in.Role, TZ: in.TZ, ExpectExitIP: in.ExpectExitIP}
	if in.Sub != "" {
		if in.Name == "" {
			return nil, errors.New("需要选定订阅里的一个节点")
		}
		return nil, c.eng.NodeFromSub(c.ctx, n, in.Sub, in.Name)
	}
	n.Proxy = map[string]any{"type": in.Type, "server": strings.TrimSpace(in.Server), "port": in.Port}
	if in.Username != "" {
		n.Proxy["username"] = in.Username
	}
	if in.Password != "" {
		n.Proxy["password"] = in.Password
	}
	return nil, c.eng.AddNode(c.ctx, n)
}

func (s *Server) gatewayAdd(c *call) (any, error) {
	var in struct {
		ID      string `json:"id"`
		Note    string `json:"note"`
		Transit string `json:"transit"`
		Pool    string `json:"pool"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	return nil, c.eng.AddGateway(model.Gateway{ID: in.ID, Note: strings.TrimSpace(in.Note), Transit: in.Transit, Pool: in.Pool})
}

func (s *Server) slotAdd(c *call) (any, error) {
	var in struct {
		ID       string   `json:"id"`
		Note     string   `json:"note"`
		Gateway  string   `json:"gateway"`
		Landing  string   `json:"landing"`
		Mounts   []string `json:"mounts"`
		Memory   string   `json:"memory"`
		CPUs     float64  `json:"cpus"`
		User     string   `json:"user"`
		Hostname string   `json:"hostname"`
		TZ       string   `json:"tz"`
		FakeDMI  bool     `json:"fake_dmi"`
		FakeProc bool     `json:"fake_proc"`
		Tmux     bool     `json:"tmux_bridge"`
		// 直出时给出订阅和其中的节点名，网关与落地由服务端准备。
		DirectSub  string `json:"direct_sub"`
		DirectName string `json:"direct_name"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	sl := model.Slot{
		ID: in.ID, Note: strings.TrimSpace(in.Note), Gateway: in.Gateway, Landing: in.Landing, UID: os.Getuid(), GID: os.Getgid(),
		CPUs: in.CPUs, TZ: in.TZ, FakeDMI: in.FakeDMI, FakeProc: in.FakeProc, TmuxBridge: in.Tmux,
	}
	var err error
	if sl.MemoryBytes, err = model.ParseBytes(in.Memory); err != nil {
		return nil, err
	}
	for _, m := range in.Mounts {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		// 网页上只填宿主目录：worker 内使用相同的路径，和 enter 自动挂载的做法一致。
		if !filepath.IsAbs(m) {
			return nil, fmt.Errorf("挂载目录 %q 需要是绝对路径", m)
		}
		dir := filepath.Clean(m)
		sl.Mounts = append(sl.Mounts, model.Mount{Source: dir, Target: dir})
	}
	// 直出：不用落地，把订阅里的一个节点登记成这个槽专用的出口，并放进不带中转的网关。
	direct := in.DirectSub != ""
	if direct {
		gw, landing, err := c.eng.PrepareDirect(c.ctx, sl.ID, in.DirectSub, in.DirectName)
		if err != nil {
			return nil, err
		}
		sl.Gateway, sl.Landing = gw, landing
	}
	_, warnings, err := c.eng.AddSlot(c.ctx, sl, engine.SlotOptions{Hostname: in.Hostname, User: in.User})
	if err != nil {
		if direct {
			_ = c.eng.RemoveNode(sl.Landing)
		}
		return nil, err
	}
	if direct {
		warnings = append(warnings, engine.DirectNotice)
	}
	return map[string]any{"warnings": orEmpty(warnings)}, nil
}

func (s *Server) slotUp(c *call) (any, error) {
	var in struct {
		Recreate bool `json:"recreate"`
	}
	if err := c.body(&in); err != nil {
		return nil, err
	}
	res, err := c.eng.SlotUp(c.ctx, c.id, engine.UpOptions{Recreate: in.Recreate})
	if err != nil {
		return nil, err
	}
	return map[string]any{"exit": res.ExitSummary()}, nil
}
