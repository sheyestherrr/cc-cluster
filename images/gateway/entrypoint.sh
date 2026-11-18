#!/bin/sh
# cc-cluster 网关监督进程。
#
# 职责只有三件：
#   1. 启动时清空主路由表。之后主路由表里只有管理器写入的首跳 /32，
#      核心配置写错或节点数据有问题，都连不到首跳以外的地址。
#   2. 拉起核心，退出后重启。
#   3. TUN 网卡出现后，让路由表 100 的默认路由指向它。TUN 消失时内核自动删除这条路由，
#      各槽的包随即落到"不可达"规则上；不使用 iptables / nft。
#
# 核心配置由管理器写入 /var/lib/ccgw/config.yaml；每个槽的 veth 与策略路由也由管理器登记。
set -u

HOME_DIR=/var/lib/ccgw
RUN_DIR=/run/ccgw
CONFIG="$HOME_DIR/config.yaml"
TUN=cc0
TABLE=100

log() { echo "ccgw: $*"; }

pid=""
stop() {
    log "收到停止信号"
    [ -n "$pid" ] && kill "$pid" 2>/dev/null
    exit 0
}
trap stop TERM INT

# TUN 在而表里没有默认路由时补上；返回 0 表示路由已就位。
ensure_route() {
    ip link show "$TUN" >/dev/null 2>&1 || return 1
    if ip route show table "$TABLE" 2>/dev/null | grep -q '^default'; then
        return 0
    fi
    ip route replace default dev "$TUN" table "$TABLE" 2>/dev/null
}

ip route flush table main
ip route flush table "$TABLE" 2>/dev/null
mkdir -p "$RUN_DIR"
# 管理器看到这个文件才开始写首跳路由，避免与上面的清空动作抢先后。
touch "$RUN_DIR/ready"
log "主路由表已清空，等待配置"

while :; do
    if [ ! -s "$CONFIG" ]; then
        sleep 0.5
        continue
    fi
    rm -f "$HOME_DIR/mihomo.sock"
    mihomo -d "$HOME_DIR" -f "$CONFIG" &
    pid=$!
    log "核心已启动 pid=$pid"
    routed=0
    while kill -0 "$pid" 2>/dev/null; do
        if ensure_route; then
            [ "$routed" = 0 ] && log "TUN 就绪，表 $TABLE 已指向 $TUN"
            routed=1
        else
            routed=0
        fi
        sleep 0.5
    done
    wait "$pid"
    log "核心退出 rc=$?，1 秒后重启"
    pid=""
    sleep 1
done
