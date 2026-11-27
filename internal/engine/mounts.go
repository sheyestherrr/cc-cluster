package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sheyestherrr/cc-cluster/internal/dockerapi"
	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// dockerSockets 返回宿主上 Docker socket 可能所在的路径：默认位置、DOCKER_HOST 指定的位置、
// 免 root 模式的位置（$XDG_RUNTIME_DIR/docker.sock）。测试时换成假的。
var dockerSockets = func() []string {
	out := []string{"/var/run/docker.sock", "/run/docker.sock"}
	if p, err := dockerapi.SocketPath(""); err == nil && filepath.IsAbs(p) {
		out = append(out, filepath.Clean(p))
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		out = append(out, filepath.Join(d, "docker.sock"))
	}
	return out
}

// withResolved 在 paths 之后补上其中各项解析符号链接后的路径（与原路径不同、且解析得出来的）。
func withResolved(paths []string) []string {
	out := append([]string(nil), paths...)
	for _, p := range paths {
		if p == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(p); err == nil && r != filepath.Clean(p) {
			out = append(out, r)
		}
	}
	return out
}

// checkMountSource 检查宿主上的一个路径能否作为挂载源交给 worker。
//
// Docker 挂载时会跟随符号链接，所以除了路径本身，还要看它实际指向哪里：
// 两者都不能是使用者的主目录、数据目录或它们的上级（见 model.CheckMountSource），
// 不能位于数据目录之内（见 model.CheckMountOutside），
// 也不能是 Docker socket 或包含它的目录（见 model.CheckMountSocket）。
// 登记槽位、追加挂载和每次创建 worker 之前都检查：登记之后路径可能被换成符号链接。
func (e *Engine) checkMountSource(source string) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("挂载源 %s 不可用：%w", source, err)
	}
	protected := withResolved(e.ProtectedPaths())
	dataDirs := withResolved([]string{e.st.Dir()})
	sockets := withResolved(dockerSockets())
	check := func(p string) error {
		if err := model.CheckMountSource(p, protected...); err != nil {
			return err
		}
		if err := model.CheckMountOutside(p, dataDirs...); err != nil {
			return err
		}
		return model.CheckMountSocket(p, sockets...)
	}
	if err := check(source); err != nil {
		return err
	}
	if resolved == filepath.Clean(source) {
		return nil
	}
	if err := check(resolved); err != nil {
		return fmt.Errorf("挂载源 %s 经符号链接指向 %s：%w", source, resolved, err)
	}
	return nil
}

// WorkspaceCandidates 列出进入槽位时可以作为挂载根的目录：dir 本身和它的各级上级，由近到远，
// 只保留通过挂载源检查的。home 是该槽在 worker 内的 HOME。
func (e *Engine) WorkspaceCandidates(dir, home string) []string {
	var out []string
	for _, d := range model.WorkspaceCandidates(dir, home, e.ProtectedPaths()...) {
		if e.checkMountSource(d) == nil {
			out = append(out, d)
		}
	}
	return out
}
