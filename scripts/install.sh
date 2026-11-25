#!/bin/sh
# cc-cluster 的一键安装：下载发布的二进制，拉取网关与 worker 镜像，初始化数据目录。
#
#   curl -fsSL https://raw.githubusercontent.com/sheyestherrr/cc-cluster/master/scripts/install.sh | sh
#
# 可以用环境变量调整：
#   CC_CLUSTER_VERSION   要装的版本，默认是最新的正式版；也可以写具体的版本号，如 v0.0.1
#   CC_CLUSTER_BIN_DIR   二进制放在哪里，默认 ~/.local/bin
#   CC_CLUSTER_DATA      数据目录，默认 ~/.cc-cluster
#   CC_CLUSTER_NO_INIT   设为 1 时只装二进制和镜像，不初始化数据目录
#
# 需要 Docker（当前用户能直接运行 docker）、curl 和 tar。不需要 root，不改系统设置。
# 重复运行是安全的：二进制和镜像会更新，已有的数据目录不动。
set -eu

REPO=sheyestherrr/cc-cluster
VERSION=${CC_CLUSTER_VERSION:-latest}
BIN_DIR=${CC_CLUSTER_BIN_DIR:-$HOME/.local/bin}
DATA=${CC_CLUSTER_DATA:-$HOME/.cc-cluster}

say() { printf '%s\n' "$*"; }
die() {
	printf '安装失败：%s\n' "$*" >&2
	exit 1
}
need() { command -v "$1" >/dev/null 2>&1 || die "需要 $1，请先安装它"; }

need curl
need tar
need docker
docker info >/dev/null 2>&1 || die "当前用户运行不了 docker：确认 Docker 已启动，并且你在 docker 组里"

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "不支持的系统：$(uname -s)（只有 Linux 和 macOS 的发布包）" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "不支持的架构：$(uname -m)（只有 amd64 和 arm64 的发布包）" ;;
esac

if [ "$VERSION" = latest ]; then
	# releases/latest 会跳转到最新正式版的页面，地址的最后一段就是版本号。
	url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || die "查不到最新的版本"
	VERSION=${url##*/}
fi
case $VERSION in
v[0-9]*) ;;
*) die "版本号 $VERSION 不对：应当是 v0.0.1 这样的写法" ;;
esac
GATEWAY_IMAGE=ghcr.io/$REPO-gateway:$VERSION
WORKER_IMAGE=ghcr.io/$REPO-worker:$VERSION

name=cc-cluster_${VERSION}_${os}_${arch}
base=https://github.com/$REPO/releases/download/$VERSION
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "下载 $name.tar.gz …"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || die "下载不到 $base/$name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "下载不到校验和文件"

want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "校验和文件里没有 $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
	die "需要 sha256sum 或 shasum 来核对下载的文件"
fi
[ "$want" = "$got" ] || die "校验和对不上，下载的文件可能不完整"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
[ -f "$tmp/$name/cc-cluster" ] || die "压缩包里没有找到 cc-cluster"
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/$name/cc-cluster" "$BIN_DIR/cc-cluster"
say "已安装 $BIN_DIR/cc-cluster（$("$BIN_DIR/cc-cluster" version 2>/dev/null || echo "$VERSION")）"

say "拉取镜像 …"
docker pull "$GATEWAY_IMAGE"
docker pull "$WORKER_IMAGE"

if [ "${CC_CLUSTER_NO_INIT:-}" = 1 ]; then
	say "按要求跳过了初始化。"
elif [ -e "$DATA" ]; then
	# 升级：数据目录里用的是本项目发布的镜像时，把它换成这个版本的；自己指定的镜像不动。
	if grep -q "\"ghcr.io/$REPO-gateway:" "$DATA/cluster.json" 2>/dev/null &&
		"$BIN_DIR/cc-cluster" --data-dir "$DATA" images --gateway-image "$GATEWAY_IMAGE" --worker-image "$WORKER_IMAGE" >/dev/null 2>&1; then
		say "数据目录 $DATA 已经存在：镜像换成了 $VERSION 的。"
		say "运行中的容器仍在用原来的镜像；在网页里把网关停止后再启动、给槽位重建 worker 之后换成新的。"
	else
		say "数据目录 $DATA 已经存在，保持原样。"
	fi
else
	"$BIN_DIR/cc-cluster" --data-dir "$DATA" init --gateway-image "$GATEWAY_IMAGE" --worker-image "$WORKER_IMAGE"
fi

say ""
case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*)
	say "$BIN_DIR 不在 PATH 里，把这一行加进 shell 的启动文件："
	say "  export PATH=\"$BIN_DIR:\$PATH\""
	say ""
	;;
esac
say "装好了。接下来："
say "  cc-cluster doctor      检查这台机器能否运行"
say "  cc-cluster web         打开网页界面"
