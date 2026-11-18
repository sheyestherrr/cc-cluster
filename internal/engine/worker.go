package engine

import (
	"fmt"
	"sort"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// workerDNS 写进 worker 的 /etc/resolv.conf。查询包经默认路由到网关，在 TUN 层被核心接管，
// 实际并不会到达这个地址；它只需要是一个普通的公网解析器地址。
const workerDNS = "1.1.1.1"

// workerUser 是 worker 内的用户名；UID/GID 与宿主上的项目所有者对齐。
const workerUser = "dev"

// Docker 默认遮蔽的路径。MaskedPaths 一旦给出就整体替换默认值，所以这里要带上它们。
var defaultMaskedPaths = []string{
	"/proc/asound", "/proc/acpi", "/proc/interrupts", "/proc/kcore", "/proc/keys",
	"/proc/latency_stats", "/proc/timer_list", "/proc/timer_stats", "/proc/sched_debug",
	"/proc/scsi", "/sys/firmware", "/sys/devices/virtual/powercap",
}

// 额外遮蔽的路径：DMI 信息（主板序列号、产品 UUID 等）是宿主机的硬件身份，所有槽都相同。
var extraMaskedPaths = []string{"/sys/devices/virtual/dmi/id"}

// workerFiles 是挂进 worker 的身份文件在宿主上的路径。
type workerFiles struct {
	Home      string
	MachineID string
	Passwd    string
	Group     string
	// ResolvConf 是只有一行 nameserver 的解析配置。Docker 自己生成的那份会带上宿主的
	// search 域和 options，等于把宿主所在网络的名字带进了每个槽。
	ResolvConf string
}

// passwdFile 生成 worker 的 /etc/passwd：只有 root、nobody 和项目用户。
func passwdFile(slot model.Slot) []byte {
	return []byte(fmt.Sprintf(
		"root:x:0:0:root:/root:/sbin/nologin\n"+
			"nobody:x:65534:65534:nobody:/:/sbin/nologin\n"+
			"%s:x:%d:%d:%s:%s:%s\n",
		workerUser, slot.UID, slot.GID, workerUser, model.WorkerHome, slot.Shell))
}

// groupFile 生成 worker 的 /etc/group。
func groupFile(slot model.Slot) []byte {
	return []byte(fmt.Sprintf("root:x:0:\nnobody:x:65534:\n%s:x:%d:\n", workerUser, slot.GID))
}

// workerSpec 构造 worker 容器。
//
// 网络模式固定为 none：容器启动时只有 lo，之后由助手接一条到网关的 veth。
// 环境变量是白名单，任何代理变量都不会出现；身份文件只读挂入。
func workerSpec(image string, slot model.Slot, ident model.Identity, files workerFiles, labels map[string]string) dockerapi.ContainerSpec {
	env := map[string]string{
		"HOME":    model.WorkerHome,
		"USER":    workerUser,
		"LOGNAME": workerUser,
		"SHELL":   slot.Shell,
		"PATH":    model.WorkerHome + "/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM":    "xterm-256color",
		"LANG":    slot.Lang,
	}
	if slot.TZ != "" {
		env["TZ"] = slot.TZ
	}
	for k, v := range slot.Env {
		if !model.IsProxyEnv(k) {
			env[k] = v
		}
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	sort.Strings(envList)

	binds := []string{
		files.Home + ":" + model.WorkerHome,
		files.MachineID + ":/etc/machine-id:ro",
		files.Passwd + ":/etc/passwd:ro",
		files.Group + ":/etc/group:ro",
		files.ResolvConf + ":/etc/resolv.conf:ro",
	}
	for _, m := range slot.Mounts {
		b := m.Source + ":" + m.Target
		if m.ReadOnly {
			b += ":ro"
		}
		binds = append(binds, b)
	}

	hc := dockerapi.HostConfig{
		NetworkMode:    "none",
		Binds:          binds,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp":     "rw,nosuid,nodev,mode=1777",
			"/var/tmp": "rw,nosuid,nodev,mode=1777",
		},
		Sysctls: map[string]string{"net.ipv6.conf.all.disable_ipv6": "1"},
		// worker 自己读到的是上面挂入的 resolv.conf；这两项管的是 Docker 生成的那一份，
		// 进入 worker 网络命名空间的助手用的是它，同样不能带宿主的 search 域。
		DNS:         []string{workerDNS},
		DNSSearch:   []string{"."},
		Memory:      slot.MemoryBytes,
		NanoCPUs:    int64(slot.CPUs * 1e9),
		PidsLimit:   slot.PidsLimit,
		Init:        dockerapi.Bool(true),
		MaskedPaths: append(append([]string(nil), defaultMaskedPaths...), extraMaskedPaths...),
		LogConfig:   &dockerapi.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}},
	}
	return dockerapi.ContainerSpec{
		Image:    image,
		Hostname: ident.Hostname,
		User:     fmt.Sprintf("%d:%d", slot.UID, slot.GID),
		Env:      envList,
		// 覆盖镜像自带的入口：worker 只是一个常驻的环境，实际程序经 exec 进入。
		Entrypoint: []string{"sleep", "infinity"},
		WorkingDir: model.WorkerHome,
		Labels:     labels,
		HostConfig: hc,
	}
}
