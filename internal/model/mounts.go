package model

import (
	"fmt"
	"path"
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
// 根目录、系统目录、会盖住 HOME 的目录和包住 protected 中任何一个路径的目录不在其中。
func WorkspaceCandidates(dir, home string, protected ...string) []string {
	var out []string
	for d := filepath.Clean(dir); d != "/" && d != "."; d = filepath.Dir(d) {
		if CheckMountTarget(d, home) == nil && CheckMountSource(d, protected...) == nil {
			out = append(out, d)
		}
	}
	return out
}

// CheckMountSource 检查一个挂载源：它不能是 protected 中某个路径本身或其上级。
// protected 是宿主上不该整个交给 worker 的目录：使用者的主目录（里面有 SSH 密钥、
// 各种登录凭据）和管理器的数据目录（里面有节点的账号密码和其他槽位的 HOME）。
func CheckMountSource(source string, protected ...string) error {
	source = filepath.Clean(source)
	for _, p := range protected {
		if p == "" {
			continue
		}
		if p = filepath.Clean(p); under(p, source) {
			if p == source {
				return fmt.Errorf("不能将 %s 整体挂载到槽位，请挂载其下的具体项目目录", source)
			}
			return fmt.Errorf("不能挂载 %s：该目录包含 %s，请挂载具体的项目目录", source, p)
		}
	}
	return nil
}

// LinkName 返回一个挂载在槽位 HOME 下的快捷方式的名字：挂载目标的最后一段。
// 目标本来就在 HOME 里、或名字以点开头（会与配置目录混在一起）时不建快捷方式。
func LinkName(target, home string) (string, bool) {
	if under(target, home) {
		return "", false
	}
	name := path.Base(path.Clean(target))
	if name == "/" || name == "." || strings.HasPrefix(name, ".") {
		return "", false
	}
	return name, true
}

// DefaultDir 返回进入槽位时默认所在的目录：有 HOME 之外的挂载就用第一个，否则是 HOME。
func DefaultDir(mounts []Mount, home string) string {
	for _, m := range mounts {
		if !under(m.Target, home) {
			return m.Target
		}
	}
	return home
}
