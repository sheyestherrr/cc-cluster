#!/bin/sh
# 用编出来的数据预览网页界面：不需要 Docker，也不需要数据目录。在仓库根目录运行：
#
#   sh scripts/preview.sh [端口]
#
# 页面上能看到槽位、节点、订阅、流量曲线和操作记录的样子；数据是编的，页面上的操作都不会生效。
# 要看真实的数据用 scripts/web.sh。
#
# 每次启动前都重新构建前端（make web，需要 Node.js 与 pnpm），看到的总是当前源码。
# 跳过构建：NOBUILD=1 sh scripts/preview.sh
#
# 端口默认 7878，被占用时自动往后找；指定了端口就只用那一个。服务只监听 127.0.0.1。
set -eu

cd "$(dirname "$0")/.."

case "${1:-}" in
-h | --help)
	sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
	exit 0
	;;
"") ;;
*[!0-9]*)
	echo "用法：sh scripts/preview.sh [端口]" >&2
	exit 2
	;;
*) export PORT="$1" ;;
esac

command -v node >/dev/null 2>&1 || {
	echo "需要 Node.js" >&2
	exit 1
}

if [ "${NOBUILD:-}" != 1 ]; then
	echo "构建前端（make web）…" >&2
	if [ -f tmp/impl/go.sh ]; then
		# 本仓库约定的包装：工具链的缓存放在 tmp/impl/ 下，与其它构建共用
		sh tmp/impl/go.sh make web >&2
	else
		make web >&2
	fi
fi

exec node scripts/preview.mjs
