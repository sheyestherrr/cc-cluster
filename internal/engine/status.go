package engine

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/netplan"
)

// 槽位状态。只有 StateReady 允许进入。
const (
	StateHold    = "hold"    // 手动断网
	StateAbsent  = "absent"  // 没有 worker 容器
	StateStopped = "stopped" // worker 容器没在运行
	StateOffline = "offline" // worker 在运行但没有接线（刚启动、网关重启过等）
	StatePending = "pending" // 已接线，但期望配置在上次应用之后变过，或没有应用记录
	StateReady   = "ready"   // 已接线，且与最近一次通过出口复测的配置一致
)

// SlotStatus 是一个槽位的现状。
type SlotStatus struct {
	Slot       model.Slot
	Identity   model.Identity
	State      string
	HoldReason string
	ExitIP     string    // 最近一次复测通过的出口
	ExitWhere  string    // 出口的国家与运营商，探测地址给出时才有
	AppliedAt  time.Time // 最近一次复测通过的时间
	WorkerIP   string
}

// SlotStatuses 返回全部槽位的现状。只读，不取锁。
func (e *Engine) SlotStatuses(ctx context.Context) ([]SlotStatus, error) {
	slots, err := e.st.Slots()
	if err != nil {
		return nil, err
	}
	out := make([]SlotStatus, 0, len(slots))
	for _, sl := range slots {
		s, err := e.slotStatus(ctx, sl)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// SlotStatus 返回一个槽位的现状。
func (e *Engine) SlotStatus(ctx context.Context, id string) (SlotStatus, error) {
	sl, err := e.st.Slot(id)
	if err != nil {
		return SlotStatus{}, err
	}
	return e.slotStatus(ctx, sl)
}

func (e *Engine) slotStatus(ctx context.Context, sl model.Slot) (SlotStatus, error) {
	s := SlotStatus{Slot: sl}
	s.Identity, _, _ = e.st.Identity(sl.ID)
	applied, hasApplied, err := e.st.Applied(sl.ID)
	if err != nil {
		return s, err
	}
	if hasApplied {
		s.ExitIP, s.AppliedAt = applied.ExitIP, applied.AppliedAt
		s.ExitWhere = exitWhere(applied.ExitCountry, applied.ExitOrg)
	}
	if p, err := netip.ParsePrefix(sl.Subnet); err == nil {
		s.WorkerIP = p.Addr().Next().Next().String()
	}

	if h, held, err := e.st.Hold(sl.ID); err != nil {
		return s, err
	} else if held {
		s.State, s.HoldReason = StateHold, h.Reason
		return s, nil
	}
	wc, ok, err := e.inspect(ctx, e.WorkerContainer(sl.ID))
	if err != nil {
		return s, err
	}
	switch {
	case !ok:
		s.State = StateAbsent
		return s, nil
	case !wc.State.Running:
		s.State = StateStopped
		return s, nil
	}

	s.State = StateOffline
	p, err := e.plan(sl.Gateway)
	if err != nil {
		return s, nil // 期望配置本身有问题时，只报告容器层面的状态
	}
	gc, ok, err := e.inspect(ctx, e.GatewayContainer(sl.Gateway))
	if err != nil {
		return s, err
	}
	if !ok || !gc.State.Running {
		return s, nil
	}
	plumbed, err := e.isPlumbed(ctx, &gatewayRuntime{name: gc.Name}, p.links[sl.ID])
	if err != nil || !plumbed {
		return s, err
	}
	s.State = StatePending
	digest, err := e.slotDigest(sl, s.Identity, p)
	if err != nil {
		return s, err
	}
	if hasApplied && applied.Digest == digest && applied.WorkerID == wc.ID && applied.GatewayID == gc.ID {
		s.State = StateReady
	}
	return s, nil
}

// RequireReady 在槽位不处于 ready 时返回说明原因的错误。进入槽位前调用。
func (e *Engine) RequireReady(ctx context.Context, id string) error {
	s, err := e.SlotStatus(ctx, id)
	if err != nil {
		return err
	}
	if s.State != StateReady {
		return fmt.Errorf("槽位 %s 当前是 %s，不是 ready；先执行 slot up %s", id, s.State, id)
	}
	return nil
}

// Check 是 doctor 的一项检查结果。
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Doctor 检查这台机器能否运行本项目：Docker 可用、镜像在、
// 容器里能建 veth、写策略路由、打开 TUN 设备。不访问任何外部地址。
func (e *Engine) Doctor(ctx context.Context) []Check {
	var out []Check
	add := func(name string, ok bool, detail string) { out = append(out, Check{name, ok, detail}) }

	v := e.dk.Server()
	add("Docker 守护进程", true, fmt.Sprintf("版本 %s，API %s（使用 %s），%s/%s，内核 %s", v.Version, v.APIVersion, e.dk.APIVersion(), v.Os, v.Arch, v.KernelVersion))
	if info, err := e.dk.Info(ctx); err != nil {
		add("Docker 信息", false, err.Error())
	} else {
		add("Docker 信息", true, fmt.Sprintf("%s，cgroup v%s，存储驱动 %s，安全选项 %v", info.OperatingSystem, info.CgroupVersion, info.Driver, info.SecurityOptions))
	}

	gwImage := e.cl.GatewayImage
	hasGW, err := e.dk.ImageExists(ctx, gwImage)
	add("网关镜像 "+gwImage, hasGW && err == nil, imageDetail(hasGW, err, "执行 make images 构建"))
	hasW, err := e.dk.ImageExists(ctx, e.cl.WorkerImage)
	add("worker 镜像 "+e.cl.WorkerImage, hasW && err == nil, imageDetail(hasW, err, "执行 make images 构建，或在 slot add 时用 --image 指定"))
	if !hasGW {
		add("容器内网络能力", false, "缺少网关镜像，跳过")
		return out
	}

	// 在一个无网的一次性容器里走一遍网关要用的内核功能。
	script := fmt.Sprintf("ip link add t0 type veth peer name t1 && echo veth-ok && "+
		"ip rule add pref 1000 iif t0 lookup %d && ip rule add pref 1001 iif t0 unreachable && echo rule-ok && "+
		"test -c /dev/net/tun && echo tun-ok && mihomo -v | head -1", netplan.RouteTable)
	spec := dockerapi.ContainerSpec{
		Image:      gwImage,
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{script},
		Labels:     e.labels(roleHelper, ""),
		HostConfig: dockerapi.HostConfig{
			NetworkMode: "none",
			CapDrop:     []string{"ALL"},
			CapAdd:      []string{"NET_ADMIN"},
			SecurityOpt: []string{"no-new-privileges"},
			Devices:     []dockerapi.DeviceMapping{{PathOnHost: "/dev/net/tun", PathInContainer: "/dev/net/tun", CgroupPermissions: "rwm"}},
		},
	}
	res, err := e.dk.RunOnce(ctx, spec)
	switch {
	case err != nil:
		add("容器内网络能力", false, err.Error())
	case res.ExitCode != 0:
		add("容器内网络能力", false, truncate(res.Stdout+res.Stderr, 300))
	default:
		add("容器内网络能力", true, "veth、策略路由、TUN 设备可用；"+lastLines(res.Stdout, 1))
	}
	return out
}

func imageDetail(has bool, err error, hint string) string {
	switch {
	case err != nil:
		return err.Error()
	case !has:
		return "本地没有；" + hint
	}
	return "已在本地"
}
