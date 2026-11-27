package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/update/updatetest"
)

func TestCompare(t *testing.T) {
	order := []string{"v0.0.1", "v0.0.2", "v0.0.10", "v0.1.0-alpha.1", "v0.1.0-alpha.2", "v0.1.0-alpha.10", "v0.1.0-rc.1", "v0.1.0", "v1.0.0"}
	for i := range order {
		for j := range order {
			got := Compare(order[i], order[j])
			if (got < 0) != (i < j) || (got > 0) != (i > j) {
				t.Errorf("Compare(%s, %s) = %d", order[i], order[j], got)
			}
		}
	}
	if !Newer("v0.0.3", "v0.0.2") || Newer("v0.0.2", "v0.0.2") || Newer("v0.0.1", "v0.0.2") {
		t.Error("Newer 的结果不对")
	}
	// 从源码构建的版本号没法比较：既不算新也不算旧。
	if Newer("v0.0.3", "768d650") || Newer("768d650", "v0.0.3") {
		t.Error("不是版本号的不应参与比较")
	}
}

func TestIsRelease(t *testing.T) {
	for _, v := range []string{"v0.0.1", "v1.2.3", "v0.1.0-alpha.1", "v1.0.0-rc.1"} {
		if !IsRelease(v) {
			t.Errorf("%s 应当算发布版", v)
		}
	}
	// git describe 给从源码构建的二进制起的名字，和别的乱七八糟的写法。
	for _, v := range []string{"", "dev", "768d650", "v0.0.2-3-g768d650", "v0.0.2-dirty", "v0.0.2-3-g768d650-dirty", "ci-768d650", "0.0.2", "v0.0", "v0.0.2/../x", "v0.0.2\n"} {
		if IsRelease(v) {
			t.Errorf("%q 不应算发布版", v)
		}
	}
}

func newHub(t *testing.T) (*updatetest.Hub, *Client) {
	t.Helper()
	h := updatetest.New(t, Repo)
	c := NewClient("test")
	c.API, c.Site = h.URL, h.URL
	return h, c
}

func versions(list []Release) string {
	var tags []string
	for _, r := range list {
		tags = append(tags, r.Version)
	}
	return strings.Join(tags, " ")
}

func TestReleasesSkipsDraftsAndPrereleases(t *testing.T) {
	h, c := newHub(t)
	h.Add("v0.0.2", 25, "二")
	h.Add("v0.0.5", 30, "草稿")
	h.Add("v0.1.0-rc.1", 29, "预发布")
	h.Add("nightly", 20, "滚动")
	h.Add("v0.0.3", 27, "三\r\n- 一条\r\n")
	h.Add("v0.0.1", 24, "一")
	h.Releases[1].Draft, h.Releases[2].Prerelease = true, true

	got, err := c.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if versions(got) != "v0.0.3 v0.0.2 v0.0.1" {
		t.Fatalf("应只有正式版，新的在前: %s", versions(got))
	}
	if got[0].Notes != "三\n- 一条" || got[0].PublishedAt.Day() != 27 {
		t.Fatalf("更新说明或发布时间不对: %+v", got[0])
	}
}

func TestCheck(t *testing.T) {
	h, c := newHub(t)
	h.Add("v0.0.4", 28, "四")
	h.Add("v0.0.3", 27, "三")
	h.Add("v0.0.2", 25, "二")
	h.Add("v0.0.1", 24, "一")
	ctx := context.Background()

	// 落后两个版本：两份说明都给，新的在前。
	got, err := c.Check(ctx, "v0.0.2")
	if err != nil || !got.Newer || got.Latest.Version != "v0.0.4" || versions(got.Releases) != "v0.0.4 v0.0.3" {
		t.Fatalf("落后时: %+v %v", got, err)
	}
	// 已经是最新：给现在这个版本的说明。
	got, err = c.Check(ctx, "v0.0.4")
	if err != nil || got.Newer || versions(got.Releases) != "v0.0.4" || got.Releases[0].Notes != "四" {
		t.Fatalf("已是最新时: %+v %v", got, err)
	}
	// 从源码构建的：不算有新版本，给最新版的说明。
	got, err = c.Check(ctx, "768d650")
	if err != nil || got.Newer || versions(got.Releases) != "v0.0.4" {
		t.Fatalf("从源码构建时: %+v %v", got, err)
	}
	// 比发布的还新（预发布之类）：不算有新版本。
	if got, err = c.Check(ctx, "v0.1.0-rc.1"); err != nil || got.Newer {
		t.Fatalf("比最新的还新时: %+v %v", got, err)
	}
}

func TestCheckFallsBackWhenRateLimited(t *testing.T) {
	h, c := newHub(t)
	h.Add("v0.0.3", 27, "三")
	h.Limited = true
	ctx := context.Background()

	got, err := c.Check(ctx, "v0.0.2")
	if err != nil || !got.Newer || got.Latest.Version != "v0.0.3" || got.Latest.Notes != "" || !got.Latest.PublishedAt.IsZero() {
		t.Fatalf("被限流时应从跳转里读到版本号: %+v %v", got, err)
	}
	if !strings.HasSuffix(got.Latest.URL, "/releases/tag/v0.0.3") || versions(got.Releases) != "v0.0.3" {
		t.Fatalf("应给出发布页的地址: %+v", got)
	}
	if _, err := c.Releases(ctx); err == nil || !strings.Contains(err.Error(), "限流") {
		t.Fatalf("列表接口被限流时应说明原因: %v", err)
	}

	h.Releases = nil
	if _, err := c.Check(ctx, "v0.0.2"); err == nil || !strings.Contains(err.Error(), "限流") {
		t.Fatalf("一个版本都查不到时应报出接口的错误: %v", err)
	}
}

// oldBinary 在一个空目录里放一个“旧的二进制”，返回它的路径。
func oldBinary(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "cc-cluster")
	if err := os.WriteFile(target, updatetest.Script("v0.0.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

// onlyOld 确认目录里只剩那个没被动过的旧二进制。
func onlyOld(t *testing.T, target string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil || len(entries) != 1 {
		t.Fatalf("目录里应只有原来的二进制: %v %v", entries, err)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, updatetest.Script("v0.0.2")) {
		t.Fatalf("原来的二进制被改动了: %q", b)
	}
}

func TestAssetNameMatchesReleaseWorkflow(t *testing.T) {
	if got, want := AssetName("v0.0.3", "linux", "arm64"), updatetest.PackageName("v0.0.3", "linux", "arm64"); got != want {
		t.Fatalf("包名应与发布工作流的写法一致: %s != %s", got, want)
	}
}

func TestStageThenCommitReplacesBinary(t *testing.T) {
	h, c := newHub(t)
	h.Publish(t, "v0.0.3", "linux", "amd64", updatetest.Script("v0.0.3"))
	h.Publish(t, "v0.0.3", "linux", "arm64", updatetest.Script("v0.0.3"))
	target := oldBinary(t)
	if err := os.Chmod(target, 0o750); err != nil {
		t.Fatal(err)
	}

	s, err := c.stage(context.Background(), "v0.0.3", target, "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	// 走到这里旧的还在原处。
	if b, _ := os.ReadFile(target); !bytes.Equal(b, updatetest.Script("v0.0.2")) {
		t.Fatalf("替换之前不应改动原来的二进制: %q", b)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(b, updatetest.Script("v0.0.3")) {
		t.Fatalf("应换成新的: %q %v", b, err)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o750 {
		t.Fatalf("应沿用原来的权限: %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 1 {
		t.Fatalf("不应留下临时文件: %v", entries)
	}
	if err := s.Commit(); err == nil {
		t.Fatal("替换过之后不能再替换一次")
	}
	s.Discard() // 替换之后调用不应删掉任何东西
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
}

func TestStageDiscard(t *testing.T) {
	h, c := newHub(t)
	h.Publish(t, "v0.0.3", "linux", "amd64", updatetest.Script("v0.0.3"))
	target := oldBinary(t)
	s, err := c.stage(context.Background(), "v0.0.3", target, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	s.Discard()
	onlyOld(t, target)
}

func TestStageFailuresLeaveBinaryAlone(t *testing.T) {
	pkg := updatetest.PackageName("v0.0.3", "linux", "amd64") + ".tar.gz"
	cases := []struct {
		name  string
		setup func(t *testing.T, h *updatetest.Hub)
		want  string
	}{
		{"校验和对不上", func(t *testing.T, h *updatetest.Hub) {
			h.Publish(t, "v0.0.3", "linux", "amd64", updatetest.Script("v0.0.3"))
			b := h.Files["v0.0.3/"+pkg]
			h.Files["v0.0.3/"+pkg] = append([]byte{}, b[:len(b)-1]...)
		}, "校验和对不上"},
		{"没有这个平台的包", func(t *testing.T, h *updatetest.Hub) {
			h.Publish(t, "v0.0.3", "darwin", "arm64", updatetest.Script("v0.0.3"))
		}, "没有 linux/amd64 的包"},
		{"没有这个版本", func(*testing.T, *updatetest.Hub) {}, "校验和文件"},
		{"包里的程序报告的是别的版本", func(t *testing.T, h *updatetest.Hub) {
			h.Publish(t, "v0.0.3", "linux", "amd64", updatetest.Script("v0.0.1"))
		}, "不是 v0.0.3"},
		{"包里的程序运行不了", func(t *testing.T, h *updatetest.Hub) {
			h.Publish(t, "v0.0.3", "linux", "amd64", []byte("#!/bin/sh\nexit 3\n"))
		}, "运行不了"},
		{"包里没有 cc-cluster", func(t *testing.T, h *updatetest.Hub) {
			// 把另一个版本的包冒充成这个版本的：校验和照着它写，里面的目录名却对不上。
			h.Publish(t, "v0.0.4", "linux", "amd64", updatetest.Script("v0.0.4"))
			b := h.Files["v0.0.4/"+updatetest.PackageName("v0.0.4", "linux", "amd64")+".tar.gz"]
			h.Files["v0.0.3/"+pkg] = b
			h.Files["v0.0.3/SHA256SUMS"] = []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(b), pkg))
		}, "没有找到"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, c := newHub(t)
			tc.setup(t, h)
			target := oldBinary(t)
			_, err := c.stage(context.Background(), "v0.0.3", target, "linux", "amd64")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应当失败并说明 %q，得到 %v", tc.want, err)
			}
			onlyOld(t, target)
		})
	}
}

func TestStageRejectsMalformedVersion(t *testing.T) {
	_, c := newHub(t)
	target := oldBinary(t)
	for _, v := range []string{"", "latest", "v0.0.3/../../x", "0.0.3"} {
		if _, err := c.stage(context.Background(), v, target, "linux", "amd64"); err == nil {
			t.Errorf("版本号 %q 应被拒绝", v)
		}
	}
	onlyOld(t, target)
}

func TestImageVersion(t *testing.T) {
	g, w := Images("v0.0.3")
	if g != "ghcr.io/sheyestherrr/cc-cluster-gateway:v0.0.3" || w != "ghcr.io/sheyestherrr/cc-cluster-worker:v0.0.3" {
		t.Fatalf("发布镜像的名字不对: %s %s", g, w)
	}
	for ref, want := range map[string]string{g: "v0.0.3", w: "v0.0.3", ImageRepo + "gateway:v0.1.0-rc.1": "v0.1.0-rc.1"} {
		if got, ok := ImageVersion(ref); !ok || got != want {
			t.Errorf("%s 应认作发布的镜像（%s），得到 %q %v", ref, want, got, ok)
		}
	}
	// 自己构建的、别人的、按摘要固定的、滚动标签的都不算，更新时不碰。
	for _, ref := range []string{"", "cc-cluster-gateway:dev", "ghcr.io/someone/cc-cluster-gateway:v0.0.3", ImageRepo + "gateway", ImageRepo + "gateway:latest", ImageRepo + "other:v0.0.3", ImageRepo + "gateway@sha256:abcd", ImageRepo + "gateway:v0.0.3@sha256:abcd"} {
		if got, ok := ImageVersion(ref); ok {
			t.Errorf("%q 不应认作发布的镜像（%s）", ref, got)
		}
	}

	old, _ := Images("v0.0.2")
	if from, behind := ImagesBehind(old, w, "v0.0.3"); !behind || from != "v0.0.2" {
		t.Errorf("有一个落后就算落后: %q %v", from, behind)
	}
	if _, behind := ImagesBehind(g, w, "v0.0.3"); behind {
		t.Error("两个都是这个版本时不算落后")
	}
	if _, behind := ImagesBehind("my/gw:1", "cc-cluster-worker:dev", "v0.0.3"); behind {
		t.Error("自己指定的镜像不算落后")
	}
}

func TestLocalNotes(t *testing.T) {
	for _, c := range [][2]string{
		{"中文\n- 一条\n\n---\n\nEnglish\n\n---\n\n镜像", "中文\n- 一条"},
		{"没有分隔线\n- 一条\n", "没有分隔线\n- 一条"},
		{"---\n只有后半段", "---\n只有后半段"},
		{"表格 | a\n--- | ---\n不是分隔线", "表格 | a\n--- | ---\n不是分隔线"},
		{"", ""},
	} {
		if got := LocalNotes(c[0]); got != c[1] {
			t.Errorf("LocalNotes(%q) = %q，应为 %q", c[0], got, c[1])
		}
	}
}

func TestFindSum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	list := []byte("deadbeef  short.tar.gz\n" + strings.ToUpper(sum) + " *a.tar.gz\n" + sum + "  b.tar.gz\n")
	if findSum(list, "a.tar.gz") != sum || findSum(list, "b.tar.gz") != sum {
		t.Error("应认得带星号和不带星号的两种写法")
	}
	if findSum(list, "short.tar.gz") != "" || findSum(list, "c.tar.gz") != "" {
		t.Error("长度不对的和没有的都应当找不到")
	}
}
