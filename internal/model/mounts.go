package model

import (
	"path/filepath"
	"strings"
)

// under 报告 p 是否是 dir 本身或在它下面。两者都应是规范的绝对路径。
func under(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// ContainerPath 把宿主上的一个路径换算成 worker 内的路径。
// hostPath 落在某个挂载源之下时返回对应位置；多个挂载都覆盖它时取最具体的那个。
func ContainerPath(mounts []Mount, hostPath string) (string, bool) {
	hostPath = filepath.Clean(hostPath)
	best := -1
	for i, m := range mounts {
		if under(hostPath, m.Source) && (best < 0 || len(m.Source) > len(mounts[best].Source)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := mounts[best]
	rel := strings.TrimPrefix(strings.TrimPrefix(hostPath, m.Source), "/")
	if rel == "" {
		return m.Target, true
	}
	return strings.TrimSuffix(m.Target, "/") + "/" + filepath.ToSlash(rel), true
}

// AddWorkspace 把宿主目录 dir 以相同的路径挂进 worker，返回新的挂载列表。
// 已经被 dir 覆盖的同路径挂载（它的子目录）随之去掉；路径不同或只读的挂载保持原样。
func AddWorkspace(mounts []Mount, dir string) []Mount {
	dir = filepath.Clean(dir)
	out := make([]Mount, 0, len(mounts)+1)
	for _, m := range mounts {
		if m.Source == m.Target && !m.ReadOnly && under(m.Source, dir) {
			continue
		}
		out = append(out, m)
	}
	return append(out, Mount{Source: dir, Target: dir})
}

// WorkspaceCandidates 列出可以作为挂载根的目录：dir 本身和它的各级上级，由近到远。
// 根目录、系统目录和会盖住 HOME 的目录不在其中。
func WorkspaceCandidates(dir, home string) []string {
	var out []string
	for d := filepath.Clean(dir); d != "/" && d != "."; d = filepath.Dir(d) {
		if CheckMountTarget(d, home) == nil {
			out = append(out, d)
		}
	}
	return out
}
