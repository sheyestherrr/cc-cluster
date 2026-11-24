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
# 另外带两个只在开启“宿主 tmux 桥接”的槽位里用到的程序（所有槽的镜像都带，不随开关变化）：
#   - tmux：只作为客户端去连宿主上的 tmux server，必须与宿主的 tmux 同一版本
#     （实测发行版自带的 3.3a、3.5a 都连不上 3.8 的 server）。所以由 make image-worker 把宿主
#     tmux -V 的版本号传进来，在构建阶段从 tmux 官方源码包编译；宿主升级 tmux 后重建镜像即可。
#     没有给版本号时（宿主没有 tmux；发布到镜像仓库的那一份也不给）只有发行版自带的那一个。
#   - tmux-mcp-rs：https://github.com/bnomei/tmux-mcp（MIT），用 npm 安装，取当时的最新版。
#     npm 包只是一个启动脚本，真正的二进制在第一次运行时才从 GitHub Releases 下载到缓存目录；
#     这里在构建时就运行一次让它落在镜像里（否则每个槽位第一次使用时都要各自经出口去下载），
#     再把命令直接指向这个二进制，槽内不需要额外的环境变量。
#
# 需要别的工具链时，以此为基础另建镜像，再用 slot add --image 指定。
# 自定义镜像至少要有 /bin/sh、sleep、rm、ln；想让时区生效还需要 tzdata。
ARG BASE_IMAGE=node:24-bookworm

FROM ${BASE_IMAGE} AS tmux-build
ARG TMUX_VERSION=
RUN mkdir -p /out \
    && if [ -n "$TMUX_VERSION" ]; then \
        apt-get update \
        && apt-get install -y --no-install-recommends \
            bison build-essential ca-certificates curl libevent-dev libncurses-dev pkg-config \
        && curl -fsSL "https://github.com/tmux/tmux/releases/download/${TMUX_VERSION}/tmux-${TMUX_VERSION}.tar.gz" | tar -xz -C /tmp \
        && cd "/tmp/tmux-${TMUX_VERSION}" \
        && ./configure --prefix=/usr/local \
        && make -j"$(nproc)" \
        && install -m 0755 tmux /out/tmux; \
    fi

FROM ${BASE_IMAGE}
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash ca-certificates curl fzf gh git gnupg2 jq less locales man-db nano \
        openssh-client procps ripgrep tmux tzdata unzip vim zsh \
    && rm -rf /var/lib/apt/lists/* \
    && sed -i 's/^# *\(en_US.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
    && locale-gen \
    && (userdel -r node 2>/dev/null || true)
# 编译出来的 tmux 放在 /usr/local/bin，排在发行版那一个（/usr/bin，同时带来了运行库）前面。
COPY --from=tmux-build /out/ /usr/local/bin/
RUN npm install -g @bnomei/tmux-mcp-rs \
    && TMUX_MCP_RS_NPM_CACHE=/usr/local/share/tmux-mcp-rs tmux-mcp-rs --version \
    && bin="$(find /usr/local/share/tmux-mcp-rs -type f -name tmux-mcp-rs)" \
    && chmod 0755 "$bin" && chmod -R a+rX /usr/local/share/tmux-mcp-rs \
    && ln -sf "$bin" /usr/local/bin/tmux-mcp-rs \
    && tmux-mcp-rs --version \
    && npm cache clean --force
