// Package engine 把期望配置落实成容器、接线和路由。
//
// 对外的方法各自取数据目录锁；未导出的方法假定锁已在手。
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/store"
)

// 容器标签。
const (
	labelCluster = "cc-cluster.cluster"
	labelRole    = "cc-cluster.role"
	labelID      = "cc-cluster.id"
	labelSpec    = "cc-cluster.spec"

	roleGateway = "gateway"
	roleWorker  = "worker"
	roleHelper  = "helper"
)

// Engine 持有一个数据目录和一个 Docker 连接。
type Engine struct {
	st  *store.Store
	dk  *dockerapi.Client
	cl  model.Cluster
	out io.Writer

	// LandingLimit 是同一个落地最多同时接线的槽位数；0 表示用 model.MaxSlotsPerLanding。
	LandingLimit int
	// Tmux 描述怎样在宿主上运行 tmux（桥接用）；零值即可。
	Tmux TmuxHost
	// NoExitReport 关掉启动槽位时对出口 IP 评估的查询（它会访问外部接口），不论集群设置如何；测试用。
	// 给使用者的开关是集群设置里的 no_exit_report，见 SetExitReportAuto。
	NoExitReport bool
}

func (e *Engine) landingLimit() int {
	if e.LandingLimit > 0 {
		return e.LandingLimit
	}
	return model.MaxSlotsPerLanding
}

// New 创建 Engine。progress 接收面向用户的进度行，可以为 nil。
func New(st *store.Store, dk *dockerapi.Client, progress io.Writer) (*Engine, error) {
	cl, err := st.Cluster()
	if err != nil {
		return nil, err
	}
	cl.Normalize()
	if err := cl.Validate(); err != nil {
		return nil, err
	}
	if progress == nil {
		progress = io.Discard
	}
	return &Engine{st: st, dk: dk, cl: cl, out: progress}, nil
}

// Cluster 返回集群设置。
func (e *Engine) Cluster() model.Cluster { return e.cl }

// ExitReportAuto 报告启动槽位时是否顺带查询出口评估。每次从数据目录读取：
// 网页运行期间可以改这个设置。
func (e *Engine) ExitReportAuto() bool {
	if e.NoExitReport {
		return false
	}
	c, err := e.st.Cluster()
	return err != nil || !c.NoExitReport
}

// SetExitReportAuto 打开或关掉启动槽位时顺带进行的出口评估查询。
func (e *Engine) SetExitReportAuto(on bool) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record("cluster.exit-report", "", err, fmt.Sprintf("auto=%t", on)) }()
	c, err := e.st.Cluster()
	if err != nil {
		return err
	}
	c.NoExitReport = !on
	return e.st.SaveCluster(c)
}

// SetNote 能改备注的对象种类。
const (
	KindNode    = "node"
	KindSub     = "sub"
	KindGateway = "gateway"
	KindSlot    = "slot"
)

// SetNote 修改节点、订阅、网关或槽位的备注；note 为空表示清掉。
// 备注只用于展示，不影响容器和核心配置，改了不需要重建或重新应用。
func (e *Engine) SetNote(kind, id, note string) (err error) {
	unlock, err := e.st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { e.record(kind+".note", id, err, note) }()
	note = strings.TrimSpace(note)
	if err := model.ValidateNote(note); err != nil {
		return err
	}
	switch kind {
	case KindNode:
		n, err := e.st.Node(id)
		if err != nil {
			return err
		}
		n.Note = note
		return e.st.SaveNode(n, false)
	case KindSub:
		s, err := e.st.Sub(id)
		if err != nil {
			return err
		}
		s.Note = note
		return e.st.SaveSub(s, false)
	case KindGateway:
		g, err := e.st.Gateway(id)
		if err != nil {
			return err
		}
		g.Note = note
		return e.st.SaveGateway(g, false)
	case KindSlot:
		sl, err := e.st.Slot(id)
		if err != nil {
			return err
		}
		sl.Note = note
		return e.st.SaveSlot(sl, false)
	}
	return fmt.Errorf("不认识的对象种类 %q", kind)
}

// GatewayContainer 返回网关的容器名。
func (e *Engine) GatewayContainer(id string) string { return e.cl.Name + "-gw-" + id }

// WorkerContainer 返回槽位 worker 的容器名。
func (e *Engine) WorkerContainer(id string) string { return e.cl.Name + "-w-" + id }

func (e *Engine) logf(format string, args ...any) {
	fmt.Fprintf(e.out, format+"\n", args...)
}

func (e *Engine) labels(role, id string) map[string]string {
	return map[string]string{labelCluster: e.cl.Name, labelRole: role, labelID: id}
}

// record 写一条操作记录；记录失败不影响操作本身的结果。
func (e *Engine) record(op, target string, err error, detail string) {
	o := model.Op{Time: time.Now().UTC(), Op: op, Target: target, OK: err == nil, Detail: detail}
	if err != nil {
		o.Error = truncate(err.Error(), 500)
		var ce *configError
		if errors.As(err, &ce) {
			o.Error = "核心配置校验失败（核心的报错可能带出配置片段，不写入记录）"
		}
	}
	_ = e.st.AppendOp(o)
}

// inspect 读取容器；不存在时 ok 为 false。
func (e *Engine) inspect(ctx context.Context, name string) (c dockerapi.Container, ok bool, err error) {
	c, err = e.dk.ContainerInspect(ctx, name)
	if dockerapi.IsNotFound(err) {
		return c, false, nil
	}
	return c, err == nil, err
}

// sh 在容器里以 sh -c 执行脚本；退出码非 0 时返回带标准错误的错误。
func (e *Engine) sh(ctx context.Context, container, script string) (string, error) {
	res, err := e.dk.Exec(ctx, container, []string{"/bin/sh", "-c", script}, dockerapi.ExecOptions{})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, &exitError{code: res.ExitCode, output: strings.TrimSpace(res.Stderr + res.Stdout)}
	}
	return res.Stdout, nil
}

// exitError 是容器内命令的非 0 退出。
type exitError struct {
	code   int
	output string
}

func (x *exitError) Error() string {
	return fmt.Sprintf("退出码 %d：%s", x.code, truncate(x.output, 400))
}

// helperSpec 构造助手容器：一次性、只带给定的能力，进入目标容器的网络命名空间。
func (e *Engine) helperSpec(image, netOf, pidOf string, caps []string, script string) dockerapi.ContainerSpec {
	spec := dockerapi.ContainerSpec{
		Image:      image,
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{script},
		Labels:     e.labels(roleHelper, ""),
		HostConfig: dockerapi.HostConfig{
			NetworkMode: "container:" + netOf,
			CapDrop:     []string{"ALL"},
			CapAdd:      caps,
			SecurityOpt: []string{"no-new-privileges"},
			LogConfig:   &dockerapi.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m"}},
		},
	}
	if pidOf != "" {
		spec.HostConfig.PidMode = "container:" + pidOf
	}
	return spec
}

// runHelper 运行助手并要求它以 0 退出。
func (e *Engine) runHelper(ctx context.Context, spec dockerapi.ContainerSpec) (string, error) {
	res, err := e.dk.RunOnce(ctx, spec)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, &exitError{code: res.ExitCode, output: strings.TrimSpace(res.Stderr + res.Stdout)}
	}
	return res.Stdout, nil
}

// poll 每隔 interval 调一次 fn，直到它返回 true、出错或超时。
func poll(ctx context.Context, timeout, interval time.Duration, fn func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := fn()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

var errTimeout = errors.New("等待超时")

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
