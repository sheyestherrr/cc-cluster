# cc-cluster

为 Claude Code 提供相互独立的工作槽位：每个槽位有自己的 HOME、稳定的身份和固定的代理出口。

每个槽位的 worker 容器默认没有网络，全部流量由网关容器透明转发到绑定的落地（中转 → 落地，或直连落地）。worker 内没有代理变量，也看不到 TUN 网卡；网关出问题时的结果是没网，不是直连。宿主侧只要求“用户在 docker 组里”，不依赖宿主的 iptables / nft 或特定内核版本。

## 当前状态

首版 Go 管理器已实现，并通过了全程使用假节点的端到端测试。**尚未用真实代理节点和 Claude CLI 验证**，登录出口对齐、网页和 tmux 桥接尚未实现。

Docker 共享宿主内核，独立配置与资源限额不能证明物理机级隔离或账号不会关联。

## 构建与测试

需要 Go 1.23 或更新版本；不下载任何第三方 Go 依赖。

```sh
make check          # go vet、gofmt 检查、单元测试；不需要 Docker
make build          # 生成 bin/cc-cluster
make images         # 构建网关镜像与默认的 worker 镜像
make e2e            # 端到端测试：需要 Docker 与网关镜像，只用 internal 网络和假节点
```

## 使用

数据目录必须显式给出，用 `--data-dir` 或环境变量 `CC_CLUSTER_DATA`。

```sh
export CC_CLUSTER_DATA=/path/to/cc-data

cc-cluster init
cc-cluster doctor                                   # 检查这台机器能否运行，不访问外部地址

# 节点：Mihomo 原生 proxy 片段（JSON），或用参数描述简单的 SOCKS5 / HTTP 节点
cc-cluster node add hk --role transit --file transit.json --connect-ipv4 203.0.113.10
cc-cluster node add home-a --role landing --type socks5 --server 198.51.100.20 --port 1080 \
    --username u --password-file ./pw.txt --expect-exit-ip 198.51.100.20

# 网关：共享一个中转；不带 --transit 即单跳直连落地，此时一个网关只能接一个槽
cc-cluster gateway add main --transit hk

# 槽位
cc-cluster slot add alice --gateway main --landing home-a --mount /data/proj:/work --memory 8g
cc-cluster slot up alice          # 对齐网关 → 探测链路 → 启动无网的 worker → 接线 → 从 worker 位置复测出口
cc-cluster status
cc-cluster slot enter alice       # 只有状态为 ready 才允许进入

cc-cluster slot hold alice --reason "出口异常"      # 立即断网并保持，直到 release
cc-cluster slot release alice && cc-cluster slot up alice
```

默认的 worker 镜像不预装 Claude CLI：首次进入槽位后在槽内安装，它会装进该槽的 HOME 并随 HOME 持久化，安装与更新产生的访问都从该槽的出口发出。

worker 或网关重启后，线不会自动接回：重新执行 `slot up`。在那之前该槽保持没网。

## 目录

| 路径 | 内容 |
| --- | --- |
| `cmd/cc-cluster` | 程序入口 |
| `internal/` | 配置模型、文件状态、编址、核心配置生成、Docker API 客户端、编排与命令行 |
| `images/gateway` | 网关镜像：固定版本的 Mihomo 与监督脚本 |
| `images/worker` | 默认的 worker 基础镜像 |
| `test/e2e` | 端到端测试与假服务 |

## 致谢

宿主桥接（tmux-mcp）使用本人的真实宿主权限，容器的代理规则不覆盖宿主命令；该功能尚未实现。
