package identity

import (
	"bytes"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewFormats(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id.MachineID) {
			t.Fatalf("machine-id 格式不对: %q", id.MachineID)
		}
		if err := ValidateHostname(id.Hostname); err != nil {
			t.Fatal(err)
		}
		if err := ValidateUser(id.User); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id.Hostname, id.User+"-") {
			t.Fatalf("默认 hostname 应以用户名开头: %s / %s", id.Hostname, id.User)
		}
		hw, err := net.ParseMAC(id.MAC)
		if err != nil {
			t.Fatalf("MAC 无法解析: %q", id.MAC)
		}
		if hw[0]&0x03 != 0 {
			t.Fatalf("MAC 应是真实厂商前缀（单播、全球管理）: %s", id.MAC)
		}
		for _, k := range []string{"sys_vendor", "product_name", "bios_vendor", "chassis_type"} {
			if id.DMI[k] == "" {
				t.Fatalf("机型信息缺少 %s", k)
			}
		}
		for _, k := range []string{"product_serial", "product_uuid", "board_serial", "chassis_serial"} {
			if _, ok := id.DMI[k]; ok {
				t.Fatalf("%s 在真实机器上只有 root 可读，不应生成", k)
			}
		}
		if seen[id.MachineID] || seen[id.MAC] {
			t.Fatal("两次生成得到了相同的身份")
		}
		seen[id.MachineID], seen[id.MAC] = true, true
	}
}

func TestGenerateDeterministicWithFixedSource(t *testing.T) {
	src := bytes.Repeat([]byte{0x00}, 64)
	now := time.Unix(0, 0).UTC()
	a, err := generate(bytes.NewReader(src), Options{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if a.MAC != "3c:2c:30:00:00:00" || a.User != "user" || a.Hostname != "user-OptiPlex-7090" {
		t.Fatalf("固定随机源的结果不对: %+v", a)
	}
	b, _ := generate(bytes.NewReader(src), Options{}, now)
	if a.MachineID != b.MachineID || a.MAC != b.MAC || a.Hostname != b.Hostname {
		t.Fatal("相同随机源应得到相同结果")
	}
}

func TestOverrides(t *testing.T) {
	id, err := New(Options{Hostname: "alice-laptop", User: "alice"})
	if err != nil || id.Hostname != "alice-laptop" || id.User != "alice" {
		t.Fatalf("应使用给定的 hostname 与用户名: %+v %v", id, err)
	}
	for _, bad := range []string{"has.dot", "-lead", "trail-", "a b"} {
		if _, err := New(Options{Hostname: bad}); err == nil {
			t.Errorf("hostname %q 应被拒绝", bad)
		}
	}
	for _, bad := range []string{"Root", "root", "1abc", "a b", "daemon", ""} {
		if err := ValidateUser(bad); err == nil {
			t.Errorf("用户名 %q 应被拒绝", bad)
		}
	}
}

func TestShortRandomSource(t *testing.T) {
	if _, err := generate(bytes.NewReader([]byte{1, 2, 3}), Options{}, time.Now()); err == nil {
		t.Fatal("随机源不足时应报错")
	}
}
