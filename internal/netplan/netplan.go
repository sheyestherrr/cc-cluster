// Package netplan 负责槽位在网关地址池里的编址，以及由序号派生的各种名字。
//
// 每个槽占地址池里的一个 /30：第一个可用地址给网关一端，第二个给 worker。
// 序号是该 /30 在地址池里的位置，网关侧网卡名、策略路由优先级和回环探测端口都由它确定，
// 因此同一个槽每次接线得到的名字都相同。
package netplan

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

const (
	// TUNDevice 是核心创建的 TUN 网卡名。
	TUNDevice = "cc0"
	// RouteTable 是只含"默认路由指向 TUN"的路由表。
	RouteTable = 100
	// WorkerIfName 是 worker 内的网卡名。
	WorkerIfName = "eth0"

	rulePrefBase  = 1000
	probePortBase = 17000
)

// Link 是一个槽位的编址结果。
type Link struct {
	Index     int
	Subnet    netip.Prefix
	GatewayIP netip.Addr
	WorkerIP  netip.Addr
}

// GatewayIf 返回网关侧的网卡名。
func (l Link) GatewayIf() string { return fmt.Sprintf("cs%d", l.Index) }

// RulePref 返回"该槽的包进 TUN"这条策略路由的优先级；紧随其后的 RulePref()+1 是不可达规则。
func (l Link) RulePref() int { return rulePrefBase + 2*l.Index }

// ProbePort 返回该槽所用链路在网关回环上的探测端口。
func (l Link) ProbePort() int { return probePortBase + l.Index }

// Capacity 返回地址池能容纳的槽位数。
func Capacity(pool netip.Prefix) int {
	return 1 << (30 - pool.Bits())
}

// At 返回地址池里第 idx 个 /30 的编址。
func At(pool netip.Prefix, idx int) (Link, error) {
	if !pool.Addr().Is4() || pool.Bits() > 30 {
		return Link{}, fmt.Errorf("地址池 %s 不是不小于 /30 的 IPv4 网段", pool)
	}
	if idx < 0 || idx >= Capacity(pool) {
		return Link{}, fmt.Errorf("序号 %d 超出地址池 %s 的范围", idx, pool)
	}
	base := binary.BigEndian.Uint32(pool.Masked().Addr().AsSlice())
	return Link{
		Index:     idx,
		Subnet:    netip.PrefixFrom(u32(base+uint32(idx)*4), 30),
		GatewayIP: u32(base + uint32(idx)*4 + 1),
		WorkerIP:  u32(base + uint32(idx)*4 + 2),
	}, nil
}

// Locate 由槽位的 /30 反推它在地址池里的编址。
func Locate(pool, subnet netip.Prefix) (Link, error) {
	if subnet.Bits() != 30 || subnet != subnet.Masked() || !pool.Contains(subnet.Addr()) {
		return Link{}, fmt.Errorf("网段 %s 不是地址池 %s 里的一个 /30", subnet, pool)
	}
	base := binary.BigEndian.Uint32(pool.Masked().Addr().AsSlice())
	off := binary.BigEndian.Uint32(subnet.Addr().AsSlice()) - base
	return At(pool, int(off/4))
}

// ErrPoolFull 表示地址池里没有空闲的 /30。
var ErrPoolFull = errors.New("地址池已满")

// Allocate 返回地址池里第一个未被占用的 /30。used 是该网关上已有槽位的网段。
func Allocate(pool netip.Prefix, used []netip.Prefix) (Link, error) {
	taken := make(map[netip.Prefix]bool, len(used))
	for _, u := range used {
		taken[u] = true
	}
	for i := 0; i < Capacity(pool); i++ {
		l, err := At(pool, i)
		if err != nil {
			return Link{}, err
		}
		if !taken[l.Subnet] {
			return l, nil
		}
	}
	return Link{}, ErrPoolFull
}

func u32(v uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}
