# cc-cluster 默认的 worker 镜像：Claude Code 的通用开发环境。
#
# 软件包的选择参考了 Claude Code 官方开发容器
# （https://github.com/anthropics/claude-code/tree/main/.devcontainer 的 Dockerfile）：
# 同样以 Node 官方镜像为底，带 git、gh、jq、fzf、zsh 等日常工具。本文件是重新编写的，没有复制原文。
#
# 与官方开发容器有意不同的地方：
#   - 不装 iptables / ipset / iproute2 / dnsutils / aggregate，也没有 init-firewall.sh 与 sudo。
#     官方用它们在容器内部做域名白名单，需要容器自己有 NET_ADMIN 与 NET_RAW；
#     这里的 worker 没有任何能力，网络约束在 worker 碰不到的网关里。
#   - 不设 DEVCONTAINER=true：那是一个“我在开发容器里”的标记，而且所有槽都一样。
#   - 不预装 Claude Code：首次进入槽位时装进该槽的 HOME，随 HOME 持久化并各自更新，
#     安装与更新产生的访问都从该槽绑定的出口发出。
#   - 不保留镜像自带的 node 用户：每个槽的用户由管理器在容器创建后写入 /etc/passwd。
#
# 需要别的工具链时，以此为基础另建镜像，再用 slot add --image 指定。
# 自定义镜像至少要有 /bin/sh、sleep、rm、ln；想让时区生效还需要 tzdata。
ARG BASE_IMAGE=node:24-bookworm
FROM ${BASE_IMAGE}
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash ca-certificates curl fzf gh git gnupg2 jq less locales man-db nano \
        openssh-client procps ripgrep tzdata unzip vim zsh \
    && rm -rf /var/lib/apt/lists/* \
    && sed -i 's/^# *\(en_US.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
    && locale-gen \
    && (userdel -r node 2>/dev/null || true)
