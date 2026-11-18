# cc-cluster 网关镜像：固定版本的 Mihomo 核心 + 监督脚本。
# 核心版本是固定的，升级前重新跑 e2e。
ARG MIHOMO_IMAGE=metacubex/mihomo:v1.19.33
ARG BASE_IMAGE=alpine:3.22

FROM ${MIHOMO_IMAGE} AS mihomo

FROM ${BASE_IMAGE}
# iproute2：策略路由与 veth；curl：经控制 socket 重载配置、经回环入口探测出口。
RUN apk add --no-cache iproute2 curl \
    && mkdir -p /var/lib/ccgw /run/ccgw
COPY --from=mihomo /mihomo /usr/local/bin/mihomo
COPY entrypoint.sh /usr/local/bin/ccgw-entrypoint
RUN chmod 0755 /usr/local/bin/ccgw-entrypoint
ENTRYPOINT ["/usr/local/bin/ccgw-entrypoint"]
