package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ContainerSpec 是创建容器的请求体。字段名与 Engine API 一致。
type ContainerSpec struct {
	Image            string            `json:"Image"`
	Hostname         string            `json:"Hostname,omitempty"`
	User             string            `json:"User,omitempty"`
	Env              []string          `json:"Env,omitempty"`
	Cmd              []string          `json:"Cmd,omitempty"`
	Entrypoint       []string          `json:"Entrypoint,omitempty"`
	WorkingDir       string            `json:"WorkingDir,omitempty"`
	Labels           map[string]string `json:"Labels,omitempty"`
	HostConfig       HostConfig        `json:"HostConfig"`
	NetworkingConfig *NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

// HostConfig 是容器的宿主侧设置。未设置重启策略，即不自动重启。
type HostConfig struct {
	NetworkMode    string            `json:"NetworkMode,omitempty"`
	PidMode        string            `json:"PidMode,omitempty"`
	Binds          []string          `json:"Binds,omitempty"`
	CapDrop        []string          `json:"CapDrop,omitempty"`
	CapAdd         []string          `json:"CapAdd,omitempty"`
	SecurityOpt    []string          `json:"SecurityOpt,omitempty"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs,omitempty"`
	Tmpfs          map[string]string `json:"Tmpfs,omitempty"`
	Devices        []DeviceMapping   `json:"Devices,omitempty"`
	Sysctls        map[string]string `json:"Sysctls,omitempty"`
	// CgroupnsMode 为 private 时容器看不到自己在宿主 cgroup 树里的路径（其中带容器 ID）。
	CgroupnsMode string `json:"CgroupnsMode,omitempty"`
	// ExtraHosts 追加到容器的 /etc/hosts，每项形如 名字:地址。
	ExtraHosts []string `json:"ExtraHosts,omitempty"`
	DNS        []string `json:"Dns,omitempty"`
	DNSSearch  []string `json:"DnsSearch,omitempty"`
	Memory     int64    `json:"Memory,omitempty"`
	NanoCPUs   int64    `json:"NanoCpus,omitempty"`
	PidsLimit  int64    `json:"PidsLimit,omitempty"`
	Init       *bool    `json:"Init,omitempty"`
	// MaskedPaths 为 nil 时使用 Docker 的默认列表；给出时整体替换默认值。
	MaskedPaths []string   `json:"MaskedPaths,omitempty"`
	LogConfig   *LogConfig `json:"LogConfig,omitempty"`
}

// DeviceMapping 把宿主的一个设备节点放进容器。
type DeviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

// LogConfig 是容器的日志驱动设置。
type LogConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config,omitempty"`
}

// NetworkingConfig 指定容器创建时接入的网络端点。
type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointSettings `json:"EndpointsConfig"`
}

// EndpointSettings 是容器在一个网络上的端点设置与状态。
type EndpointSettings struct {
	IPAMConfig  *EndpointIPAM `json:"IPAMConfig,omitempty"`
	IPAddress   string        `json:"IPAddress,omitempty"`
	IPPrefixLen int           `json:"IPPrefixLen,omitempty"`
	Gateway     string        `json:"Gateway,omitempty"`
	MacAddress  string        `json:"MacAddress,omitempty"`
}

// EndpointIPAM 指定端点的静态地址。
type EndpointIPAM struct {
	IPv4Address string `json:"IPv4Address,omitempty"`
}

// Container 是 inspect 结果里本项目关心的部分。
type Container struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status    string `json:"Status"`
		Running   bool   `json:"Running"`
		ExitCode  int    `json:"ExitCode"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	Config struct {
		Image    string            `json:"Image"`
		Hostname string            `json:"Hostname"`
		Labels   map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]EndpointSettings `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Bool 返回指向 v 的指针，用于可选的布尔字段。
func Bool(v bool) *bool { return &v }

// ContainerCreate 创建容器并返回它的 ID。
func (c *Client) ContainerCreate(ctx context.Context, name string, spec ContainerSpec) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	if err := c.do(ctx, http.MethodPost, "/containers/create", q, spec, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// ContainerStart 启动容器；已在运行时不报错。
func (c *Client) ContainerStart(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, nil)
}

// ContainerStop 停止容器，等待 timeout 后强杀；已停止或不存在时不报错。
func (c *Client) ContainerStop(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}
	err := c.do(ctx, http.MethodPost, "/containers/"+id+"/stop", q, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ContainerKill 向容器的主进程发信号。
func (c *Client) ContainerKill(ctx context.Context, id, signal string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+id+"/kill", url.Values{"signal": {signal}}, nil, nil)
}

// ContainerRemove 强制删除容器及其匿名卷；不存在时不报错。
func (c *Client) ContainerRemove(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/containers/"+id, url.Values{"force": {"1"}, "v": {"1"}}, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ContainerInspect 读取容器状态；不存在时返回的错误满足 IsNotFound。
func (c *Client) ContainerInspect(ctx context.Context, id string) (Container, error) {
	var out Container
	err := c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &out)
	out.Name = strings.TrimPrefix(out.Name, "/")
	return out, err
}

// ContainerWait 等到容器不再运行，返回它的退出码。
func (c *Client) ContainerWait(ctx context.Context, id string) (int, error) {
	var out struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/"+id+"/wait", url.Values{"condition": {"not-running"}}, nil, &out); err != nil {
		return 0, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return out.StatusCode, fmt.Errorf("docker: %s", out.Error.Message)
	}
	return out.StatusCode, nil
}

// ContainerLogs 读取容器至今的标准输出与标准错误。
func (c *Client) ContainerLogs(ctx context.Context, id string) (stdout, stderr string, err error) {
	resp, err := c.request(ctx, http.MethodGet, "/containers/"+id+"/logs", url.Values{"stdout": {"1"}, "stderr": {"1"}}, "", nil)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var o, e bytes.Buffer
	err = demux(resp.Body, &o, &e)
	return o.String(), e.String(), err
}

// ContainerSummary 是列表结果里的一项。
type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// ContainerList 列出带有全部给定标签的容器，包括已停止的。
func (c *Client) ContainerList(ctx context.Context, labels map[string]string) ([]ContainerSummary, error) {
	var sel []string
	for k, v := range labels {
		sel = append(sel, k+"="+v)
	}
	f, err := json.Marshal(map[string][]string{"label": sel})
	if err != nil {
		return nil, err
	}
	var out []ContainerSummary
	err = c.do(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"1"}, "filters": {string(f)}}, nil, &out)
	return out, err
}

// RunResult 是一次性容器或 exec 的执行结果。
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// RunOnce 创建并运行一个一次性容器，等它结束后收集输出并删除。
func (c *Client) RunOnce(ctx context.Context, spec ContainerSpec) (RunResult, error) {
	id, err := c.ContainerCreate(ctx, "", spec)
	if err != nil {
		return RunResult{}, err
	}
	// 清理不随调用方的取消而中断，避免留下残余容器。
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		_ = c.ContainerRemove(cctx, id)
	}()
	if err := c.ContainerStart(ctx, id); err != nil {
		return RunResult{}, err
	}
	code, err := c.ContainerWait(ctx, id)
	if err != nil {
		return RunResult{}, err
	}
	stdout, stderr, err := c.ContainerLogs(ctx, id)
	return RunResult{ExitCode: code, Stdout: stdout, Stderr: stderr}, err
}
