#!/bin/sh
# 一键启动网页界面，用于本机预览。在仓库根目录运行：
#
#   sh scripts/web.sh [数据目录] [cc-cluster web 的参数，例如 --port 8080]
#
# 数据目录的取法依次是：第一个参数、环境变量 CC_CLUSTER_DATA、仓库里的 tmp/web-data。
# 用到最后一种且它还不存在时，脚本会先执行一次 cc-cluster init（容器名前缀为 ccweb）。
#
# 每次启动前都重新构建（make release，需要 Go、Node.js 与 pnpm），看到的总是当前源码。
# 只想用现成的二进制、跳过构建：NOBUILD=1 sh scripts/web.sh
#
# 服务只监听 127.0.0.1。在别的机器上看页面时，先在那台机器上执行
#   ssh -L 7878:127.0.0.1:7878 <这台机器>
# 再用浏览器打开本脚本打印的地址。按 Ctrl-C 停止。
set -eu

cd "$(dirname "$0")/.."

BIN=bin/cc-cluster
DEFAULT_DATA=tmp/web-data

case "${1:-}" in
-h | --help)
	sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
	exit 0
	;;
"" | -*)
	DATA=${CC_CLUSTER_DATA:-$DEFAULT_DATA}
	;;
*)
	DATA=$1
	shift
	;;
esac

if [ "${NOBUILD:-}" != 1 ]; then
	echo "构建带网页界面的二进制（make release）…" >&2
	if [ -f tmp/impl/go.sh ]; then
		# 本仓库约定的包装：工具链的缓存放在 tmp/impl/ 下，与其它构建共用
		sh tmp/impl/go.sh make release >&2
	else
		make release >&2
	fi
fi

if [ "$DATA" = "$DEFAULT_DATA" ] && [ ! -f "$DATA/cluster.json" ]; then
	echo "初始化预览用的数据目录 $DATA …" >&2
	"$BIN" --data-dir "$DATA" init --name ccweb >&2
fi

echo "数据目录：$DATA" >&2
exec "$BIN" --data-dir "$DATA" web "$@"
