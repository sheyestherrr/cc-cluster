package engine

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// workerDNS 写进 worker 的 /etc/resolv.conf。查询包经默认路由到网关，在 TUN 层被核心接管，
// 实际并不会到达这个地址；它只需要是一个普通的公网解析器地址。
const workerDNS = "1.1.1.1"

// dmiPath 是容器里 DMI 信息的位置（/sys/class/dmi/id 是指向它的符号链接）。
const dmiPath = "/sys/devices/virtual/dmi/id"

// Docker 默认遮蔽的路径。MaskedPaths 一旦给出就整体替换默认值，所以这里要带上它们。
var defaultMaskedPaths = []string{
	"/proc/asound", "/proc/acpi", "/proc/interrupts", "/proc/kcore", "/proc/keys",
	"/proc/latency_stats", "/proc/timer_list", "/proc/timer_stats", "/proc/sched_debug",
	"/proc/scsi", "/sys/firmware", "/sys/devices/virtual/powercap",
}

// workerParams 是构造 worker 容器所需的全部输入。
type workerParams struct {
	Image string
	Slot  model.Slot
	Ident model.Identity
	// TZ 与 Lang 是已经结合落地设置算出的实际值；TZ 可以为空。
	TZ, Lang string
	// HomeDir 是该槽的 HOME 在宿主上的路径。
	HomeDir string
	// 下面三项是可选的硬件信息覆盖在宿主上的路径；为空表示不覆盖。
	DMIDir, CPUInfo, MemInfo string
	// TmuxDir 是该槽的宿主 tmux socket 目录；为空表示没有开启桥接。
	TmuxDir string
	Labels  map[string]string
}

// workerSpec 构造 worker 容器。
//
// 网络模式固定为 none：容器启动时只有 lo，之后由助手接一条到网关的 veth。
// 环境变量是白名单，任何代理变量都不会出现。
//
// 身份文件（passwd、group、machine-id）不用挂载，而是在容器创建后写进它自己的文件层，见 workerSeed：
// 挂载会出现在容器内的 /proc/self/mountinfo 里，并带出宿主上的路径。rootfs 因此不是只读的，
// 但 worker 内的用户没有 root 身份也没有任何能力，写不了系统目录。
func workerSpec(p workerParams) dockerapi.ContainerSpec {
	home, user := p.Ident.Home(), p.Ident.UserName()
	env := map[string]string{
		"HOME":    home,
		"USER":    user,
		"LOGNAME": user,
		"SHELL":   p.Slot.Shell,
		"PATH":    home + "/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM":    "xterm-256color",
		"LANG":    p.Lang,
	}
	if p.TZ != "" {
		env["TZ"] = p.TZ
	}
	for k, v := range p.Slot.Env {
		if !model.IsProxyEnv(k) {
			env[k] = v
		}
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	sort.Strings(envList)

	binds := []string{p.HomeDir + ":" + home}
	for _, m := range p.Slot.Mounts {
		b := m.Source + ":" + m.Target
		if m.ReadOnly {
			b += ":ro"
		}
		binds = append(binds, b)
	}
	masked := append([]string(nil), defaultMaskedPaths...)
	if p.DMIDir != "" {
		binds = append(binds, p.DMIDir+":"+dmiPath+":ro")
	} else {
		// DMI 信息（厂商、型号等）是宿主机的硬件身份，所有槽都相同；不覆盖时就遮成空的。
		masked = append(masked, dmiPath)
	}
	if p.CPUInfo != "" {
		binds = append(binds, p.CPUInfo+":/proc/cpuinfo:ro")
	}
	if p.MemInfo != "" {
		binds = append(binds, p.MemInfo+":/proc/meminfo:ro")
	}
	if p.TmuxDir != "" {
		// 挂的是目录而不是 socket 文件：宿主的 tmux server 重建后，worker 里看到的仍是新的 socket。
		binds = append(binds, p.TmuxDir+":"+tmuxMountPath)
	}

	hc := dockerapi.HostConfig{
		NetworkMode: "none",
		Binds:       binds,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		Tmpfs: map[string]string{
			"/tmp":     "rw,nosuid,nodev,mode=1777",
			"/var/tmp": "rw,nosuid,nodev,mode=1777",
		},
		Sysctls: map[string]string{"net.ipv6.conf.all.disable_ipv6": "1"},
		// 私有的 cgroup 命名空间：否则 /proc/self/cgroup 里能看到带容器 ID 的 /docker/<id>。
		CgroupnsMode: "private",
		// 与桌面发行版一致：主机名解析到 127.0.1.1。
		ExtraHosts: []string{p.Ident.Hostname + ":127.0.1.1"},
		// Docker 每次启动容器都会重新生成 /etc/resolv.conf。这两项让生成的内容不带宿主的
		// search 域；启动后 workerSettle 再把它改写成只有一行 nameserver。
		DNS:         []string{workerDNS},
		DNSSearch:   []string{"."},
		Memory:      p.Slot.MemoryBytes,
		NanoCPUs:    int64(p.Slot.CPUs * 1e9),
		PidsLimit:   p.Slot.PidsLimit,
		Init:        dockerapi.Bool(true),
		MaskedPaths: masked,
		LogConfig:   &dockerapi.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}},
	}
	return dockerapi.ContainerSpec{
		Image:    p.Image,
		Hostname: p.Ident.Hostname,
		User:     fmt.Sprintf("%d:%d", p.Slot.UID, p.Slot.GID),
		Env:      envList,
		// 覆盖镜像自带的入口：worker 只是一个常驻的环境，实际程序经 exec 进入。
		Entrypoint: []string{"sleep", "infinity"},
		WorkingDir: home,
		Labels:     p.Labels,
		HostConfig: hc,
	}
}

// 镜像里没有 passwd / group 时使用的最小内容。
const (
	minimalPasswd = "root:x:0:0:root:/root:/sbin/nologin\nnobody:x:65534:65534:nobody:/nonexistent:/sbin/nologin\n"
	minimalGroup  = "root:x:0:\nnogroup:x:65534:\n"
)

// mergePasswd 在镜像自带的 /etc/passwd 上加入该槽的用户：去掉同名或同 UID 的已有条目，再追加一行。
// 保留镜像原有的系统账号，读起来和一台普通机器一样。
func mergePasswd(base []byte, ident model.Identity, slot model.Slot) []byte {
	if len(bytes.TrimSpace(base)) == 0 {
		base = []byte(minimalPasswd)
	}
	user, uid := ident.UserName(), strconv.Itoa(slot.UID)
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(base), "\n"), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 && (f[0] == user || f[2] == uid) {
			continue
		}
		out = append(out, line)
	}
	gecos := strings.ToUpper(user[:1]) + user[1:]
	out = append(out, fmt.Sprintf("%s:x:%d:%d:%s:%s:%s", user, slot.UID, slot.GID, gecos, ident.Home(), slot.Shell))
	return []byte(strings.Join(out, "\n") + "\n")
}

// mergeGroup 保证该槽的 GID 在 /etc/group 里有名字：已有同 GID 的组就沿用，
// 否则去掉同名的组并追加一个与用户同名的组。
func mergeGroup(base []byte, ident model.Identity, slot model.Slot) []byte {
	if len(bytes.TrimSpace(base)) == 0 {
		base = []byte(minimalGroup)
	}
	user, gid := ident.UserName(), strconv.Itoa(slot.GID)
	var out []string
	hasGID := false
	for _, line := range strings.Split(strings.TrimRight(string(base), "\n"), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 && f[2] == gid {
			hasGID = true
		} else if len(f) >= 3 && f[0] == user {
			continue
		}
		out = append(out, line)
	}
	if !hasGID {
		out = append(out, fmt.Sprintf("%s:x:%d:", user, slot.GID))
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// workerSettleScript 在 worker 每次启动后以容器内的 root 身份执行一次。
//
// 容器内的 root 没有任何能力，但它是这些文件的属主，所以改得动：
//   - 删除 /.dockerenv：这个文件是"我在 Docker 容器里"的直接标记。
//   - 把 Docker 生成的 /etc/resolv.conf 改写成只有一行 nameserver。
//   - 镜像里有对应的时区数据时，设置 /etc/localtime 与 /etc/timezone。
const workerSettleScript = `set -e
rm -f /.dockerenv
printf 'nameserver %s\n' "$CC_DNS" > /etc/resolv.conf
if [ -n "$CC_TZ" ] && [ -e "/usr/share/zoneinfo/$CC_TZ" ]; then
	ln -sfn "/usr/share/zoneinfo/$CC_TZ" /etc/localtime
	printf '%s\n' "$CC_TZ" > /etc/timezone
fi
`

// dmiFiles 把身份里的机型信息展开成 DMI 目录下的文件。
func dmiFiles(dmi map[string]string) map[string][]byte {
	files := map[string][]byte{}
	for k, v := range dmi {
		files[k] = []byte(v + "\n")
	}
	us := func(k string) string { return strings.ReplaceAll(dmi[k], " ", "") }
	alias := fmt.Sprintf("dmi:bvn%s:bvr%s:bd%s:svn%s:pn%s:pvr%s:rvn%s:rn%s:rvr%s:cvn%s:ct%s:cvr:sku%s:",
		us("bios_vendor"), us("bios_version"), dmi["bios_date"], us("sys_vendor"), us("product_name"), us("product_version"),
		us("board_vendor"), us("board_name"), us("board_version"), us("chassis_vendor"), dmi["chassis_type"], us("product_sku"))
	files["modalias"] = []byte(alias + "\n")
	files["uevent"] = []byte("MODALIAS=" + alias + "\n")
	return files
}

// limitCPUInfo 把 /proc/cpuinfo 截成 n 个处理器，并让每个处理器里的核数字段与之一致。
func limitCPUInfo(real []byte, n int) []byte {
	blocks := strings.Split(strings.TrimSpace(string(real)), "\n\n")
	if n <= 0 || n > len(blocks) {
		n = len(blocks)
	}
	var out []string
	for _, b := range blocks[:n] {
		lines := strings.Split(b, "\n")
		for i, l := range lines {
			k, _, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "siblings", "cpu cores":
				lines[i] = k + ": " + strconv.Itoa(n)
			}
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return []byte(strings.Join(out, "\n\n") + "\n\n")
}

// limitMemInfo 把 /proc/meminfo 里以 kB 计的各项按"限额 / 实际总量"等比例缩小，
// 使 MemTotal 等于限额、其余各项仍然自洽。
func limitMemInfo(real []byte, limitBytes int64) []byte {
	lines := strings.Split(strings.TrimRight(string(real), "\n"), "\n")
	var total int64
	for _, l := range lines {
		if f := strings.Fields(l); len(f) == 3 && f[0] == "MemTotal:" {
			total, _ = strconv.ParseInt(f[1], 10, 64)
		}
	}
	limit := limitBytes / 1024
	if total <= 0 || limit <= 0 || limit >= total {
		return real
	}
	ratio := float64(limit) / float64(total)
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) != 3 || f[2] != "kB" || f[0] == "Hugepagesize:" {
			continue
		}
		v, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		scaled := int64(math.Round(float64(v) * ratio))
		if f[0] == "MemTotal:" {
			scaled = limit
		}
		lines[i] = fmt.Sprintf("%-16s%8d kB", f[0], scaled)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}
