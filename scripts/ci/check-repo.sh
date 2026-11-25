#!/bin/sh
# 仓库卫生检查：通用的凭据扫描器认不出本项目特有的敏感内容（订阅地址、带密码的代理地址），
# 也不管“哪些路径不该进版本库”，这里补上。持续集成里与 gitleaks 一起运行，本地也可以直接跑。
#
# 用法（在仓库根目录）：sh scripts/ci/check-repo.sh [--history]
#   不带参数只查当前被跟踪的文件；--history 连全部历史提交一起查（需要完整的克隆）。
#
# 命中时只打印位置（文件与行号，或提交号）和规则名，不打印内容：
# 持续集成的日志是公开的，不能把查到的东西再抄一遍。
#
# 确认是误报时，把 “规则名<TAB>文件路径或提交号” 加进同目录的 check-repo.allow。
set -eu

history=0
case "${1:-}" in
"") ;;
--history) history=1 ;;
*)
	echo "用法：sh scripts/ci/check-repo.sh [--history]" >&2
	exit 2
	;;
esac

self="scripts/ci/check-repo.sh"
allow="scripts/ci/check-repo.allow"
bad=0
tab="$(printf '\t')"

# allowed <规则名> <文件路径或提交号>
allowed() {
	[ -f "$allow" ] && grep -qxF "$1$tab$2" "$allow"
}

report() { # report <规则名> <位置> <说明>
	if allowed "$1" "${2%%:*}"; then
		return 0
	fi
	echo "[$1] $2：$3"
	bad=1
}

# ---- 一、不该进版本库的路径 ----
# 数据目录与运行期文件、本地的真实配置、临时材料、只读参考、构建产物、密钥与订阅地址文件，
# 以及维护者本机的材料（助手配置、笔记与草稿）：它们不随仓库发布。
paths_common='^(tmp|slots|state|run|_ref|dist|docs/task-archives|vendor|\.claude|\.agents|\.notes\.git)/|^docs/方案|^AGENTS\.md$|(^|/)node_modules/|\.(log|sock|pem|key|p12|pfx|url)$|(^|/)\.env$|(^|/)\.?credentials(\.json)?$|(^|/)(cluster\.env|proxies\.conf)$|^internal/web/dist/(assets/|index\.html$)'
# bin/ 现在是构建产物；历史上（改用 Go 之前）它放的是脚本，所以只对当前的文件查。
paths_tree="$paths_common|^bin/"

for f in $(git -c core.quotepath=off ls-files | grep -E "$paths_tree" || true); do
	report path "$f" "这个路径不应进版本库"
done
if [ "$history" = 1 ]; then
	for f in $(git -c core.quotepath=off log --all --pretty=format: --name-only --diff-filter=A | sort -u | grep -E "$paths_common" || true); do
		report path-history "$f" "历史提交里加入过这个路径"
	done
fi

# ---- 二、内容 ----
# 规则名与扩展正则一一对应。
#   url-token     地址的查询串里带着长令牌（订阅地址通常是这个样子）
#   share-link    代理的分享链接
#   url-password  地址里写着用户名和密码
#   anthropic-key Anthropic 的密钥
check() { # check <规则名> <正则> <说明>
	for hit in $(git -c core.quotepath=off grep -nIE -e "$2" -- . ":!$self" | cut -d: -f1,2 || true); do
		report "$1" "$hit" "$3"
	done
	if [ "$history" = 1 ]; then
		for c in $(git log --all --pretty=format:%h -G"$2" -- . ":!$self" || true); do
			report "$1-history" "$c" "这个提交的改动里出现过：$3"
		done
	fi
}

check url-token '[?&](token|key|secret|passwd|password)=[A-Za-z0-9_%-]{16,}' "地址的查询串里带着长令牌"
check share-link '(ss|ssr|vmess|vless|trojan|hysteria2?|hy2|tuic)://[A-Za-z0-9+/=_-]{16,}' "代理的分享链接"
check url-password "(socks[45]h?|https?)://[^/@[:space:]\"'<>]+:[^/@[:space:]\"'<>]{6,}@" "地址里写着用户名和密码"
check anthropic-key 'sk-ant-[A-Za-z0-9_-]{20,}' "Anthropic 的密钥"

if [ "$bad" = 1 ]; then
	echo "仓库卫生检查没有通过。真的是凭据就先作废它，再从历史里清除；误报加进 $allow。" >&2
	exit 1
fi
if [ "$history" = 1 ]; then
	echo "仓库卫生检查通过（当前文件与全部历史）"
else
	echo "仓库卫生检查通过（当前文件）"
fi
