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
