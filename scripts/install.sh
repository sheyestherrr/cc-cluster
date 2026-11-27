#!/bin/sh
# cc-cluster 的一键安装：下载发布的二进制，拉取网关与 worker 镜像，初始化数据目录。
#
#   curl -fsSL https://raw.githubusercontent.com/sheyestherrr/cc-cluster/master/scripts/install.sh | sh
#
# 可用的环境变量：
#   CC_CLUSTER_VERSION   要安装的版本，默认为最新正式版；也可指定版本号，如 v0.0.1
#   CC_CLUSTER_BIN_DIR   二进制的安装目录，默认 ~/.local/bin
#   CC_CLUSTER_DATA      数据目录，默认 ~/.cc-cluster
#   CC_CLUSTER_NO_INIT   设为 1 时只安装二进制并拉取镜像，不初始化数据目录
#
# 需要 Docker（当前用户可直接运行 docker）、curl 和 tar。不需要 root，不修改系统设置。
# 可重复运行：数据目录已存在时只更新二进制，不拉取镜像，也不修改数据目录
# （镜像设置由 cc-cluster update --images 更新）。
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
need() { command -v "$1" >/dev/null 2>&1 || die "未找到 $1，请先安装"; }

need curl
need tar
need docker
docker info >/dev/null 2>&1 || die "无法连接 Docker，请确认 Docker 已启动且当前用户有权限运行 docker"

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "不支持的操作系统：$(uname -s)（仅提供 Linux 与 macOS 的安装包）" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "不支持的架构：$(uname -m)（仅提供 amd64 与 arm64 的安装包）" ;;
esac

if [ "$VERSION" = latest ]; then
	# releases/latest 会重定向到最新正式版的页面，地址的最后一段就是版本号。
	url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || die "无法获取最新版本"
	VERSION=${url##*/}
fi
case $VERSION in
v[0-9]*) ;;
*) die "版本号 $VERSION 无效，格式应为 vX.Y.Z（如 v0.0.1）" ;;
esac
GATEWAY_IMAGE=ghcr.io/$REPO-gateway:$VERSION
WORKER_IMAGE=ghcr.io/$REPO-worker:$VERSION

name=cc-cluster_${VERSION}_${os}_${arch}
base=https://github.com/$REPO/releases/download/$VERSION
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "正在下载 $name.tar.gz …"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || die "下载 $base/$name.tar.gz 失败"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "下载校验和文件失败"

want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "校验和文件中缺少 $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
	die "未找到 sha256sum 或 shasum，无法验证下载的文件"
fi
[ "$want" = "$got" ] || die "$name.tar.gz 校验和不匹配，文件可能不完整"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
[ -f "$tmp/$name/cc-cluster" ] || die "安装包中缺少 cc-cluster"
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/$name/cc-cluster" "$BIN_DIR/cc-cluster"
say "已安装 $BIN_DIR/cc-cluster（$("$BIN_DIR/cc-cluster" version 2>/dev/null || echo "$VERSION")）"

pull_images() {
	say "正在拉取镜像 …"
	docker pull "$GATEWAY_IMAGE"
	docker pull "$WORKER_IMAGE"
}

if [ "${CC_CLUSTER_NO_INIT:-}" = 1 ]; then
	pull_images
	say "已跳过初始化（CC_CLUSTER_NO_INIT=1）。"
elif [ -e "$DATA" ]; then
	# 升级：只更新二进制。修改镜像设置会使运行中的槽位变为“配置待应用”，交给使用者决定何时进行。
	say "数据目录 $DATA 已存在，未作改动；镜像设置保持不变。"
	if [ "$DATA" = "$HOME/.cc-cluster" ]; then
		say "如需将镜像更新至 $VERSION，请在槽位空闲时执行：cc-cluster update --images"
	else
		say "如需将镜像更新至 $VERSION，请在槽位空闲时执行：cc-cluster --data-dir $DATA update --images"
	fi
else
	pull_images
	"$BIN_DIR/cc-cluster" --data-dir "$DATA" init --gateway-image "$GATEWAY_IMAGE" --worker-image "$WORKER_IMAGE"
fi

say ""
case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*)
	say "$BIN_DIR 不在 PATH 中，请将以下内容加入 shell 的启动文件："
	say "  export PATH=\"$BIN_DIR:\$PATH\""
	say ""
	;;
esac
say "安装完成。后续步骤："
say "  cc-cluster doctor      检查本机环境"
say "  cc-cluster web         启动网页界面"
