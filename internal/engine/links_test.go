package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

func TestLinkMounts(t *testing.T) {
	homeDir, home := t.TempDir(), "/home/user"
	// HOME 里已有同名的文件时不动它。
	if err := os.WriteFile(filepath.Join(homeDir, "notes"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounts := []model.Mount{
		{Source: "/home/pi/project", Target: "/home/pi/project"},
		{Source: "/data/notes", Target: "/data/notes"},
		{Source: "/data/in-home", Target: "/home/user/in-home"},
	}
	linkMounts(homeDir, home, mounts)
	linkMounts(homeDir, home, mounts) // 重复执行不出错、不改变结果

	if dst, err := os.Readlink(filepath.Join(homeDir, "project")); err != nil || dst != "/home/pi/project" {
		t.Fatalf("应建出指向挂载目标的链接: %q %v", dst, err)
	}
	if fi, err := os.Lstat(filepath.Join(homeDir, "notes")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("已有的同名文件应保持原样: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(homeDir, "in-home")); err == nil {
		t.Fatal("目标在 HOME 里的挂载不应建链接")
	}

	unlinkMount(homeDir, home, "/data/notes") // 不是我们建的，不能删
	if _, err := os.Lstat(filepath.Join(homeDir, "notes")); err != nil {
		t.Fatal("不是链接的同名文件被删掉了")
	}
	unlinkMount(homeDir, home, "/home/pi/project")
	if _, err := os.Lstat(filepath.Join(homeDir, "project")); err == nil {
		t.Fatal("去掉挂载后链接应被删除")
	}
}

func TestPromptScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("没有 bash")
	}
	// 非交互的 bash 启动时会清掉 PS1，所以交互与否用 -i 来区分。
	run := func(args ...string) string {
		args = append([]string{"--noprofile", "--norc"}, args...)
		cmd := exec.Command(bash, append(args, "-c", profilePromptScript+`printf %s "$PS1"`)...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	got := run("-i")
	if !strings.Contains(got, `\u`) || !strings.Contains(got, `\w`) || !strings.Contains(got, `\A`) || !strings.HasSuffix(got, "\\n❯ ") {
		t.Fatalf("提示符不对: %q", got)
	}
	if strings.Contains(got, `\h`) {
		t.Fatalf("提示符不应带 hostname: %q", got)
	}
	if got := run(); got != "" {
		t.Fatalf("非交互的 shell 不应设置提示符: %q", got)
	}
}
