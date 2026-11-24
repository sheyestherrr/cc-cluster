package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ProxyHash 返回节点片段内容的指纹：配置相同（不看名称）就得到相同的值，
// 用来认出订阅之间重复的节点、已经登记过的节点，以及订阅刷新后只是改了名的节点。
// 片段为空或无法序列化时返回空串。
func ProxyHash(p map[string]any) string {
	if len(p) == 0 {
		return ""
	}
	c := make(map[string]any, len(p))
	for k, v := range p {
		if k != "name" {
			c[k] = v
		}
	}
	// encoding/json 输出映射时按键排序；刚从 YAML 解出的整数与从 JSON 读回的浮点数写出来也相同。
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// 地区的来源。
const (
	RegionFromExit = "exit" // 出口评估给出的所在地
	RegionFromText = "name" // 从名称、备注或 ID 里认出来的，是节点提供方的说法
)

// Region 返回节点所在的国家或地区（两位大写代码）和这个结论的来源；不知道时返回两个空串。
// 落地有出口评估时以它为准；其余情况从订阅里的名称、备注和 ID 里识别。
func (n Node) Region() (code, from string) {
	if r := n.ExitReport; r != nil && len(r.CountryCode) == 2 {
		return strings.ToUpper(r.CountryCode), RegionFromExit
	}
	if n.Source != nil {
		if c := RegionFromName(n.Source.Name); c != "" {
			return c, RegionFromText
		}
	}
	if c := RegionFromName(n.Note); c != "" {
		return c, RegionFromText
	}
	if c := RegionFromID(n.ID); c != "" {
		return c, RegionFromText
	}
	return "", ""
}

// RegionFromName 从节点名称里识别它所指的国家或地区，返回两位大写代码（ISO 3166-1），认不出时返回空串。
// 依次看国旗 emoji、中英文的国家与城市名、单独出现的大写代码（HK、JP、USA）；同一类里出现多个时取最靠前的。
// 这只是名称的说法，不保证与实际出口一致。
func RegionFromName(name string) string {
	if c := firstFlag(name); c != "" {
		return c
	}
	if c := firstRegionWord(strings.ToLower(name)); c != "" {
		return c
	}
	return firstRegionToken(name, false)
}

// RegionFromID 对节点 ID 做同样的识别。ID 是小写的，所以容易撞上普通单词的代码（in、id、it、my）不认。
func RegionFromID(id string) string {
	if c := firstRegionWord(strings.ReplaceAll(id, "-", " ")); c != "" {
		return c
	}
	return firstRegionToken(id, true)
}

// firstFlag 找出第一个国旗 emoji（一对区域指示符）并还原成代码。
func firstFlag(s string) string {
	const base = 0x1F1E6 // 区域指示符 A
	var prev rune
	for _, r := range s {
		if r < base || r > base+25 {
			prev = 0
			continue
		}
		if prev != 0 {
			return string([]rune{'A' + prev - base, 'A' + r - base})
		}
		prev = r
	}
	return ""
}

func asciiLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// firstRegionWord 在已转成小写的文本里找最靠前的地名；位置相同时取更长的（“印度尼西亚”先于“印度”）。
// 英文地名的两侧不能紧挨着字母。
func firstRegionWord(lower string) string {
	best, bestAt, bestLen := "", len(lower), 0
	for word, code := range regionWords {
		at := indexWord(lower, word)
		if at < 0 || at > bestAt || at == bestAt && len(word) <= bestLen {
			continue
		}
		best, bestAt, bestLen = code, at, len(word)
	}
	return best
}

func indexWord(s, word string) int {
	if word[0] >= 0x80 { // 中文地名不需要边界
		return strings.Index(s, word)
	}
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return -1
		}
		i += from
		end := i + len(word)
		if (i == 0 || !asciiLetter(s[i-1])) && (end == len(s) || !asciiLetter(s[end])) {
			return i
		}
		from = i + 1
	}
	return -1
}

// firstRegionToken 找第一个单独出现的代码：一段连续的字母，长度 2 或 3，正好是表里的代码。
// 名称里只认全大写的；ID 里（id 为 true）认小写的，但跳过容易撞上普通单词的那几个。
func firstRegionToken(s string, id bool) string {
	for i := 0; i < len(s); {
		if !asciiLetter(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && asciiLetter(s[j]) {
			j++
		}
		tok := s[i:j]
		i = j
		if len(tok) != 2 && len(tok) != 3 {
			continue
		}
		if id {
			if tok != strings.ToLower(tok) || ambiguousInID[tok] {
				continue
			}
			tok = strings.ToUpper(tok)
		} else if tok != strings.ToUpper(tok) {
			continue
		}
		if code, ok := regionTokens[tok]; ok {
			return code
		}
	}
	return ""
}

var ambiguousInID = map[string]bool{"in": true, "id": true, "it": true, "my": true, "ch": true, "se": true, "es": true, "ar": true, "th": true}

// regionTokens 是名称里单独出现时可以认作地区的代码。没有收进来的：CN（会撞上 CN2 线路）、
// NO（NO.1）、AT、BE、IS 这类同时是常见单词的代码，它们只能靠国旗或地名识别。
var regionTokens = map[string]string{
	"HK": "HK", "HKG": "HK", "TW": "TW", "TWN": "TW", "JP": "JP", "JPN": "JP", "SG": "SG", "SGP": "SG",
	"US": "US", "USA": "US", "KR": "KR", "KOR": "KR", "UK": "GB", "GB": "GB", "GBR": "GB",
	"DE": "DE", "DEU": "DE", "GER": "DE", "FR": "FR", "FRA": "FR", "NL": "NL", "NLD": "NL",
	"CA": "CA", "CAN": "CA", "AU": "AU", "AUS": "AU", "RU": "RU", "RUS": "RU", "IN": "IN", "IND": "IN",
	"ID": "ID", "IDN": "ID", "TR": "TR", "TUR": "TR", "BR": "BR", "BRA": "BR", "AR": "AR", "ARG": "AR",
	"MY": "MY", "MYS": "MY", "TH": "TH", "THA": "TH", "VN": "VN", "VNM": "VN", "PH": "PH", "PHL": "PH",
	"IT": "IT", "ITA": "IT", "ES": "ES", "ESP": "ES", "CH": "CH", "CHE": "CH", "SE": "SE", "SWE": "SE",
	"MO": "MO", "MAC": "MO", "AE": "AE", "UAE": "AE", "IE": "IE", "IRL": "IE", "PL": "PL", "POL": "PL",
	"UA": "UA", "UKR": "UA", "IL": "IL", "ISR": "IL", "MX": "MX", "MEX": "MX", "ZA": "ZA", "ZAF": "ZA",
	"NZ": "NZ", "NZL": "NZ", "FI": "FI", "FIN": "FI", "DK": "DK", "DNK": "DK", "PT": "PT", "PRT": "PT",
	"KZ": "KZ", "KAZ": "KZ", "PK": "PK", "PAK": "PK", "NG": "NG", "NGA": "NG", "EG": "EG", "EGY": "EG",
	"CL": "CL", "CHL": "CL", "CO": "CO", "COL": "CO", "RO": "RO", "ROU": "RO", "BG": "BG", "BGR": "BG",
	"HU": "HU", "HUN": "HU", "CZ": "CZ", "CZE": "CZ", "GR": "GR", "GRC": "GR", "LU": "LU", "LUX": "LU",
	"KH": "KH", "KHM": "KH", "SA": "SA", "SAU": "SA", "NOR": "NO", "AUT": "AT", "BEL": "BE", "ISL": "IS",
}

// regionWords 是地名（小写）到代码的对照。没有收大陆的城市和“中国”：它们在名称里多半指入口或运营商
// （“上海 → 日本”“中国移动优化”），不是出口。
var regionWords = func() map[string]string {
	groups := map[string][]string{
		"HK": {"香港", "hong kong", "hongkong"},
		"TW": {"台湾", "台灣", "臺灣", "台北", "新北", "彰化", "taiwan", "taipei"},
		"JP": {"日本", "东京", "東京", "大阪", "埼玉", "japan", "tokyo", "osaka"},
		"SG": {"新加坡", "狮城", "singapore"},
		"US": {"美国", "美國", "洛杉矶", "圣何塞", "硅谷", "西雅图", "纽约", "芝加哥", "达拉斯", "凤凰城", "迈阿密", "亚特兰大",
			"旧金山", "波特兰", "阿什本", "弗吉尼亚", "俄勒冈", "united states", "los angeles", "san jose", "silicon valley",
			"seattle", "new york", "chicago", "dallas", "phoenix", "miami", "atlanta", "san francisco", "ashburn", "fremont"},
		"KR": {"韩国", "韓國", "首尔", "春川", "korea", "seoul"},
		"GB": {"英国", "英國", "伦敦", "united kingdom", "britain", "england", "london"},
		"DE": {"德国", "德國", "法兰克福", "germany", "frankfurt"},
		"FR": {"法国", "法國", "巴黎", "france", "paris"},
		"NL": {"荷兰", "阿姆斯特丹", "netherlands", "amsterdam"},
		"CA": {"加拿大", "多伦多", "温哥华", "蒙特利尔", "canada", "toronto", "vancouver", "montreal"},
		"AU": {"澳大利亚", "澳洲", "悉尼", "墨尔本", "australia", "sydney", "melbourne"},
		"RU": {"俄罗斯", "俄羅斯", "莫斯科", "圣彼得堡", "伯力", "russia", "moscow"},
		"IN": {"印度", "孟买", "india", "mumbai"},
		"ID": {"印度尼西亚", "印尼", "雅加达", "indonesia", "jakarta"},
		"TR": {"土耳其", "伊斯坦布尔", "turkey", "türkiye", "istanbul"},
		"BR": {"巴西", "圣保罗", "brazil", "sao paulo"},
		"AR": {"阿根廷", "argentina"},
		"MY": {"马来西亚", "馬來西亞", "吉隆坡", "malaysia", "kuala lumpur"},
		"TH": {"泰国", "泰國", "曼谷", "thailand", "bangkok"},
		"VN": {"越南", "胡志明", "河内", "vietnam"},
		"PH": {"菲律宾", "菲律賓", "马尼拉", "philippines", "manila"},
		"IT": {"意大利", "米兰", "italy", "milan"},
		"ES": {"西班牙", "马德里", "spain", "madrid"},
		"CH": {"瑞士", "苏黎世", "switzerland", "zurich"},
		"SE": {"瑞典", "斯德哥尔摩", "sweden", "stockholm"},
		"MO": {"澳门", "澳門", "macau", "macao"},
		"AE": {"阿联酋", "迪拜", "dubai"},
		"IE": {"爱尔兰", "都柏林", "ireland", "dublin"},
		"PL": {"波兰", "华沙", "poland", "warsaw"},
		"UA": {"乌克兰", "ukraine"},
		"IL": {"以色列", "israel"},
		"MX": {"墨西哥", "mexico"},
		"ZA": {"南非", "south africa"},
		"NZ": {"新西兰", "new zealand"},
		"FI": {"芬兰", "finland"},
		"NO": {"挪威", "norway"},
		"DK": {"丹麦", "denmark"},
		"AT": {"奥地利", "维也纳", "austria", "vienna"},
		"BE": {"比利时", "belgium"},
		"PT": {"葡萄牙", "portugal"},
		"GR": {"希腊", "greece"},
		"CZ": {"捷克", "czech"},
		"HU": {"匈牙利", "hungary"},
		"RO": {"罗马尼亚", "romania"},
		"BG": {"保加利亚", "bulgaria"},
		"LU": {"卢森堡", "luxembourg"},
		"IS": {"冰岛", "iceland"},
		"KZ": {"哈萨克斯坦", "kazakhstan"},
		"PK": {"巴基斯坦", "pakistan"},
		"NG": {"尼日利亚", "nigeria"},
		"EG": {"埃及", "egypt"},
		"CL": {"智利", "chile"},
		"CO": {"哥伦比亚", "colombia"},
		"PE": {"秘鲁", "peru"},
		"KH": {"柬埔寨", "cambodia"},
		"MM": {"缅甸", "myanmar"},
		"LA": {"老挝"},
		"MN": {"蒙古", "mongolia"},
		"NP": {"尼泊尔", "nepal"},
		"BD": {"孟加拉", "bangladesh"},
		"SA": {"沙特", "saudi"},
		"QA": {"卡塔尔", "qatar"},
		"CN": {"回国"},
	}
	out := map[string]string{}
	for code, words := range groups {
		for _, w := range words {
			out[w] = code
		}
	}
	// 专线常见的缩写：入口城市加出口地区，例如“沪日”“深港”。
	exits := map[string]string{"港": "HK", "日": "JP", "美": "US", "新": "SG", "韩": "KR", "台": "TW", "德": "DE", "英": "GB", "俄": "RU"}
	for _, entry := range []string{"沪", "深", "广", "京", "杭"} {
		for suffix, code := range exits {
			out[entry+suffix] = code
		}
	}
	return out
}()
