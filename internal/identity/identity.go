// Package identity 生成槽位的稳定身份：machine-id、hostname 与 MAC。
//
// 身份只在槽位首次创建时生成一次并持久化，之后重建容器、重新接线都沿用。
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 生成 hostname 用的词表：常见的工作站命名，不带项目特征。
var hostWords = []string{"dev", "ws", "work", "build", "lab", "node", "box", "desk"}

const hostAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// New 用系统随机源生成一份身份。hostname 非空时使用给定值。
func New(hostname string) (model.Identity, error) {
	return generate(rand.Reader, hostname, time.Now().UTC())
}

func generate(r io.Reader, hostname string, now time.Time) (model.Identity, error) {
	var mid [16]byte
	if _, err := io.ReadFull(r, mid[:]); err != nil {
		return model.Identity{}, err
	}
	var mac [6]byte
	if _, err := io.ReadFull(r, mac[:]); err != nil {
		return model.Identity{}, err
	}
	// 本地管理位置 1、组播位置 0：不会与真实厂商地址冲突，也是合法的单播地址。
	mac[0] = mac[0]&^0x01 | 0x02

	if hostname == "" {
		var b [7]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return model.Identity{}, err
		}
		suffix := make([]byte, 6)
		for i := range suffix {
			suffix[i] = hostAlphabet[int(b[i+1])%len(hostAlphabet)]
		}
		hostname = hostWords[int(b[0])%len(hostWords)] + "-" + string(suffix)
	} else if err := ValidateHostname(hostname); err != nil {
		return model.Identity{}, err
	}

	return model.Identity{
		MachineID: hex.EncodeToString(mid[:]),
		Hostname:  hostname,
		MAC: fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
			mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]),
		CreatedAt: now,
	}, nil
}

// ValidateHostname 检查 hostname 是否为合法的单段主机名。
func ValidateHostname(h string) error {
	if !hostnameRe.MatchString(h) {
		return fmt.Errorf("hostname %q 不合法：只能用小写字母、数字和连字符，最长 63 个字符", h)
	}
	return nil
}
