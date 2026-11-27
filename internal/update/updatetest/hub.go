// Package updatetest 提供一个假的 GitHub 发布页，给用到 internal/update 的测试用。
// 它不依赖 internal/update：文件名和接口的样子按 GitHub 与发布工作流的实际写法另写一遍，
// 这样被测代码把名字拼错时测试会失败，而不是跟着一起错。
package updatetest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Release 是接口里一个 Release 的字段。
type Release struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// Hub 是假的 GitHub：接口和下载都在同一个地址上。
type Hub struct {
	*httptest.Server
	// Releases 是接口返回的发布，顺序由测试决定。
	Releases []Release
	// Limited 为真时接口一律回“被限流”；下载和页面跳转不受影响。
	Limited bool
	// Gate 非 nil 时，接口收到请求后先等它被关闭（或对方放弃）再应答；下载和页面跳转不受影响。
	Gate chan struct{}
	// APICalls 是接口被请求的次数。
	APICalls atomic.Int32
	// Files 是可以下载的文件，键是 "<标签>/<文件名>"。
	Files map[string][]byte
}

// New 启动一个假的 GitHub，repo 是 "所有者/仓库"。测试结束时自动关闭。
func New(t testing.TB, repo string) *Hub {
	t.Helper()
	h := &Hub{Files: map[string][]byte{}}
	stable := func() (Release, bool) {
		for _, r := range h.Releases {
			if !r.Draft && !r.Prerelease {
				return r, true
			}
		}
		return Release{}, false
	}
	closing := make(chan struct{})
	t.Cleanup(func() { close(closing) })
	api := func(w http.ResponseWriter, r *http.Request, v any) {
		h.APICalls.Add(1)
		if h.Gate != nil {
			select {
			case <-h.Gate:
			case <-r.Context().Done():
				return
			case <-closing:
				return
			}
		}
		if h.Limited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/"+repo+"/releases", func(w http.ResponseWriter, r *http.Request) { api(w, r, h.Releases) })
	mux.HandleFunc("GET /repos/"+repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if rel, ok := stable(); ok {
			api(w, r, rel)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /repos/"+repo+"/releases/tags/{tag}", func(w http.ResponseWriter, r *http.Request) {
		for _, rel := range h.Releases {
			if rel.TagName == r.PathValue("tag") {
				api(w, r, rel)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /"+repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if rel, ok := stable(); ok {
			http.Redirect(w, r, "/"+repo+"/releases/tag/"+rel.TagName, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /"+repo+"/releases/download/{tag}/{file}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := h.Files[r.PathValue("tag")+"/"+r.PathValue("file")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	h.Server = httptest.NewServer(mux)
	t.Cleanup(h.Close)
	return h
}

// Add 登记一个正式版；day 是 2026 年 11 月的几号，只为了让发布时间各不相同。
func (h *Hub) Add(tag string, day int, body string) {
	h.Releases = append(h.Releases, Release{
		TagName: tag, HTMLURL: "https://github.example/releases/tag/" + tag, Body: body,
		PublishedAt: time.Date(2026, 11, day, 8, 0, 0, 0, time.UTC),
	})
}

// PackageName 是发布工作流给一个平台的包起的名字（不带 .tar.gz）。
func PackageName(v, goos, goarch string) string {
	return "cc-cluster_" + v + "_" + goos + "_" + goarch
}

// Publish 往 v 这个版本里放一个平台的包，并把它记进校验和文件；binary 是包里 cc-cluster 的内容。
func (h *Hub) Publish(t testing.TB, v, goos, goarch string, binary []byte) {
	t.Helper()
	name := PackageName(v, goos, goarch)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{name + "/README.md", []byte("readme")}, {name + "/cc-cluster", binary}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	h.Files[v+"/"+name+".tar.gz"] = buf.Bytes()
	h.Files[v+"/SHA256SUMS"] = append(h.Files[v+"/SHA256SUMS"], []byte(hex.EncodeToString(sum[:])+"  "+name+".tar.gz\n")...)
}

// Script 是一个冒充 cc-cluster 的脚本：不管带什么参数都打印给定的版本号。
func Script(v string) []byte {
	return []byte("#!/bin/sh\necho " + v + "\n")
}
