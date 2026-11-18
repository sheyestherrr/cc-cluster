// Package store 管理数据目录：期望配置、身份、已应用记录和操作日志都以私有文件保存。
//
// 目录结构：
//
//	<数据目录>/                       0700
//	├── cluster.json                  集群设置
//	├── nodes.d/<node-id>.json        节点片段与角色，含凭据，0600
//	├── subs/<sub-id>.json            订阅地址与拉取到的节点列表，含凭据，0600
//	├── gateways/<gw-id>/gateway.json 网关期望配置
//	├── slots/<slot-id>/
//	│   ├── slot.json                 槽位期望配置
//	│   ├── identity.json             首次生成后不再改动
//	│   ├── applied.json              最近一次成功应用的记录
//	│   ├── hold.json                 手动断网标记
//	│   ├── etc/                      可选的硬件信息覆盖文件（DMI、cpuinfo、meminfo）
//	│   └── home/                     该槽完整 HOME
//	├── ops.jsonl                     追加式操作记录，不含密钥
//	└── lock
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// ErrNotFound 表示请求的对象不存在。
var ErrNotFound = errors.New("不存在")

// ErrExists 表示要创建的对象已经存在。
var ErrExists = errors.New("已存在")

// Store 是一个已打开的数据目录。
type Store struct {
	dir string
}

// Init 创建数据目录并写入集群设置；目录已初始化时返回 ErrExists。
func Init(dir string, c model.Cluster) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: abs}
	if _, err := os.Stat(s.path("cluster.json")); err == nil {
		return nil, fmt.Errorf("数据目录 %s %w", abs, ErrExists)
	}
	for _, d := range []string{"", "nodes.d", "gateways", "slots"} {
		if err := os.MkdirAll(s.path(d), 0o700); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, err
	}
	if err := writeJSON(s.path("cluster.json"), c); err != nil {
		return nil, err
	}
	return s, nil
}

// Open 打开一个已初始化的数据目录。
func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: abs}
	if _, err := os.Stat(s.path("cluster.json")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("数据目录 %s 尚未初始化，先运行 init", abs)
		}
		return nil, err
	}
	return s, nil
}

// Dir 返回数据目录的绝对路径。
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(elem ...string) string {
	return filepath.Join(append([]string{s.dir}, elem...)...)
}

// Cluster 读取集群设置。
func (s *Store) Cluster() (model.Cluster, error) {
	var c model.Cluster
	err := readJSON(s.path("cluster.json"), &c)
	return c, err
}

// ---- 节点 ----

// SaveNode 写入节点；create 为 true 时节点已存在则返回 ErrExists。
func (s *Store) SaveNode(n model.Node, create bool) error {
	p := s.path("nodes.d", n.ID+".json")
	if create && exists(p) {
		return fmt.Errorf("节点 %s %w", n.ID, ErrExists)
	}
	return writeJSON(p, n)
}

// Node 读取一个节点。
func (s *Store) Node(id string) (model.Node, error) {
	var n model.Node
	if err := model.ValidateID("节点", id); err != nil {
		return n, err
	}
	err := readJSON(s.path("nodes.d", id+".json"), &n)
	return n, wrapNotFound(err, "节点", id)
}

// Nodes 按 ID 顺序列出全部节点。
func (s *Store) Nodes() ([]model.Node, error) {
	names, err := listNames(s.path("nodes.d"), ".json")
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(names))
	for _, id := range names {
		n, err := s.Node(id)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// DeleteNode 删除一个节点。
func (s *Store) DeleteNode(id string) error {
	if err := model.ValidateID("节点", id); err != nil {
		return err
	}
	return wrapNotFound(os.Remove(s.path("nodes.d", id+".json")), "节点", id)
}

// ---- 订阅 ----

// SaveSub 写入订阅（含订阅地址与节点凭据，0600）；create 为 true 时已存在则返回 ErrExists。
func (s *Store) SaveSub(sub model.Subscription, create bool) error {
	if err := os.MkdirAll(s.path("subs"), 0o700); err != nil {
		return err
	}
	p := s.path("subs", sub.ID+".json")
	if create && exists(p) {
		return fmt.Errorf("订阅 %s %w", sub.ID, ErrExists)
	}
	return writeJSON(p, sub)
}

// Sub 读取一个订阅。
func (s *Store) Sub(id string) (model.Subscription, error) {
	var sub model.Subscription
	if err := model.ValidateID("订阅", id); err != nil {
		return sub, err
	}
	err := readJSON(s.path("subs", id+".json"), &sub)
	return sub, wrapNotFound(err, "订阅", id)
}

// Subs 按 ID 顺序列出全部订阅。
func (s *Store) Subs() ([]model.Subscription, error) {
	names, err := listNames(s.path("subs"), ".json")
	if errors.Is(err, fs.ErrNotExist) {
		// 早期版本初始化的数据目录里没有这个子目录。
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]model.Subscription, 0, len(names))
	for _, id := range names {
		sub, err := s.Sub(id)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, nil
}

// DeleteSub 删除一个订阅。
func (s *Store) DeleteSub(id string) error {
	if err := model.ValidateID("订阅", id); err != nil {
		return err
	}
	return wrapNotFound(os.Remove(s.path("subs", id+".json")), "订阅", id)
}

// ---- 网关 ----

// SaveGateway 写入网关；create 为 true 时网关已存在则返回 ErrExists。
func (s *Store) SaveGateway(g model.Gateway, create bool) error {
	dir := s.path("gateways", g.ID)
	p := filepath.Join(dir, "gateway.json")
	if create && exists(p) {
		return fmt.Errorf("网关 %s %w", g.ID, ErrExists)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeJSON(p, g)
}

// Gateway 读取一个网关。
func (s *Store) Gateway(id string) (model.Gateway, error) {
	var g model.Gateway
	if err := model.ValidateID("网关", id); err != nil {
		return g, err
	}
	err := readJSON(s.path("gateways", id, "gateway.json"), &g)
	return g, wrapNotFound(err, "网关", id)
}

// Gateways 按 ID 顺序列出全部网关。
func (s *Store) Gateways() ([]model.Gateway, error) {
	names, err := listNames(s.path("gateways"), "")
	if err != nil {
		return nil, err
	}
	var out []model.Gateway
	for _, id := range names {
		g, err := s.Gateway(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// DeleteGateway 删除一个网关的全部文件。
func (s *Store) DeleteGateway(id string) error {
	if _, err := s.Gateway(id); err != nil {
		return err
	}
	return os.RemoveAll(s.path("gateways", id))
}

// ---- 槽位 ----

// SlotDir 返回槽位目录。
func (s *Store) SlotDir(id string) string { return s.path("slots", id) }

// HomeDir 返回槽位的 HOME 目录。
func (s *Store) HomeDir(id string) string { return s.path("slots", id, "home") }

// EtcDir 返回槽位里挂进 worker 的身份文件目录。
func (s *Store) EtcDir(id string) string { return s.path("slots", id, "etc") }

// SaveSlot 写入槽位并确保其子目录存在；create 为 true 时槽位已存在则返回 ErrExists。
func (s *Store) SaveSlot(sl model.Slot, create bool) error {
	p := filepath.Join(s.SlotDir(sl.ID), "slot.json")
	if create && exists(p) {
		return fmt.Errorf("槽位 %s %w", sl.ID, ErrExists)
	}
	for _, d := range []string{s.SlotDir(sl.ID), s.HomeDir(sl.ID), s.EtcDir(sl.ID)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return writeJSON(p, sl)
}

// Slot 读取一个槽位。
func (s *Store) Slot(id string) (model.Slot, error) {
	var sl model.Slot
	if err := model.ValidateID("槽位", id); err != nil {
		return sl, err
	}
	err := readJSON(filepath.Join(s.SlotDir(id), "slot.json"), &sl)
	return sl, wrapNotFound(err, "槽位", id)
}

// Slots 按 ID 顺序列出全部槽位。只剩 HOME 和身份、没有 slot.json 的目录不算槽位。
func (s *Store) Slots() ([]model.Slot, error) {
	names, err := listNames(s.path("slots"), "")
	if err != nil {
		return nil, err
	}
	var out []model.Slot
	for _, id := range names {
		sl, err := s.Slot(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, nil
}

// DeleteSlot 删除槽位。purge 为 false 时保留 HOME 与身份，
// 以后用同一个 ID 重新创建会沿用它们；为 true 时整个目录一并删除。
func (s *Store) DeleteSlot(id string, purge bool) error {
	if _, err := s.Slot(id); err != nil {
		return err
	}
	if purge {
		return os.RemoveAll(s.SlotDir(id))
	}
	for _, f := range []string{"slot.json", "applied.json", "hold.json"} {
		if err := os.Remove(filepath.Join(s.SlotDir(id), f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Identity 读取槽位身份；尚未生成时 ok 为 false。
func (s *Store) Identity(id string) (ident model.Identity, ok bool, err error) {
	err = readJSON(filepath.Join(s.SlotDir(id), "identity.json"), &ident)
	if errors.Is(err, fs.ErrNotExist) {
		return ident, false, nil
	}
	return ident, err == nil, err
}

// SaveIdentity 写入槽位身份；已有身份时返回 ErrExists，身份不允许被覆盖。
func (s *Store) SaveIdentity(id string, ident model.Identity) error {
	p := filepath.Join(s.SlotDir(id), "identity.json")
	if exists(p) {
		return fmt.Errorf("槽位 %s 的身份 %w", id, ErrExists)
	}
	if err := os.MkdirAll(s.SlotDir(id), 0o700); err != nil {
		return err
	}
	return writeJSON(p, ident)
}

// Applied 读取最近一次成功应用的记录；没有时 ok 为 false。
func (s *Store) Applied(id string) (a model.Applied, ok bool, err error) {
	err = readJSON(filepath.Join(s.SlotDir(id), "applied.json"), &a)
	if errors.Is(err, fs.ErrNotExist) {
		return a, false, nil
	}
	return a, err == nil, err
}

// SaveApplied 写入应用记录。
func (s *Store) SaveApplied(id string, a model.Applied) error {
	return writeJSON(filepath.Join(s.SlotDir(id), "applied.json"), a)
}

// ClearApplied 删除应用记录。
func (s *Store) ClearApplied(id string) error {
	return ignoreNotExist(os.Remove(filepath.Join(s.SlotDir(id), "applied.json")))
}

// Hold 读取手动断网标记；没有时 ok 为 false。
func (s *Store) Hold(id string) (h model.Hold, ok bool, err error) {
	err = readJSON(filepath.Join(s.SlotDir(id), "hold.json"), &h)
	if errors.Is(err, fs.ErrNotExist) {
		return h, false, nil
	}
	return h, err == nil, err
}

// SetHold 写入手动断网标记。
func (s *Store) SetHold(id string, h model.Hold) error {
	return writeJSON(filepath.Join(s.SlotDir(id), "hold.json"), h)
}

// ClearHold 删除手动断网标记。
func (s *Store) ClearHold(id string) error {
	return ignoreNotExist(os.Remove(filepath.Join(s.SlotDir(id), "hold.json")))
}

// WriteSlotFile 在槽位的 etc 目录下写一个文件，返回它的绝对路径。
// 文件会以只读方式挂进 worker，容器内的用户与宿主用户同 UID，因此权限用 0644 即可读。
func (s *Store) WriteSlotFile(id, name string, data []byte) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("文件名 %q 不合法", name)
	}
	if err := os.MkdirAll(s.EtcDir(id), 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(s.EtcDir(id), name)
	return p, writeFile(p, data, 0o644)
}

// WriteSlotDir 在槽位的 etc 目录下重建一个子目录并写入给定的文件，返回它的绝对路径。
func (s *Store) WriteSlotDir(id, name string, files map[string][]byte) (string, error) {
	if strings.ContainsAny(name, "/\\") || name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("目录名 %q 不合法", name)
	}
	dir := filepath.Join(s.EtcDir(id), name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for f, data := range files {
		if strings.ContainsAny(f, "/\\") {
			return "", fmt.Errorf("文件名 %q 不合法", f)
		}
		if err := writeFile(filepath.Join(dir, f), data, 0o444); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// AppendOp 追加一条操作记录。
func (s *Store) AppendOp(op model.Op) error {
	b, err := json.Marshal(op)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path("ops.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// ---- 文件读写 ----

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("解析 %s: %w", p, err)
	}
	return nil
}

func writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(p, append(b, '\n'), 0o600)
}

// writeFile 先写临时文件再改名，保证读到的要么是旧内容要么是完整的新内容。
func writeFile(p string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func ignoreNotExist(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func wrapNotFound(err error, kind, id string) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s %s %w", kind, id, ErrNotFound)
	}
	return err
}

// listNames 列出目录下的条目名（去掉后缀），忽略隐藏文件，按字典序返回。
func listNames(dir, suffix string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, suffix) {
			continue
		}
		out = append(out, strings.TrimSuffix(name, suffix))
	}
	sort.Strings(out)
	return out, nil
}
