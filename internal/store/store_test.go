package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	c := model.Cluster{}
	c.Normalize()
	s, err := Init(filepath.Join(t.TempDir(), "data"), c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestInitAndOpen(t *testing.T) {
	s := newStore(t)
	if mode(t, s.Dir()) != 0o700 {
		t.Fatalf("数据目录权限应为 0700，得到 %o", mode(t, s.Dir()))
	}
	if _, err := Init(s.Dir(), model.Cluster{}); !errors.Is(err, ErrExists) {
		t.Fatalf("重复初始化应报已存在: %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("打开未初始化的目录应报错")
	}
	o, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := o.Cluster()
	if err != nil || c.Name != model.DefaultClusterName {
		t.Fatalf("集群设置读回不对: %+v %v", c, err)
	}
}

func TestNodeRoundTripAndPermissions(t *testing.T) {
	s := newStore(t)
	n := model.Node{ID: "home-a", Role: model.RoleLanding, Proxy: map[string]any{"type": "socks5", "server": "198.51.100.3", "port": float64(1080), "password": "p"}}
	if err := s.SaveNode(n, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNode(n, true); !errors.Is(err, ErrExists) {
		t.Fatalf("重复创建应报已存在: %v", err)
	}
	if got := mode(t, filepath.Join(s.Dir(), "nodes.d", "home-a.json")); got != 0o600 {
		t.Fatalf("含凭据的节点文件权限应为 0600，得到 %o", got)
	}
	back, err := s.Node("home-a")
	if err != nil || back.Port() != 1080 || back.Server() != "198.51.100.3" {
		t.Fatalf("节点读回不对: %+v %v", back, err)
	}
	if _, err := s.Node("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("读不存在的节点应报不存在: %v", err)
	}
	if _, err := s.Node("../../etc/passwd"); err == nil {
		t.Fatal("带路径分隔符的 ID 必须被拒绝")
	}
	list, _ := s.Nodes()
	if len(list) != 1 {
		t.Fatalf("应列出 1 个节点: %d", len(list))
	}
	if err := s.DeleteNode("home-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode("home-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应报不存在: %v", err)
	}
}

func TestSlotLifecycleKeepsIdentityAndHome(t *testing.T) {
	s := newStore(t)
	sl := model.Slot{ID: "a", Gateway: "main", Landing: "home-a", Subnet: "10.213.250.0/30", UID: 1000, GID: 1000}
	sl.Normalize()
	if err := s.SaveSlot(sl, true); err != nil {
		t.Fatal(err)
	}
	ident := model.Identity{MachineID: strings.Repeat("ab", 16), Hostname: "dev-x", MAC: "02:00:00:00:00:01", CreatedAt: time.Now().UTC()}
	if err := s.SaveIdentity("a", ident); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIdentity("a", model.Identity{MachineID: "other"}); !errors.Is(err, ErrExists) {
		t.Fatalf("身份不允许被覆盖: %v", err)
	}
	marker := filepath.Join(s.HomeDir("a"), ".claude.json")
	if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := s.Hold("a"); ok {
		t.Fatal("新槽位不应处于 hold")
	}
	if err := s.SetHold("a", model.Hold{Reason: "测试", At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if h, ok, _ := s.Hold("a"); !ok || h.Reason != "测试" {
		t.Fatalf("hold 读回不对: %+v %v", h, ok)
	}
	if err := s.SaveApplied("a", model.Applied{Digest: "d", ExitIP: "198.51.100.3"}); err != nil {
		t.Fatal(err)
	}

	// 不带 purge 删除：期望配置与标记消失，HOME 与身份保留。
	if err := s.DeleteSlot("a", false); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Slots(); len(list) != 0 {
		t.Fatalf("删除后不应再列出: %v", list)
	}
	if _, ok, _ := s.Hold("a"); ok {
		t.Fatal("删除槽位应清掉 hold")
	}
	if _, ok, _ := s.Applied("a"); ok {
		t.Fatal("删除槽位应清掉应用记录")
	}
	if got, ok, _ := s.Identity("a"); !ok || got.MachineID != ident.MachineID {
		t.Fatal("不带 purge 时身份应保留")
	}
	if !exists(marker) {
		t.Fatal("不带 purge 时 HOME 应保留")
	}

	// 用同一 ID 重建后 purge：整个目录消失。
	if err := s.SaveSlot(sl, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSlot("a", true); err != nil {
		t.Fatal(err)
	}
	if exists(s.SlotDir("a")) {
		t.Fatal("purge 后槽位目录应消失")
	}
}

func TestWriteSlotFileAndOps(t *testing.T) {
	s := newStore(t)
	p, err := s.WriteSlotFile("a", "machine-id", []byte("abc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p) || mode(t, p) != 0o644 {
		t.Fatalf("身份文件应是绝对路径且可读: %s %o", p, mode(t, p))
	}
	if _, err := s.WriteSlotFile("a", "../x", nil); err == nil {
		t.Fatal("带路径分隔符的文件名必须被拒绝")
	}
	for i := 0; i < 2; i++ {
		if err := s.AppendOp(model.Op{Time: time.Now().UTC(), Op: "slot.up", Target: "a", OK: true}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir(), "ops.jsonl"))
	if strings.Count(string(b), "\n") != 2 {
		t.Fatalf("操作记录应是每条一行: %q", b)
	}
}

func TestSlotFilesRejectInvalidID(t *testing.T) {
	s := newStore(t)
	// 数据目录之外放一个文件：ID 里带路径分隔符时不应读到、改到或删到它。
	outside := filepath.Join(filepath.Dir(s.Dir()), "hold.json")
	if err := os.WriteFile(outside, []byte(`{"reason":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../..", "a/../../..", "", ".", "A", "a b"} {
		if _, _, err := s.Hold(id); err == nil {
			t.Errorf("Hold(%q) 应报错", id)
		}
		if _, _, err := s.Identity(id); err == nil {
			t.Errorf("Identity(%q) 应报错", id)
		}
		if _, _, err := s.Applied(id); err == nil {
			t.Errorf("Applied(%q) 应报错", id)
		}
		if _, _, err := s.Watch(id); err == nil {
			t.Errorf("Watch(%q) 应报错", id)
		}
		if err := s.SetHold(id, model.Hold{Reason: "x"}); err == nil {
			t.Errorf("SetHold(%q) 应报错", id)
		}
		if err := s.ClearHold(id); err == nil {
			t.Errorf("ClearHold(%q) 应报错", id)
		}
		if err := s.SaveIdentity(id, model.Identity{}); err == nil {
			t.Errorf("SaveIdentity(%q) 应报错", id)
		}
		if _, err := s.WriteSlotFile(id, "machine-id", nil); err == nil {
			t.Errorf("WriteSlotFile(%q) 应报错", id)
		}
		if _, err := s.WriteSlotDir(id, "dmi", nil); err == nil {
			t.Errorf("WriteSlotDir(%q) 应报错", id)
		}
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != `{"reason":"outside"}` {
		t.Fatalf("数据目录之外的文件不应被改动: %q %v", b, err)
	}
	// 合法的 ID 照常：没有文件时 ok 为 false、不报错。
	if _, ok, err := s.Hold("a"); ok || err != nil {
		t.Fatalf("没有标记时应返回 ok=false: %v %v", ok, err)
	}
}

func TestLockIsExclusive(t *testing.T) {
	s := newStore(t)
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u, err := s.Lock()
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("第二次加锁不应在第一把锁释放前成功")
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("解锁后第二次加锁应成功")
	}
}
