package model

import (
	"reflect"
	"testing"
)

func TestContainerPath(t *testing.T) {
	mounts := []Mount{
		{Source: "/data/proj-b", Target: "/data/proj-b"},
		{Source: "/data/ref", Target: "/ref", ReadOnly: true},
		{Source: "/data/proj-b/vendor", Target: "/vendor"},
	}
	cases := map[string]string{
		"/data/proj-b":            "/data/proj-b",
		"/data/proj-b/log":        "/data/proj-b/log",
		"/data/proj-b/log/2026/":  "/data/proj-b/log/2026",
		"/data/ref/a/b":           "/ref/a/b",
		"/data/proj-b/vendor/x":   "/vendor/x",
		"/data/proj-b/vendor":     "/vendor",
		"/data/proj-b/../proj-b/": "/data/proj-b",
	}
	for in, want := range cases {
		if got, ok := ContainerPath(mounts, in); !ok || got != want {
			t.Errorf("ContainerPath(%q) = %q, %v；期望 %q", in, got, ok, want)
		}
	}
	// 只是名字有相同的前缀，并不在挂载之下。
	for _, in := range []string{"/data/proj-bb", "/data", "/data/proj", "/other"} {
		if got, ok := ContainerPath(mounts, in); ok {
			t.Errorf("ContainerPath(%q) 不应命中，得到 %q", in, got)
		}
	}
}

func TestAddWorkspaceDropsCoveredMounts(t *testing.T) {
	mounts := []Mount{
		{Source: "/data/repo/a", Target: "/data/repo/a"},
		{Source: "/data/repo/b/sub", Target: "/data/repo/b/sub"},
		{Source: "/data/repo/ro", Target: "/data/repo/ro", ReadOnly: true},
		{Source: "/data/repo/c", Target: "/work"},
		{Source: "/data/other", Target: "/data/other"},
	}
	got := AddWorkspace(mounts, "/data/repo/")
	want := []Mount{
		{Source: "/data/repo/ro", Target: "/data/repo/ro", ReadOnly: true},
		{Source: "/data/repo/c", Target: "/work"},
		{Source: "/data/other", Target: "/data/other"},
		{Source: "/data/repo", Target: "/data/repo"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("挂载列表不对:\n得到 %+v\n期望 %+v", got, want)
	}
	if _, ok := ContainerPath(got, "/data/repo/b/sub/x"); !ok {
		t.Fatal("原先单独挂载的子目录现在应由上级目录覆盖")
	}
}

func TestWorkspaceCandidates(t *testing.T) {
	got := WorkspaceCandidates("/home/kevin/work/proj/log", "/home/kevin")
	want := []string{"/home/kevin/work/proj/log", "/home/kevin/work/proj", "/home/kevin/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HOME 及其上级不能作为挂载根: %v", got)
	}
	got = WorkspaceCandidates("/data/team/repo", "/home/kevin")
	if !reflect.DeepEqual(got, []string{"/data/team/repo", "/data/team", "/data"}) {
		t.Fatalf("候选目录不对: %v", got)
	}
	if got := WorkspaceCandidates("/usr/local/src", ""); len(got) != 0 {
		t.Fatalf("系统目录不应成为候选: %v", got)
	}
	// 宿主的主目录和数据目录本身及其上级不能作为挂载根。
	got = WorkspaceCandidates("/home/pi/project/app", "/home/user", "/home/pi", "/home/pi/.cc-cluster")
	if !reflect.DeepEqual(got, []string{"/home/pi/project/app", "/home/pi/project"}) {
		t.Fatalf("主目录不应成为候选: %v", got)
	}
}

func TestCheckMountSource(t *testing.T) {
	protected := []string{"/home/pi", "/home/pi/.cc-cluster", ""}
	for _, bad := range []string{"/home/pi", "/home/pi/", "/home", "/home/pi/.cc-cluster"} {
		if CheckMountSource(bad, protected...) == nil {
			t.Errorf("%s 应被拒绝", bad)
		}
	}
	for _, ok := range []string{"/home/pi/project", "/data/repo", "/home/pineapple"} {
		if err := CheckMountSource(ok, protected...); err != nil {
			t.Errorf("%s 应被接受: %v", ok, err)
		}
	}
}

func TestCheckMountOutside(t *testing.T) {
	dirs := []string{"/home/pi/.cc-cluster", ""}
	for _, bad := range []string{"/home/pi/.cc-cluster/nodes.d", "/home/pi/.cc-cluster/slots/b/home", "/home/pi/.cc-cluster/slots/b/tmux/"} {
		if CheckMountOutside(bad, dirs...) == nil {
			t.Errorf("%s 应被拒绝", bad)
		}
	}
	// 数据目录本身与它的上级由 CheckMountSource 拦住，这里只管位于其下的。
	for _, ok := range []string{"/home/pi/.cc-cluster", "/home/pi", "/home/pi/.cc-cluster-old/x", "/data/repo"} {
		if err := CheckMountOutside(ok, dirs...); err != nil {
			t.Errorf("%s 应被接受: %v", ok, err)
		}
	}
}

func TestCheckMountSocket(t *testing.T) {
	sockets := []string{"/var/run/docker.sock", "/run/user/1000/docker.sock", ""}
	for _, bad := range []string{"/var/run/docker.sock", "/var/run", "/var/run/", "/var", "/run/user/1000", "/run", "/"} {
		if CheckMountSocket(bad, sockets...) == nil {
			t.Errorf("%s 应被拒绝", bad)
		}
	}
	for _, ok := range []string{"/var/runner", "/var/run/other", "/data/repo", "/run/user/1001"} {
		if err := CheckMountSocket(ok, sockets...); err != nil {
			t.Errorf("%s 应被接受: %v", ok, err)
		}
	}
}

func TestLinkNameAndDefaultDir(t *testing.T) {
	home := "/home/user"
	if n, ok := LinkName("/home/pi/project", home); !ok || n != "project" {
		t.Fatalf("链接名不对: %q %v", n, ok)
	}
	for _, target := range []string{"/home/user/work", "/data/.cache"} {
		if _, ok := LinkName(target, home); ok {
			t.Errorf("%s 不应建链接", target)
		}
	}
	mounts := []Mount{{Source: "/x", Target: "/home/user/x"}, {Source: "/data/a", Target: "/data/a"}}
	if got := DefaultDir(mounts, home); got != "/data/a" {
		t.Fatalf("默认目录应是第一个 HOME 之外的挂载: %s", got)
	}
	if got := DefaultDir(nil, home); got != home {
		t.Fatalf("没有挂载时默认目录应是 HOME: %s", got)
	}
}
