# 架构说明

这份文档讲 cc-cluster 是怎么搭起来的，给想了解原理、排查问题或参与开发的人看。只想用的话读 [README](README.md) 就够了。

## 目标与边界

- 每个槽位的全部流量从它绑定的出口出去；任何环节出问题，结果是**没网**，不是直连。
- 槽位里看不到代理的痕迹：没有代理变量，没有 TUN 网卡，没有本地的代理端口。
- 宿主侧只要求能用 Docker：不改宿主的 iptables / nft，不装内核模块，不需要 root。

不在目标里的：Docker 共享宿主内核，内核版本、开机时长、CPU 型号这些信息各槽位相同，本项目不处理，也不承诺账号之间不被关联。

## 用到了什么

| 层 | 内容 | 是否使用 |
| --- | --- | --- |
| Docker 功能 | `--network none`、`--network container:`、`--pid container:`、sysctl、`NET_ADMIN`、`/dev/net/tun`、bind mount、资源限额 | 使用 |
| 内核基本功能 | 网络命名空间、veth、策略路由（`ip rule`）、IP 转发、TUN | 使用 |
| 内核防火墙 | iptables、nftables、conntrack | **不使用** |
| 宿主上的工具 | 宿主的 iptables、nft、python | 不使用（tmux 只在开启桥接时用到） |

veth 是 Docker 自己建容器网络用的设备，能跑 Docker 的内核一定有。某台机器没有 `/dev/net/tun` 或不允许 `NET_ADMIN` 时，`cc-cluster doctor` 会直接报告不可用。

## 组成

```
 worker A ── veth /30 ──┐
 worker B ── veth /30 ──┼─▶ 网关容器 ──▶ 首跳（中转，或直连时的落地）──▶ 落地 ──▶ 目标
 worker C ── veth /30 ──┘   策略路由 → TUN → Mihomo 按来源子网选出口
```

| 对象 | 网络 | 权限 | 说明 |
| --- | --- | --- | --- |
| worker | `--network none`，由助手接一条到网关的 veth | 去掉全部能力，`no-new-privileges`，私有 cgroup 命名空间 | 一个槽位一个；HOME、hostname、MAC、machine-id 首次生成后固定 |
| 网关 | 出口网络 `cc-egress`，加上每个槽位一条 veth | 只有 `NET_ADMIN` 和 `/dev/net/tun` | 核心（Mihomo）加一个监督脚本；面向 worker 的地址上不监听任何端口 |
| 助手 | 临时加入目标容器的网络命名空间 | 只有 `NET_ADMIN`，执行完即退出 | 接线、撤线、从 worker 的位置实测出口 |
| 管理器 | 宿主上的 `cc-cluster` 进程 | 当前用户，经 Docker 的 Unix socket 操作 | 命令行、交互面板、网页界面共用同一套实现（`internal/engine`） |

管理器不常驻：它退出后已经接好线的槽位照常工作。

## 数据路径

### 接线

每个槽位在所属网关的地址池（默认 `10.213.250.0/24`）里占一个 `/30`：第一个地址给网关一端，第二个给 worker。网卡名、策略路由优先级和探测端口都由这个 `/30` 的序号算出，同一个槽位每次接线得到的名字相同。

```sh
# 助手：在 worker 的网络命名空间里，同时加入网关的 PID 命名空间
ip link add eth0 type veth peer name cs2 netns /proc/1/ns/net   # 对端放进网关
ip link set eth0 address <该槽固定的 MAC>
ip addr add 10.213.250.10/30 dev eth0 && ip link set eth0 up
ip route add default via 10.213.250.9 dev eth0

# 网关一端
ip addr add 10.213.250.9/30 dev cs2 && ip link set cs2 up
ip rule add pref 1004 iif cs2 from 10.213.250.8/30 lookup 100   # 该槽的包只能进 TUN
ip rule add pref 1005 iif cs2 unreachable                       # TUN 不在时直接不可达
```

包不经过宿主的网桥和防火墙，所以不受宿主配置的影响。代价是 Docker 不知道这块网卡：worker 或网关任何一方重启后线就没了，要由管理器重新接（`slot up`），在那之前该槽没有网络。

### 网关的路由

监督脚本（`images/gateway/entrypoint.sh`）启动时清空主路由表；之后主路由表里只有管理器写入的**首跳 `/32`**。核心的 TUN 网卡出现后，脚本让路由表 100 的默认路由指向它；TUN 消失时内核自动删掉这条路由。

由此得到：

- worker 发来的包，不管目的是什么，只有“进 TUN”和“不可达”两个去向，不会被网关直接转发到出口网卡。
- 核心自己发出的连接走主路由表，那里只有首跳。配置写错或节点数据有问题，都连不到首跳以外的地址。
- 来源地址不属于该槽子网的包不匹配第一条规则，落到不可达。

### 核心的配置

由 `internal/mihomo` 从槽位配置生成，不手写，也不接受订阅里的规则：

- TUN 入站，用户态协议栈（gVisor），`auto-route` 关闭——路由由上面两节负责。
- 规则只有四类，按顺序：拒绝 UDP；拒绝目的为私网、回环、链路本地的连接；`SRC-IP-CIDR,<槽位的 /30>,<该槽的出口>`；其余一律拒绝。没有 `DIRECT`。
- 落地经 `dialer-proxy` 接在中转后面。有候选中转时，中转是一个 `fallback` 组：只在你登记的几个中转之间改用，落地不变，所以出口不变。
- 每条链路在回环地址上有一个入口（`127.0.0.1:17000+序号`），只在网关容器内可达，供管理器探测出口用。
- 节点保存为 Mihomo 原生的 proxy 片段；`dialer-proxy`、`interface-name`、`routing-mark` 这些由管理器控制的字段不允许自带，`udp` 强制为 `false`。

### DNS

槽内的 `/etc/resolv.conf` 只有一行普通的 `nameserver`。查询包经默认路由到网关，在 TUN 层被核心接管，答回 `100.64.0.0/10` 里的映射地址（fake-ip）；连接时核心把域名交给落地，由落地一侧解析。程序自己指定别的 DNS 服务器，结果相同。

首跳是域名时，由管理器在宿主上解析一次，写进核心的 `hosts` 和首跳路由，每次启动网关前复查。

## 出了问题会怎样

| 情况 | 结果 |
| --- | --- |
| 核心崩溃或被杀 | TUN 消失，表 100 的路由随之消失；worker 的连接“网络不可达”。监督脚本重启核心后自动恢复 |
| 网关容器停止或被删 | 各槽位的 veth 随网关消失，worker 只剩 `lo` |
| 中转不可达 | 有候选时核心自动改用候选，出口不变；没有候选就是没网 |
| 落地不可达 | 没网。不会改从中转或别的节点出去 |
| 出口地址变了 | 持续检测（`watch`）发现后给该槽断网并保持，等人处理 |
| 手动断网（`slot hold`） | 删掉该槽的 veth 并写下持久标记；别的槽位不受影响 |
| worker 重启 | 没网，直到重新 `slot up` |
| 管理器退出 | 已经运行的槽位不受影响 |
| 宿主防火墙被重载或清空 | 不受影响，包不经过宿主防火墙 |

共用一个网关的代价是核心崩溃时接在它上面的槽位一起断网。配置重载不打断别的槽位已有的连接。

## 启动一个槽位

`slot up` 按下面的顺序做，任何一步不符都不放行，已经接上的线会撤掉：

1. 对齐网关：生成核心配置，校验后在线重载；写入首跳路由。
2. 检查链路：经核心的控制接口测连通性。整条链路不通时再单独测中转，区分“中转不通”和“中转可用但连不上落地”。
3. 探测出口：经该链路的回环入口访问探测地址，取得出口 IP、时区和归属；落地登记了预期出口的，不符就停下。
4. 启动 worker（此时没有网络），写入身份相关的文件。
5. 助手接线，网关登记该槽的策略路由。
6. 助手从 worker 的位置再测一次出口，与第 3 步不一致就撤线。
7. 记下已应用配置的摘要。之后才允许 `enter`。

第 6 步防的是“映射写错导致串线”（A 的流量从 B 的落地出去）：规则虽然只由管理器生成，放行前仍然实测一遍。

## 槽位里能看到什么

每个槽位首次创建时生成一份身份并保存，之后重建容器、重新接线都沿用：用户名、hostname、MAC、machine-id。启动时还会删除 `/.dockerenv`，把 `/etc/resolv.conf` 改成一行 `nameserver`，按出口所在地设置时区，遮蔽 DMI。创建容器时还会写入一个 `/etc/profile.d` 片段：登录 shell 读 `/etc/profile` 时 `PATH` 会被发行版重设，这个片段把 `$HOME/.local/bin`（Claude Code 的安装位置）补回去。

| 项目 | 槽内的观测 |
| --- | --- |
| 网卡 | `lo` 和一块 `eth0`，私网地址，默认路由指向同网段的一个地址；没有 TUN |
| 环境变量 | 没有任何代理变量 |
| 本地端口 | 没有代理或 DNS 监听；网关地址上没有开放的端口 |
| DNS | 一行普通的 `nameserver`；解析结果是 `100.64.0.0/10` 里的映射地址 |

仍然可以察觉的：

- 向任何地址发起 TCP 连接，握手都会立刻成功，被拒绝的目的随后才断开——握手由网关代答，这是透明代理的固有特征。
- UDP 和 ping 不通，QUIC 会退回 TCP。
- 各槽位共同的内核信息：`boot_id`、内核版本、开机时长、CPU 型号。`--fake-proc` 只覆盖 `/proc/cpuinfo`、`/proc/meminfo` 的显示并绑核，`/proc/stat` 仍是宿主的核数。
- bind mount 的宿主路径会出现在 `/proc/self/mountinfo` 里。

## 数据目录

管理器的状态全部是数据目录下的私有文件，没有数据库：

```text
<数据目录>/                     # 0700
├── cluster.json               # 集群设置：容器名前缀、两个镜像
├── nodes.d/<节点>.json         # 节点片段与角色（中转 / 落地），含凭据，0600
├── subs/                      # 订阅及其节点、测速结果
├── gateways/<网关>/gateway.json # 期望配置：中转与候选、出口网络、地址池
├── slots/<槽位>/
│   ├── slot.json              # 期望配置：网关、出口、挂载、资源
│   ├── identity.json          # 首次生成后不变
│   ├── applied.json           # 已应用配置的摘要与容器
│   ├── hold.json / watch.json # 手动断网的标记 / 持续检测的结果
│   └── home/                  # 该槽完整的 HOME
├── ops.jsonl                  # 追加式的操作记录，不含凭据
└── lock
```

“有没有待应用的改动”由期望配置的摘要与已应用的摘要比较得出，不单独存储。带凭据的内容经 Docker 的归档接口写进容器，不出现在命令行参数、容器元数据和操作记录里。

## 网页界面与宿主 tmux 桥接

**网页界面**嵌在二进制里，只监听 `127.0.0.1`，接口要带启动时生成的一次性令牌并核对 `Host` 头；返回的现状里不含任何节点凭据。它开着的时候顺带做持续检测。

**宿主 tmux 桥接**按槽位开启：管理器在宿主上为该槽启动一个专用的 tmux server，把 socket 目录挂进 worker，槽内的 Claude Code 经 tmux-mcp 操作它。这是一条有意打开的通道——经它执行的命令是宿主上的命令，用的是宿主用户的权限和宿主的网络，不受槽位出口和断网的约束。默认不开放结束 session / window / pane 的工具。

## 代码在哪里

| 路径 | 内容 |
| --- | --- |
| `internal/model` | 配置对象与校验 |
| `internal/store` | 数据目录的读写与锁 |
| `internal/netplan` | 槽位的编址与由序号派生的名字 |
| `internal/identity` | 槽位身份的生成 |
| `internal/mihomo` | 核心配置的生成 |
| `internal/subscription` | Clash 格式订阅的解析 |
| `internal/dockerapi` | 经 Unix socket 的最小 Docker Engine API 客户端 |
| `internal/engine` | 编排：网关对齐、接线、探测、持续检测、tmux 桥接 |
| `internal/cli`、`internal/web` | 命令行与交互面板、网页服务 |
| `images/gateway`、`images/worker` | 两个镜像 |
| `test/e2e` | 端到端测试：两个 internal 网络加假节点，不访问外部地址 |
