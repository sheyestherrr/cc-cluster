// Package identity 生成槽位的稳定身份：machine-id、用户名、hostname、MAC 与机型信息。
//
// 身份只在槽位首次创建时生成一次并持久化，之后重建容器、重新接线都沿用。
//
// 网卡厂商前缀与机型目录是内置的一份常见设备清单；机型只保留普通用户在 sysfs 里读得到的字段。
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/model"
)

// 真实网卡厂商的地址前缀（单播、全球管理）。本地管理地址（02: 开头一类）在真实机器的
// 有线和无线网卡上基本见不到，反而是虚拟网卡的特征。
var macOUIs = [][3]byte{
	{0x3c, 0x2c, 0x30}, // Dell
	{0xe8, 0x6a, 0x64}, // Lenovo
	{0xb0, 0x5c, 0xda}, // HP
	{0x2c, 0xfd, 0xa1}, // ASUSTeK
	{0x3c, 0x97, 0x0e}, // Intel
	{0x00, 0xe0, 0x4c}, // Realtek
	{0x9c, 0xbf, 0x0d}, // Framework
	{0x00, 0x1b, 0x21}, // Intel
}

// DefaultUser 是没有指定用户名时 worker 内的用户名。
const DefaultUser = "user"

// 不能用作槽位用户名的系统账号。
var reservedUsers = map[string]bool{
	"root": true, "nobody": true, "daemon": true, "bin": true, "sys": true, "sync": true,
	"games": true, "man": true, "lp": true, "mail": true, "news": true, "uucp": true,
	"proxy": true, "www-data": true, "backup": true, "list": true, "irc": true, "_apt": true,
	"systemd-network": true, "messagebus": true, "sshd": true,
}

// machine 是一种机型在 DMI 里的固定字段。
type machine struct {
	vendor, product, version, family, sku string
	boardVendor, boardName, boardVersion  string
	chassisVendor, chassisType            string
	biosVendor, biosVersion, biosDate     string
}

var machines = []machine{
	{"Dell Inc.", "OptiPlex 7090", "1.0", "OptiPlex", "0A5E", "Dell Inc.", "0K3XYZ", "A01", "Dell Inc.", "3", "Dell Inc.", "2.18.0", "07/12/2023"},
	{"Dell Inc.", "Latitude 5440", "1.0", "Latitude", "0B2C", "Dell Inc.", "0M3T2Y", "A00", "Dell Inc.", "10", "Dell Inc.", "1.14.2", "03/08/2024"},
	{"Dell Inc.", "XPS 15 9530", "1.0", "XPS", "0C7D", "Dell Inc.", "0R6P4H", "A00", "Dell Inc.", "10", "Dell Inc.", "1.19.0", "02/14/2024"},
	{"Dell Inc.", "Precision 3560", "1.0", "Precision", "0D8E", "Dell Inc.", "0J4F2V", "A00", "Dell Inc.", "10", "Dell Inc.", "1.15.0", "08/09/2023"},
	{"Dell Inc.", "Inspiron 15 3520", "1.0", "Inspiron", "0E2F", "Dell Inc.", "0G8T4R", "A00", "Dell Inc.", "10", "Dell Inc.", "1.9.0", "11/14/2023"},
	{"LENOVO", "ThinkPad T14 Gen 3", "ThinkPad T14 Gen 3", "ThinkPad T14 Gen 3", "21AH", "LENOVO", "21AHCTO1WW", "SDK0T76461 WIN", "LENOVO", "10", "LENOVO", "N3MET17W (1.17 )", "01/18/2024"},
	{"LENOVO", "ThinkPad X1 Carbon Gen 11", "ThinkPad X1 Carbon Gen 11", "ThinkPad X1 Carbon Gen 11", "21HM", "LENOVO", "21HMCTO1WW", "SDK0T76463 WIN", "LENOVO", "10", "LENOVO", "N3XET54W (1.35 )", "12/11/2023"},
	{"HP", "HP EliteBook 840 G8", "1.0", "103C_5336AN HP EliteBook", "4A5B", "HP", "8846", "KBC Version 12.01.00", "HP", "10", "HP", "T76 Ver. 01.14.00", "09/22/2023"},
	{"HP", "HP ProBook 450 G9", "1.0", "103C_5336AN HP ProBook", "6C8D", "HP", "89D5", "KBC Version 11.02.00", "HP", "10", "HP", "U89 Ver. 01.11.00", "05/30/2023"},
	{"ASUSTeK COMPUTER INC.", "ExpertBook B1502CBA", "1.0", "ExpertBook", "0E9F", "ASUSTeK COMPUTER INC.", "B1502CBA", "1.0", "ASUSTeK COMPUTER INC.", "10", "American Megatrends Inc.", "310", "04/21/2023"},
	{"Acer", "TravelMate P215-54", "V1.0", "TravelMate", "1F2A", "Acer", "TravelMate P215-54", "V1.0", "Acer", "10", "Insyde Corp.", "V1.18", "06/09/2023"},
	{"Framework", "Laptop 13", "1.0", "Laptop", "0F13", "Framework", "FRANBMCP06", "A6", "Framework", "10", "INSYDE Corp.", "03.03", "10/05/2023"},
	{"Micro-Star International Co. Ltd.", "Modern 15 A11MU", "1.0", "Modern", "2A3B", "Micro-Star International Co. Ltd.", "MS-1552", "1.0", "Micro-Star International Co. Ltd.", "10", "American Megatrends International, LLC.", "E1552IMS.112", "02/24/2022"},
	{"GIGABYTE", "AERO 16 YE5", "1.0", "AERO", "3B4C", "GIGABYTE", "AERO 16 YE5", "1.0", "GIGABYTE", "10", "American Megatrends International, LLC.", "FB08", "08/17/2022"},
	{"LENOVO", "ThinkCentre M70q Gen 3", "ThinkCentre M70q Gen 3", "ThinkCentre M70q", "11T3", "LENOVO", "3150", "SDK0J40697 WIN", "LENOVO", "3", "LENOVO", "M4VKT1EA", "06/16/2023"},
	{"HP", "HP EliteDesk 800 G6", "1.0", "103C_53307F HP EliteDesk", "4D6E", "HP", "87C1", "KBC Version 12.01.00", "HP", "3", "HP", "S53 Ver. 02.14.01", "01/25/2023"},
	{"ASUSTeK COMPUTER INC.", "VivoBook 15 X1502ZA", "1.0", "VivoBook", "5E7F", "ASUSTeK COMPUTER INC.", "X1502ZA", "1.0", "ASUSTeK COMPUTER INC.", "10", "American Megatrends Inc.", "308", "05/06/2022"},
	{"Acer", "Veriton N4670G", "V1.0", "Veriton", "6F8A", "Acer", "Veriton N4670G", "V1.0", "Acer", "3", "Insyde Corp.", "V1.12", "03/02/2023"},
}

// dmi 把机型展开成 sysfs 的 DMI 字段。序列号与产品 UUID 在真实机器上只有 root 可读，
// 普通用户读到的是"权限不足"，所以这里不生成它们。
func (m machine) dmi() map[string]string {
	return map[string]string{
		"sys_vendor": m.vendor, "product_name": m.product, "product_version": m.version,
		"product_family": m.family, "product_sku": m.sku,
		"board_vendor": m.boardVendor, "board_name": m.boardName, "board_version": m.boardVersion,
		"board_asset_tag": "No Asset Tag",
		"chassis_vendor":  m.chassisVendor, "chassis_type": m.chassisType, "chassis_asset_tag": "No Asset Tag",
		"bios_vendor": m.biosVendor, "bios_version": m.biosVersion, "bios_date": m.biosDate,
	}
}

var (
	hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	userRe     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)
	slugDrop   = regexp.MustCompile(`[^A-Za-z0-9-]+`)
)

// Options 是生成身份时可以指定的部分；用户名留空用 DefaultUser，hostname 留空按“用户名-机型”生成。
type Options struct {
	Hostname string
	User     string
}

// New 用系统随机源生成一份身份。
func New(opt Options) (model.Identity, error) {
	return generate(rand.Reader, opt, time.Now().UTC())
}

func generate(r io.Reader, opt Options, now time.Time) (model.Identity, error) {
	var none model.Identity
	// 一次取够：machine-id 16 字节、MAC 4 字节（前缀下标 + 后三段）、机型 1 字节（第 21 个字节不用）。
	var b [22]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return none, err
	}
	oui := macOUIs[int(b[16])%len(macOUIs)]
	m := machines[int(b[21])%len(machines)]

	user := opt.User
	if user == "" {
		user = DefaultUser
	}
	if err := ValidateUser(user); err != nil {
		return none, err
	}

	hostname := opt.Hostname
	if hostname == "" {
		// 桌面发行版安装时的默认主机名就是"用户名-机型"。
		hostname = user + "-" + strings.Trim(slugDrop.ReplaceAllString(strings.ReplaceAll(m.product, " ", "-"), ""), "-")
		if len(hostname) > 63 {
			hostname = strings.TrimRight(hostname[:63], "-")
		}
	}
	if err := ValidateHostname(hostname); err != nil {
		return none, err
	}

	return model.Identity{
		MachineID: hex.EncodeToString(b[:16]),
		Hostname:  hostname,
		MAC:       fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", oui[0], oui[1], oui[2], b[17], b[18], b[19]),
		User:      user,
		DMI:       m.dmi(),
		CreatedAt: now,
	}, nil
}

// ValidateHostname 检查 hostname 是否为合法的单段主机名。
func ValidateHostname(h string) error {
	if !hostnameRe.MatchString(h) {
		return fmt.Errorf("hostname %q 不合法：只能用字母、数字和连字符，最长 63 个字符", h)
	}
	return nil
}

// ValidateUser 检查 worker 内的用户名。
func ValidateUser(u string) error {
	if !userRe.MatchString(u) {
		return fmt.Errorf("用户名 %q 不合法：小写字母开头，只能用小写字母、数字、下划线和连字符，最长 31 个字符", u)
	}
	if reservedUsers[u] {
		return fmt.Errorf("用户名 %q 是系统账号", u)
	}
	return nil
}
