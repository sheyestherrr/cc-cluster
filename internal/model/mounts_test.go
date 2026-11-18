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
}
