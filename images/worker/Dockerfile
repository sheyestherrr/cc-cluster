# cc-cluster 默认的 worker 镜像：一个通用的开发环境基座。
#
# 有意不预装 Claude CLI：首次进入槽位后在槽内安装（装在该槽的 HOME 里，随 HOME 持久化），
# 这样安装与登录产生的访问都从该槽绑定的出口发出，宿主的地址不会出现在其中。
#
# worker 以只读 rootfs、非 root 用户运行，/etc/passwd、/etc/group、/etc/machine-id 由管理器挂入，
# 所以镜像里不需要预先创建用户。需要别的工具链时，以此为基础另建镜像，再用 slot add --image 指定。
ARG BASE_IMAGE=debian:12-slim
FROM ${BASE_IMAGE}
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash ca-certificates curl git less locales openssh-client procps ripgrep tzdata vim-tiny \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/dev /work
ENV LANG=C.UTF-8
