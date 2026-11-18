package netplan

import (
	"errors"
	"net/netip"
	"testing"
)

func TestAtMatchesProbeLayout(t *testing.T) {
	// 序号为 2 的槽位用的就是这一组地址。
	pool := netip.MustParsePrefix("10.213.250.0/24")
	l, err := At(pool, 2)
	if err != nil {
		t.Fatal(err)
	}
	if l.Subnet.String() != "10.213.250.8/30" || l.GatewayIP.String() != "10.213.250.9" || l.WorkerIP.String() != "10.213.250.10" {
		t.Fatalf("编址不对: %+v", l)
	}
	if l.GatewayIf() != "cs2" || l.RulePref() != 1004 || l.ProbePort() != 17002 {
		t.Fatalf("派生名字不对: %s %d %d", l.GatewayIf(), l.RulePref(), l.ProbePort())
	}
}

func TestLocateRoundTrip(t *testing.T) {
	pool := netip.MustParsePrefix("10.213.250.0/24")
	for i := 0; i < Capacity(pool); i++ {
		l, err := At(pool, i)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Locate(pool, l.Subnet)
		if err != nil || back != l {
			t.Fatalf("序号 %d 往返不一致: %+v %v", i, back, err)
		}
		if len(l.GatewayIf()) > 15 {
			t.Fatalf("网卡名超过内核上限: %s", l.GatewayIf())
		}
	}
	for _, bad := range []string{"10.213.251.0/30", "10.213.250.0/29", "10.213.250.2/30"} {
		p, _ := netip.ParsePrefix(bad)
		if _, err := Locate(pool, p); err == nil {
			t.Errorf("%s 不属于地址池，应报错", bad)
		}
	}
}

func TestAllocateSkipsUsedAndReportsFull(t *testing.T) {
	pool := netip.MustParsePrefix("10.9.0.0/29") // 只能放两个槽
	a, err := Allocate(pool, nil)
	if err != nil || a.Index != 0 {
		t.Fatalf("第一个应是序号 0: %+v %v", a, err)
	}
	b, err := Allocate(pool, []netip.Prefix{a.Subnet})
	if err != nil || b.Index != 1 {
		t.Fatalf("第二个应是序号 1: %+v %v", b, err)
	}
	if _, err := Allocate(pool, []netip.Prefix{a.Subnet, b.Subnet}); !errors.Is(err, ErrPoolFull) {
		t.Fatalf("应报地址池已满: %v", err)
	}
	// 释放中间的槽后应被复用。
	c, err := Allocate(pool, []netip.Prefix{b.Subnet})
	if err != nil || c.Index != 0 {
		t.Fatalf("应复用空出来的序号 0: %+v %v", c, err)
	}
}

func TestSubnetsDoNotOverlap(t *testing.T) {
	pool := netip.MustParsePrefix("10.213.250.0/26")
	var prev Link
	for i := 0; i < Capacity(pool); i++ {
		l, _ := At(pool, i)
		if i > 0 && (prev.Subnet.Overlaps(l.Subnet) || l.Subnet.Contains(prev.WorkerIP)) {
			t.Fatalf("相邻槽位网段重叠: %s %s", prev.Subnet, l.Subnet)
		}
		if !l.Subnet.Contains(l.GatewayIP) || !l.Subnet.Contains(l.WorkerIP) {
			t.Fatalf("地址不在自己的网段里: %+v", l)
		}
		prev = l
	}
	if _, err := At(pool, Capacity(pool)); err == nil {
		t.Fatal("越界序号应报错")
	}
}
