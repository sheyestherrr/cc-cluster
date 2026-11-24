# cc-cluster

[![检查](https://github.com/sheyestherrr/cc-cluster/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/sheyestherrr/cc-cluster/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/sheyestherrr/cc-cluster?include_prereleases)](https://github.com/sheyestherrr/cc-cluster/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

在一台机器上给 Claude Code 开多个互不相干的“工作槽位”：每个槽位是一个 Docker 容器，有自己的 HOME、用户名、hostname、machine-id，和一个**固定的代理出口**。

- **默认没有网络。** 槽位的容器以 `--network none` 启动，全部流量由一个网关容器透明转发到你给它绑定的出口。网关、核心或链路出了问题，结果是没网，不是直连。
- **容器里看不出代理。** 没有 `HTTPS_PROXY` 一类的变量，没有 TUN 网卡，不读代理变量的程序同样被接管。
- **只要求能用 Docker。** 不改宿主的 iptables / nft，不依赖特定内核版本，普通用户在 docker 组里就够。
- **一个二进制。** 命令行、终端里的交互面板、本机网页界面三种用法，做的是同一件事。

> Docker 共享宿主内核。独立的配置与资源限额不等于物理机级的隔离，也不能保证账号之间不被关联；请自行判断它是否满足你的需要。

## 目录

- [它是怎么工作的](#它是怎么工作的)
- [安装](#安装)
- [五分钟上手](#五分钟上手)
- [日常使用](#日常使用)
- [出口的两种来源](#出口的两种来源)
- [网页界面与交互面板](#网页界面与交互面板)
- [宿主 tmux 桥接（可选）](#宿主-tmux-桥接可选)
- [命令速查](#命令速查)
- [要知道的几件事](#要知道的几件事)
- [验证过什么，没验证过什么](#验证过什么没验证过什么)
- [从源码构建与开发](#从源码构建与开发)
- [许可与致谢](#许可与致谢)

## 它是怎么工作的

```
 槽位 alice（没有网络）──┐
 槽位 bob  （没有网络）──┼─ veth ─▶ 网关容器（Mihomo TUN）─▶ 中转（可选）─▶ 落地 ─▶ 互联网
 槽位 carol（没有网络）──┘            按来源地址选出口                    每个槽位各自的出口
```

几个词：

| 词 | 意思 |
| --- | --- |
| **槽位**（slot） | 一个工作环境：一个 worker 容器加它的 HOME、身份和出口 |
| **落地**（landing） | 槽位的出口节点，网站看到的就是它的地址。通常是你自己的固定 IP 代理 |
| **中转**（transit） | 多个槽位共用的第一跳，通常取自机场订阅；可以没有 |
| **网关**（gateway） | 替槽位转发流量的容器。多个槽位可以共用一个 |
| **直出** | 没有落地时，直接拿订阅里的一个节点当出口 |

接线、路由、故障时的行为等细节见[架构说明](ARCHITECTURE.md)。

## 安装

**需要**：Linux，Docker（当前用户能直接运行 `docker`）。可选：`tmux` 3.0 以上（只有用宿主 tmux 桥接时才需要）。

### 1. 下载二进制

到 [Releases](https://github.com/sheyestherrr/cc-cluster/releases) 下载对应平台的压缩包（Linux / macOS，amd64 / arm64），解开后把 `cc-cluster` 放进 `PATH`：

```sh
# 以 Linux amd64 的 nightly（master 上最新一次构建）为例；正式版把 nightly 换成版本号，如 v0.1.0
curl -LO https://github.com/sheyestherrr/cc-cluster/releases/download/nightly/cc-cluster_nightly_linux_amd64.tar.gz
curl -LO https://github.com/sheyestherrr/cc-cluster/releases/download/nightly/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf cc-cluster_nightly_linux_amd64.tar.gz
install -m 0755 cc-cluster_nightly_linux_amd64/cc-cluster ~/.local/bin/
cc-cluster version
```

### 2. 准备两个镜像

```sh
# 网关镜像：直接拉取（标签与你下载的版本一致）
docker pull ghcr.io/sheyestherrr/cc-cluster-gateway:nightly

# worker 镜像：要在本机构建。它里面的 tmux 按宿主的版本编译，这样宿主桥接才连得上
git clone https://github.com/sheyestherrr/cc-cluster.git
make -C cc-cluster image-worker        # 得到 cc-cluster-worker:dev，只需要 docker 和 make
```

想自己构建网关镜像也可以：`make -C cc-cluster image-gateway`，得到 `cc-cluster-gateway:dev`。

### 3. 初始化

数据目录（槽位的 HOME、身份、节点配置都在里面）必须显式指定：

```sh
export CC_CLUSTER_DATA=~/cc-data        # 或者每次加 --data-dir

cc-cluster init --gateway-image ghcr.io/sheyestherrr/cc-cluster-gateway:nightly
cc-cluster doctor                       # 检查这台机器能否运行；不访问外部地址
```

`doctor` 全部通过就可以往下走了。

## 五分钟上手

下面假设你有一个自己的落地（SOCKS5 或 HTTP 代理）和一份 Clash 格式的机场订阅。没有落地的看[直出](#出口的两种来源)，没有订阅的可以不要中转。

```sh
# 1. 登记落地。密码放在文件里，不出现在命令行上
cc-cluster node add home-a --role landing --type socks5 \
    --server 198.51.100.20 --port 1080 --username u --password-file ./pw.txt

# 2. 导入订阅，挑一个能接通落地的节点当中转
cc-cluster sub add air --url-file ./sub.url            # 订阅地址同样放在文件里
cc-cluster sub test air --landing home-a               # 接上落地测整条链路，按延迟排序
cc-cluster node add hk --role transit --sub air --landing home-a    # 自动取能接通的里延迟最低的

# 3. 建网关和槽位
cc-cluster gateway add main --transit hk
cc-cluster slot add alice --gateway main --landing home-a --memory 8g --cpus 4

# 4. 启动：对齐网关 → 检查链路 → 探测出口 → 启动 worker → 接线 → 从 worker 里复测出口
cc-cluster slot up alice
cc-cluster status

# 5. 在项目目录里进去干活
cd ~/projects/foo
cc-cluster claude alice        # 首次会询问是否在槽内安装 Claude Code
```

`slot up` 打印出的出口地址应当是你的落地。两次探测结果不一致，或者与落地登记的预期出口不符，槽位不会被接上线。

**不要中转**：`cc-cluster gateway add direct-gw`（不带 `--transit`），槽位直连各自的落地。

**其他协议的节点**：`node add … --file 片段.json`，内容是 Mihomo 原生的 proxy 片段（JSON 对象）。

## 日常使用

```sh
cc-cluster enter alice [目录]      # 进入槽位的 shell；目录默认是当前目录，不在挂载范围内时询问并挂载
cc-cluster claude alice [目录]     # 同上，直接启动 Claude Code
cc-cluster status                  # 网关与各槽位的现状
cc-cluster slot stop alice         # 停掉 worker，HOME 与身份保留
cc-cluster slot up alice           # 再启动；worker 或网关重启过之后也要执行它来重新接线

cc-cluster slot hold alice --reason "出口异常"    # 立即断网并保持
cc-cluster slot release alice && cc-cluster slot up alice

cc-cluster slot rm alice           # 删除槽位；加 --purge 连 HOME 与身份一起删
```

槽内的路径与宿主相同：在 `/data/projects/foo` 下进入，槽内的当前目录也是它。

**持续检测**：`cc-cluster watch` 每隔几分钟测一遍各槽位的连通性和出口；出口地址变了就给那个槽位断网并保持，等你 `slot release` 后重新 `slot up`。网页界面开着时自动进行，`watch --once` 只测一遍。

**候选中转**：中转挂了可以自动换到你事先挑好的备用节点，出口不变。

```sh
cc-cluster gateway add main --transit hk --backup sg       # 登记时带候选
cc-cluster gateway set-transit main sg hk                  # 在线更换：第一个是主用的，其余是候选
```

`set-transit` 会先验证每个中转都能接通该网关各槽位的落地，接不通就不更换（`--skip-check` 跳过）。

## 出口的两种来源

两种情况规则不同，都是为了不让出口在你不知情时变掉：

| | 用自己的落地 | 直出（订阅节点直接当出口） |
| --- | --- | --- |
| 出口地址 | 你的落地 | 订阅里选定的那个节点，多人共用 |
| 落地 / 节点连不上 | **没网**，不会改从中转出去 | **没网** |
| 自动切换 | 只在你挑好的候选**中转**之间切，出口不变 | 不切换，要换只能手动 |
| 出口地址变了 | 持续检测发现后自动断网并保持 | 同左 |

直出的用法：

```sh
cc-cluster sub nodes air                                   # 看订阅里有哪些节点
cc-cluster slot add bob --direct "air:节点名" && cc-cluster slot up bob
```

## 网页界面与交互面板

```sh
cc-cluster web          # 打印一个带一次性令牌的本机地址，在浏览器里打开
cc-cluster              # 不带参数：终端里的交互面板，按编号选择
```

网页只监听 `127.0.0.1`。在别的机器上用，先做 SSH 端口转发：`ssh -L 7878:127.0.0.1:7878 <主机>`。进入槽位仍然要在终端里做，网页不提供终端。

需要较新的浏览器（Chrome 111、Firefox 128、Safari 16.4 及以上）。

## 宿主 tmux 桥接（可选）

需要让槽内的 Claude Code 驱动宿主上的程序（EDA、GDB 等）时，按槽位开启：

```sh
cc-cluster tmux alice --on                 # 或登记时 slot add … --tmux-bridge
cc-cluster slot up alice --recreate        # 重建 worker 后生效
cc-cluster claude alice                    # 启动时自动带上 tmux 这个 MCP 服务器
cc-cluster tmux alice                      # 在另一个终端接入同一个 tmux，观察或接管
cc-cluster tmux alice --kill               # 结束它，连同其中的程序
```

每个槽位在宿主上有一个专用的 tmux，槽内经 [tmux-mcp](https://github.com/bnomei/tmux-mcp) 操作它，默认不开放结束 session / window / pane 的工具（`--tools` 可改）。

> **经桥接执行的命令是宿主上的命令**：用的是你的权限和宿主的网络，不走该槽的出口，手动断网也拦不住。只给需要它的槽位开启。

宿主升级 tmux 后要重建 worker 镜像并 `slot up --recreate`（客户端和 server 版本必须一致）。macOS 上不可用。

## 命令速查

每个命令和子命令都可以加 `-h` 看全部参数。

| 命令 | 作用 |
| --- | --- |
| `init` / `doctor` / `version` | 初始化数据目录 / 检查运行环境 / 版本 |
| `sub add\|list\|nodes\|note\|test\|refresh\|rm` | Clash 格式的订阅 |
| `node add\|list\|note\|resolve\|report\|rm` | 代理节点（中转 / 落地）；`report` 重新查询落地出口的第三方评估 |
| `gateway add\|list\|note\|set-transit\|up\|down\|rm` | 网关容器 |
| `slot add\|list\|note\|up\|stop\|hold\|release\|rm\|exec\|enter\|mounts\|unmount` | 工作槽位 |
| `enter` / `claude` / `tmux` | 进入槽位 / 直接启动 Claude Code / 宿主 tmux 桥接 |
| `status` / `watch` / `web` / `panel` | 现状 / 持续检测 / 网页界面 / 交互面板 |
| `render <网关>` | 预览网关的核心配置（默认隐藏凭据） |
| `exit-report [on\|off]` | 启动槽位时是否顺带查询出口 IP 的第三方评估 |

`slot add` 常用的参数：`--memory 8g`、`--cpus 4`、`--mount 宿主目录[:容器路径[:ro]]`、`--tz Asia/Tokyo`、`--user`、`--hostname`、`--env 名=值`、`--fake-dmi`、`--fake-proc`。

**名称与备注**：订阅、节点、网关、槽位的名称（ID）只能用小写字母、数字和连字符，以字母或数字开头，最长 31 个字符。它会原样用在容器名、主机名、数据目录的文件名和核心配置里，所以只收这些地方都接受的字符；写了下划线或大写时，报错里会给出改好的写法。想用中文、下划线、空格，或者写一句说明，放在备注里：

```sh
cc-cluster node add kr-home-lg --role landing --note "韩国家宽_LG" …   # 登记时带上
cc-cluster node note kr-home-lg 韩国家宽 LG 备用                       # 之后修改；不给内容就清掉
```

备注最长 64 个字符，显示在各个 `list`、`status` 和网页界面里，只用于展示：随时可以改，不影响运行中的槽位。`sub`、`gateway`、`slot` 的用法相同。

## 要知道的几件事

- **重启后要重新接线。** worker 或网关容器重启后线不会自动接回，执行 `slot up`；在那之前该槽没有网络。
- **只代理 TCP 和 DNS。** UDP 和 ping 不通。
- **时区。** 落地没有设置时区时，首次启动按探测到的出口所在地设置，之后不再自动变动；拿不到时暂用 `America/Los_Angeles` 并提醒。也可以用 `node add --tz` 或 `slot add --tz` 指定。
- **域名解析在落地一侧。** 槽内解析域名得到的是 `100.64.0.0/10` 里的映射地址，真正的解析由落地去做，外部看到的解析器在落地所在地。
- **出口评估。** 启动槽位时会经该槽的链路向第三方接口查一次出口 IP 的评估（机房 / 代理 / 滥用标记），只用于展示，取不到不影响使用。它们是各家数据库的看法，不代表任何具体服务的结论。不想自动查：`cc-cluster exit-report off`。
- **一个落地最多同时接 5 个槽位。**
- **共用网关的槽位一起受影响。** 网关的核心崩溃时接在它上面的槽位一起断网，监督进程会把它拉起来，之后各槽 `slot up`。
- **Claude Code 装在槽位自己的 HOME 里**，随 HOME 持久化；安装与更新的访问都从该槽的出口发出。
- **硬件信息。** 槽内没有 `/.dockerenv`，DMI 默认被遮成空的。`--fake-dmi` 与 `--fake-proc` 可以另外覆盖机型和 `/proc` 里的 CPU、内存显示（`/proc/stat` 仍是宿主的核数）。
- **凭据不进命令行。** 订阅地址、节点密码都从文件读；预览配置时默认隐藏。数据目录的权限是 0700，里面有这些凭据，注意备份和保管。

## 验证过什么，没验证过什么

**验证过：**

- 单元测试，以及全程使用假节点、不访问外部地址的端到端测试（18 组）：各槽位从各自的出口出网且互不可达、DNS 接管、核心被杀后不可达并自行恢复、手动断网、候选中转自动切换、出口变化后自动断网、直出等。每次推送在 GitHub Actions 上运行。
- 在一台机器（RHEL 8.6，Docker 29）上用真实的机场订阅和家宽落地跑通过完整链路：出口是落地、解析器在落地一侧、时区自动设置、候选中转改用后出口不变、直出槽位各自从固定节点出去。
- 在槽内安装 Claude Code、经第三方接口调用、宿主 tmux 桥接，实际运行过。

**没有验证：**

- **用 Anthropic 账号登录和登录后的使用。** “登录时浏览器的出口与槽位出口对齐”也没有实现：OAuth 那一步用的是你打开链接的那个浏览器的网络。
- RHEL 8.6 以外的系统；macOS 的二进制只确认过能编译出来，没有运行过。
- 真实中转在使用途中失效、真实出口中途变化这两种情况，只在假节点上发生过。
- 网页界面只经过类型检查、构建和接口测试，没有做过系统的交互测试。
- 开启了 SELinux 容器标签的宿主。

## 从源码构建与开发

需要 Go 1.23 以上；网页界面另外需要 Node.js 24 与 pnpm。

```sh
make check          # go vet、gofmt 检查、单元测试；不需要 Docker
make build          # bin/cc-cluster（没有执行过 make web 时不带网页界面）
make release        # make web + make build：带网页界面的单个二进制
make images         # 网关镜像与 worker 镜像
make e2e            # 端到端测试：需要 Docker 与两个镜像，只用 internal 网络和假节点
make dist           # 各平台的发布包与校验和
make repo-check     # 检查有没有把订阅地址、代理密码、数据目录带进版本库
```

| 路径 | 内容 |
| --- | --- |
| `cmd/cc-cluster` | 程序入口 |
| `internal/` | 配置模型、文件状态、编址、核心配置生成、订阅解析、Docker API 客户端、编排、命令行与网页服务 |
| `web/` | 网页界面的前端源码（React），构建后嵌入二进制 |
| `images/` | 网关镜像（固定版本的 Mihomo 与监督脚本）和默认的 worker 镜像 |
| `test/e2e` | 端到端测试与假服务 |
| `scripts/` | 辅助脚本：`web.sh` 构建并启动网页界面，`ci/check-repo.sh` 仓库卫生检查 |

各部分怎么配合见[架构说明](ARCHITECTURE.md)。

开发在 `dev` 分支，`master` 只接收它的快进；PR 请提到 `dev`。推到 `master` 会更新 `nightly`，推 `v*` 标签出正式版。

## 许可与致谢

本项目以 [MIT 许可](LICENSE)发布。

- 网页界面的风格、基础组件和页面骨架取自 [Claude-Hydra](https://github.com/jin-yu-liu/Claude-Hydra)（MIT），订阅导入的做法也参考了它；取用的文件、改动和许可全文见 [web/NOTICE.md](web/NOTICE.md)。
- 网关核心是 [Mihomo](https://github.com/MetaCubeX/mihomo)。
- 宿主桥接使用 [tmux-mcp](https://github.com/bnomei/tmux-mcp)（MIT），由 worker 镜像经 npm 安装；镜像里的 [tmux](https://github.com/tmux/tmux)（ISC）在构建时从官方源码包编译。本仓库不含它们的代码。
- 默认 worker 镜像的软件包选择参考了 Claude Code 官方的开发容器，Dockerfile 为重新编写。

发布包的 `licenses/` 目录里附有随二进制分发的第三方代码的许可说明。
