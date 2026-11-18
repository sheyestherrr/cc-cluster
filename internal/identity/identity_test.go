package identity

import (
	"bytes"
	"net"
	"regexp"
	"testing"
	"time"
)

func TestNewFormats(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id, err := New("")
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id.MachineID) {
			t.Fatalf("machine-id 格式不对: %q", id.MachineID)
		}
		if err := ValidateHostname(id.Hostname); err != nil {
			t.Fatal(err)
		}
		hw, err := net.ParseMAC(id.MAC)
		if err != nil {
			t.Fatalf("MAC 无法解析: %q", id.MAC)
		}
		if hw[0]&0x01 != 0 || hw[0]&0x02 == 0 {
			t.Fatalf("MAC 必须是本地管理的单播地址: %s", id.MAC)
		}
		if seen[id.MachineID] || seen[id.MAC] {
			t.Fatal("两次生成得到了相同的身份")
		}
		seen[id.MachineID], seen[id.MAC] = true, true
	}
}

func TestGenerateDeterministicWithFixedSource(t *testing.T) {
	src := bytes.Repeat([]byte{0xff}, 64)
	now := time.Unix(0, 0).UTC()
	a, err := generate(bytes.NewReader(src), "", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.MAC != "fe:ff:ff:ff:ff:ff" {
		t.Fatalf("组播位应被清除、本地位应保留，得到 %s", a.MAC)
	}
	b, _ := generate(bytes.NewReader(src), "", now)
	if a != b {
		t.Fatal("相同随机源应得到相同结果")
	}
}

func TestHostnameOverride(t *testing.T) {
	id, err := New("alice-laptop")
	if err != nil || id.Hostname != "alice-laptop" {
		t.Fatalf("应使用给定 hostname: %+v %v", id, err)
	}
	for _, bad := range []string{"Upper", "has.dot", "-lead", "trail-", "a b"} {
		if _, err := New(bad); err == nil {
			t.Errorf("hostname %q 应被拒绝", bad)
		}
	}
}

func TestShortRandomSource(t *testing.T) {
	if _, err := generate(bytes.NewReader([]byte{1, 2, 3}), "", time.Now()); err == nil {
		t.Fatal("随机源不足时应报错")
	}
}
