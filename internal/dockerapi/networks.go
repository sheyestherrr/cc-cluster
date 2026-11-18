package dockerapi

import (
	"context"
	"net/http"
)

// Network 是 inspect 结果里本项目关心的部分。
type Network struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	IPAM     struct {
		Config []IPAMConfig `json:"Config"`
	} `json:"IPAM"`
}

// IPAMConfig 是网络的一个地址段。
type IPAMConfig struct {
	Subnet  string `json:"Subnet,omitempty"`
	Gateway string `json:"Gateway,omitempty"`
}

// NetworkSpec 是创建网络的参数。
type NetworkSpec struct {
	Name     string
	Internal bool
	Subnet   string // 为空时由 Docker 分配
	Labels   map[string]string
	Options  map[string]string
}

// NetworkInspect 读取网络；不存在时返回的错误满足 IsNotFound。
func (c *Client) NetworkInspect(ctx context.Context, name string) (Network, error) {
	var out Network
	err := c.do(ctx, http.MethodGet, "/networks/"+name, nil, nil, &out)
	return out, err
}

// NetworkCreate 创建一个 bridge 网络。
func (c *Client) NetworkCreate(ctx context.Context, spec NetworkSpec) error {
	body := map[string]any{
		"Name":     spec.Name,
		"Driver":   "bridge",
		"Internal": spec.Internal,
		"Labels":   spec.Labels,
		"Options":  spec.Options,
	}
	if spec.Subnet != "" {
		body["IPAM"] = map[string]any{"Config": []IPAMConfig{{Subnet: spec.Subnet}}}
	}
	return c.do(ctx, http.MethodPost, "/networks/create", nil, body, nil)
}

// NetworkRemove 删除网络；不存在时不报错。
func (c *Client) NetworkRemove(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/networks/"+name, nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// NetworkConnect 把容器接入网络；ipv4 非空时使用该静态地址。
func (c *Client) NetworkConnect(ctx context.Context, network, container, ipv4 string) error {
	body := map[string]any{"Container": container}
	if ipv4 != "" {
		body["EndpointConfig"] = EndpointSettings{IPAMConfig: &EndpointIPAM{IPv4Address: ipv4}}
	}
	return c.do(ctx, http.MethodPost, "/networks/"+network+"/connect", nil, body, nil)
}
