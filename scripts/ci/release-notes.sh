#!/bin/sh
# 从 CHANGELOG.md 里取出一个版本的那一段，输出到标准输出；发布时用作 Release 的说明。
#
#   sh scripts/ci/release-notes.sh v0.0.1
#
# 一段从 "## v0.0.1" 开头的标题行之后开始，到下一个 "## " 标题之前结束。找不到或是空的就失败。
set -eu

version=${1:?用法：release-notes.sh <版本>}
file=${2:-CHANGELOG.md}

notes=$(awk -v v="$version" '
	/^## / {
		if (on) exit
		on = ($2 == v)
		next
	}
	on { print }
' "$file")

if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
	echo "$file 里没有 $version 的更新记录：先写好再打标签" >&2
	exit 1
fi
printf '%s\n' "$notes"
