// Package dockerapi 是一个只覆盖本项目所需接口的 Docker Engine API 客户端。
//
// 直接经 Unix socket 说 HTTP，不依赖 docker 命令行和第三方库：
// 管理器在任何"用户属于 docker 组"的机器上都能工作，也不读取用户的 docker 配置文件。
package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultSocket = "/var/run/docker.sock"
	// 请求时使用的 API 版本上限；服务端更旧时用服务端的版本。
	maxAPIVersion = "1.44"
	// 本项目用到的字段（MaskedPaths 等）要求的最低版本。
	minAPIVersion = "1.40"
)

// Client 是到一个 Docker 守护进程的连接。
type Client struct {
	http    *http.Client
	version string
	server  Version
}

// Version 是守护进程的版本信息。
type Version struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	KernelVersion string `json:"KernelVersion"`
}

// Error 是守护进程返回的非 2xx 应答。
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("docker: %s (HTTP %d)", e.Message, e.Status)
}

// IsNotFound 报告错误是否为"对象不存在"。
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// New 连接 Docker 守护进程并协商 API 版本。
// host 为空时取环境变量 DOCKER_HOST，再为空则用默认 socket；只支持 unix:// 形式。
func New(ctx context.Context, host string) (*Client, error) {
	if host == "" {
		host = os.Getenv("DOCKER_HOST")
	}
	sock := defaultSocket
	if host != "" {
		p, ok := strings.CutPrefix(host, "unix://")
		if !ok {
			return nil, fmt.Errorf("只支持 unix:// 形式的 Docker 地址，得到 %q", host)
		}
		sock = p
	}
	c := &Client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}}
	if err := c.do(ctx, http.MethodGet, "/version", nil, nil, &c.server); err != nil {
		return nil, fmt.Errorf("连接 Docker（%s）失败：%w", sock, err)
	}
	c.version = c.server.APIVersion
	if versionLess(maxAPIVersion, c.version) {
		c.version = maxAPIVersion
	}
	if versionLess(c.version, minAPIVersion) {
		return nil, fmt.Errorf("Docker API %s 太旧，至少需要 %s", c.server.APIVersion, minAPIVersion)
	}
	return c, nil
}

// Server 返回守护进程的版本信息。
func (c *Client) Server() Version { return c.server }

// APIVersion 返回协商后使用的 API 版本。
func (c *Client) APIVersion() string { return c.version }

// versionLess 比较形如 "1.44" 的版本号。
func versionLess(a, b string) bool {
	am, an := splitVersion(a)
	bm, bn := splitVersion(b)
	return am < bm || (am == bm && an < bn)
}

func splitVersion(v string) (int, int) {
	major, minor, _ := strings.Cut(v, ".")
	a, _ := strconv.Atoi(major)
	b, _ := strconv.Atoi(minor)
	return a, b
}

// request 发出一个请求并返回应答；非 2xx 应答转成 *Error。调用方负责关闭 Body。
func (c *Client) request(ctx context.Context, method, path string, query url.Values, contentType string, body io.Reader) (*http.Response, error) {
	u := "http://docker"
	if c.version != "" {
		u += "/v" + c.version
	}
	u += path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	// 304 表示"已经是目标状态"（例如启动一个已在运行的容器），按成功处理。
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &m) != nil || m.Message == "" {
			m.Message = strings.TrimSpace(string(b))
		}
		return nil, &Error{Status: resp.StatusCode, Message: m.Message}
	}
	return resp, nil
}

// do 发出一个 JSON 请求；in 非 nil 时作为请求体，out 非 nil 时解析应答体。
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = bytes.NewReader(b), "application/json"
	}
	resp, err := c.request(ctx, method, path, query, ct, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Info 是 /info 里本项目关心的字段。
type Info struct {
	ServerVersion   string   `json:"ServerVersion"`
	OperatingSystem string   `json:"OperatingSystem"`
	KernelVersion   string   `json:"KernelVersion"`
	CgroupVersion   string   `json:"CgroupVersion"`
	Driver          string   `json:"Driver"`
	SecurityOptions []string `json:"SecurityOptions"`
	NCPU            int      `json:"NCPU"`
	MemTotal        int64    `json:"MemTotal"`
}

// Info 读取守护进程信息。
func (c *Client) Info(ctx context.Context) (Info, error) {
	var i Info
	err := c.do(ctx, http.MethodGet, "/info", nil, nil, &i)
	return i, err
}

// ImageExists 报告本地是否有这个镜像。
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil, nil)
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}
