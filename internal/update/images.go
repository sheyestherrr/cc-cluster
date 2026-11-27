package update

import "strings"

// ImageRepo 是发布的镜像所在的仓库前缀，后面接 gateway 或 worker。
const ImageRepo = "ghcr.io/" + Repo + "-"

// Images 返回 v 这个版本发布的网关镜像与 worker 镜像。
func Images(v string) (gateway, worker string) {
	return ImageRepo + "gateway:" + v, ImageRepo + "worker:" + v
}

// ImageVersion 认出本项目发布的、带版本号的镜像：ref 是其中之一时返回它的版本。
// 自己构建的、别处来的、按摘要固定的、用 latest 这类滚动标签的都返回假，更新时不碰它们。
func ImageVersion(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, ImageRepo)
	if !ok {
		return "", false
	}
	name, tag, ok := strings.Cut(rest, ":")
	if !ok || (name != "gateway" && name != "worker") {
		return "", false
	}
	if _, ok := parseVersion(tag); !ok {
		return "", false
	}
	return tag, true
}

// ImagesVersion 看设置里的两个镜像：其中有本项目发布的、但不是 v 这个版本的，就返回它现在的版本。
// 两个都是 v 的，或者都不是本项目发布的，ok 为假。
func ImagesVersion(gateway, worker, v string) (cur string, ok bool) {
	for _, ref := range []string{gateway, worker} {
		if cur, ok := ImageVersion(ref); ok && cur != v {
			return cur, true
		}
	}
	return "", false
}

// ImagesBehind 报告镜像的设置是否比 v 旧，并返回它现在的版本。比 v 新（退回过旧版本的二进制）不算。
func ImagesBehind(gateway, worker, v string) (from string, behind bool) {
	if cur, ok := ImagesVersion(gateway, worker, v); ok && Newer(v, cur) {
		return cur, true
	}
	return "", false
}

// LocalNotes 取更新说明里的第一段：发布说明先写中文，用单独一行的 --- 隔开之后是英文和镜像地址。
// 没有分隔线，或者第一段是空的，就原样返回。
func LocalNotes(md string) string {
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "---" {
			if first := strings.TrimSpace(strings.Join(lines[:i], "\n")); first != "" {
				return first
			}
			break
		}
	}
	return strings.TrimSpace(md)
}
