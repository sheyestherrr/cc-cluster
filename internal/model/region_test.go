package model

import "testing"

func TestRegionFromName(t *testing.T) {
	cases := map[string]string{
		"🇭🇰 香港 01":           "HK",
		"🇯🇵日本 | 解锁美国流媒体":     "JP",
		"香港 HKT 01":          "HK",
		"上海 → 日本 IEPL":       "JP",
		"沪日专线 02":            "JP",
		"深港 IEPL":            "HK",
		"印度尼西亚 雅加达":          "ID",
		"印度 孟买":              "IN",
		"新西兰 01":             "NZ",
		"新加坡 01":             "SG",
		"Hong Kong 03":       "HK",
		"Los Angeles-02":     "US",
		"US-CA 01":           "US",
		"HK01":               "HK",
		"JP-Tokyo":           "JP",
		"UK London":          "GB",
		"USA Seattle":        "US",
		"CN2 GIA 香港":         "HK",
		"CN2 GIA":            "",
		"NO.1 Premium":       "",
		"中国移动优化 台湾":          "TW",
		"剩余流量：12 GB":         "GB", // 提示项会被认成英国；调用方用 IsInfoName 先排除
		"Indiana Jones":      "",
		"hk lowercase":       "",
		"Premium 01":         "",
		"":                   "",
		"韩国 首尔 KT":           "KR",
		"Türkiye Istanbul":   "TR",
		"节点 in use":          "",
		"United States | 01": "US",
	}
	for name, want := range cases {
		if got := RegionFromName(name); got != want {
			t.Errorf("RegionFromName(%q) = %q，期望 %q", name, got, want)
		}
	}
}

func TestRegionFromID(t *testing.T) {
	cases := map[string]string{
		"kr-home-lg": "KR",
		"hk01":       "HK",
		"us-west-2":  "US",
		"tokyo-1":    "JP",
		"my-node":    "",
		"landing-in": "",
		"lg-home":    "",
		"hong-kong":  "HK",
	}
	for id, want := range cases {
		if got := RegionFromID(id); got != want {
			t.Errorf("RegionFromID(%q) = %q，期望 %q", id, got, want)
		}
	}
}

func TestNodeRegion(t *testing.T) {
	n := Node{ID: "jp-01", Source: &NodeSource{Sub: "s", Name: "🇺🇸 美国 01"}}
	if code, from := n.Region(); code != "US" || from != RegionFromText {
		t.Errorf("取自订阅的节点：%q %q", code, from)
	}
	n.ExitReport = &ExitReport{CountryCode: "de"}
	if code, from := n.Region(); code != "DE" || from != RegionFromExit {
		t.Errorf("有出口评估时：%q %q", code, from)
	}
	if code, from := (Node{ID: "landing-a"}).Region(); code != "" || from != "" {
		t.Errorf("认不出时应为空：%q %q", code, from)
	}
	if code, _ := (Node{ID: "landing-a", Note: "家宽 新加坡"}).Region(); code != "SG" {
		t.Errorf("备注：%q", code)
	}
}

func TestProxyHash(t *testing.T) {
	a := map[string]any{"type": "ss", "server": "a.example", "port": 443, "password": "x"}
	b := map[string]any{"password": "x", "port": float64(443), "server": "a.example", "type": "ss", "name": "别的名字"}
	if ProxyHash(a) == "" || ProxyHash(a) != ProxyHash(b) {
		t.Errorf("同样的配置应得到同样的指纹：%q %q", ProxyHash(a), ProxyHash(b))
	}
	b["port"] = 444
	if ProxyHash(a) == ProxyHash(b) {
		t.Error("配置不同时指纹不应相同")
	}
	if ProxyHash(nil) != "" {
		t.Error("空片段的指纹应为空")
	}
}
