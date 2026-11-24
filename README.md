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

### 2. 拉取两个镜像

```sh
# 标签与你下载的版本一致：nightly，或正式版的版本号
docker pull ghcr.io/sheyestherrr/cc-cluster-gateway:nightly
docker pull ghcr.io/sheyestherrr/cc-cluster-worker:nightly
```

不想用发布的镜像，或者要用[宿主 tmux 桥接](#宿主-tmux-桥接可选)，可以在本机构建（只需要 docker 和 make）：

```sh
git clone https://github.com/sheyestherrr/cc-cluster.git
make -C cc-cluster image-gateway       # 得到 cc-cluster-gateway:dev
make -C cc-cluster image-worker        # 得到 cc-cluster-worker:dev，里面的 tmux 按这台机器的 tmux 版本编译
```

### 3. 初始化

数据目录（槽位的 HOME、身份、节点配置都在里面）必须显式指定：

```sh
export CC_CLUSTER_DATA=~/cc-data        # 或者每次加 --data-dir

cc-cluster init \
  --gateway-image ghcr.io/sheyestherrr/cc-cluster-gateway:nightly \
  --worker-image ghcr.io/sheyestherrr/cc-cluster-worker:nightly
cc-cluster doctor                       # 检查这台机器能否运行；不访问外部地址
```

`doctor` 全部通过就可以往下走了。用本机构建的镜像时不用带这两个参数，默认就是 `cc-cluster-gateway:dev` 和 `cc-cluster-worker:dev`。

## 五分钟上手

下面假设你有一个自己的落地（SOCKS5 或 HTTP 代理）和一份机场订阅（Clash 格式或通用的 base64 订阅都可以）。没有落地的看[直出](#出口的两种来源)，没有订阅的可以不要中转。

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

### 订阅的来源

订阅是一批现成的节点，从里面挑出来登记的才是槽位用的节点。来源有两种：

```sh
cc-cluster sub add air --url-file ./sub.url     # 远程订阅：给地址，之后可以刷新
cc-cluster sub add mine --file ./links.txt      # 本地订阅：直接给内容；--file - 从标准输入读

cc-cluster sub set air --every 12h              # 远程订阅每 12 小时自动刷新一次；--every off 关掉
cc-cluster sub set mine --file ./more.txt --append    # 往本地订阅后面追加几条
cc-cluster sub set air --url-file ./new.url     # 换地址；已登记的节点按名称或配置对上后跟着更新
```

- 内容可以是 Clash（Mihomo）配置，也可以是一行一个的分享链接或它们的 base64：`ss://`、`vmess://`、`vless://`、`trojan://`、`hysteria2://`、`socks5://`、`http(s)://`。认不出来的行会跳过并报出行数；sing-box 的 JSON 配置暂不支持。
- 订阅地址和贴进来的内容只保存在数据目录里（权限 0600），保存之后不再显示，也不进操作记录。
- 自动刷新在 `cc-cluster web` 或 `cc-cluster watch` 运行期间进行，跟着持续检测的节奏；间隔最短 10 分钟。刷新失败时节点保持原样，原因记在 `sub list` 的状态一栏。
- 订阅里的节点改了名但配置没变时，取自它的节点会跟着改名；配置变了的节点在重新启动网关或槽位后生效。

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

**流量与连接**：`cc-cluster traffic` 列出各槽位此刻的速率和最近 24 小时的流量；`cc-cluster traffic alice` 列出它当前的连接和这段时间里连过的目标。

- 能看到的是连接这一层：目标的域名与端口、上下行流量、时长。HTTPS 的网址路径和内容是加密的，这里看不到，也不做解密。
- 历史（五分钟一档的流量、持续检测的结果、已结束的连接）在 `cc-cluster web` 或 `cc-cluster watch` 运行期间记录，存在数据目录的 `history/` 下，保留 30 天。两个都没开的时段没有曲线；想要完整的历史就让 `cc-cluster watch` 常驻。
- 连接靠十秒一次的快照记录，比这更短的连接可能不在其中；流量来自网卡的计数，不受这个影响。
- `history/` 里有各槽位访问过的域名，文件只有当前用户可读；不想留就删掉这个目录。

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

网页有五页：**总览**（接线的槽位、此刻与最近 24 小时 / 7 天 / 30 天的流量、各槽位的出口、持续检测的时间线、需要留意的事、订阅节点的延迟分布、最近的操作）、**槽位**（详情里有它的流量、当前的连接和连过的目标）、**节点与网关**、**订阅**（添加远程或本地订阅，按地区和协议筛选节点、测延迟，把选中的登记为中转或落地）和**系统**。节点行首的国旗取自落地出口的评估结果；没有评估时从节点名称里认，认不出来就显示通用图标。

需要较新的浏览器（Chrome 111、Firefox 128、Safari 16.4 及以上）。

## 宿主 tmux 桥接（可选）

需要让槽内的 Claude Code 驱动宿主上的程序（EDA、GDB 等）时，按槽位开启。

**先准备镜像。** 槽内的 tmux 只是客户端，要去连宿主上的 tmux，两边的版本必须相同，否则连不上。发布的 worker 镜像里是 Debian 自带的那一版（`docker run --rm <镜像> tmux -V` 可以看），和宿主的 `tmux -V` 不一样时，在本机构建一个（见[安装](#2-拉取两个镜像)），并让要用桥接的槽位使用它：

```sh
make -C cc-cluster image-worker                                   # 按宿主的 tmux 版本编译，得到 cc-cluster-worker:dev
cc-cluster slot add alice … --image cc-cluster-worker:dev --tmux-bridge
```

槽位的镜像在登记时确定。已有的槽位要换镜像：`slot rm`（不加 `--purge`，HOME 与身份保留）后用同一个名字重新 `slot add --image …`。

**开启与使用：**

```sh
cc-cluster tmux alice --on                 # 或登记时 slot add … --tmux-bridge
cc-cluster slot up alice --recreate        # 重建 worker 后生效
cc-cluster claude alice                    # 启动时自动带上 tmux 这个 MCP 服务器
cc-cluster tmux alice                      # 在另一个终端接入同一个 tmux，观察或接管
cc-cluster tmux alice --kill               # 结束它，连同其中的程序
```

每个槽位在宿主上有一个专用的 tmux，槽内经 [tmux-mcp](https://github.com/bnomei/tmux-mcp) 操作它，默认不开放结束 session / window / pane 的工具（`--tools` 可改）。

> **经桥接执行的命令是宿主上的命令**：用的是你的权限和宿主的网络，不走该槽的出口，手动断网也拦不住。只给需要它的槽位开启。

宿主升级 tmux 后要重新 `make image-worker` 并 `slot up --recreate`（客户端和 server 版本必须一致）。macOS 上不可用。

## 命令速查

每个命令和子命令都可以加 `-h` 看全部参数。

| 命令 | 作用 |
| --- | --- |
| `init` / `doctor` / `version` | 初始化数据目录 / 检查运行环境 / 版本 |
| `sub add\|list\|nodes\|note\|set\|test\|refresh\|rm` | 订阅：远程的订阅地址，或贴进来的订阅内容与分享链接；`set` 改来源和自动刷新 |
| `node add\|list\|note\|resolve\|report\|rm` | 代理节点（中转 / 落地）；`report` 重新查询落地出口的第三方评估 |
| `gateway add\|list\|note\|set-transit\|up\|down\|rm` | 网关容器 |
| `slot add\|list\|note\|up\|stop\|hold\|release\|rm\|exec\|enter\|mounts\|unmount` | 工作槽位 |
| `enter` / `claude` / `tmux` | 进入槽位 / 直接启动 Claude Code / 宿主 tmux 桥接 |
| `status` / `traffic` / `watch` / `web` / `panel` | 现状 / 流量与连接 / 持续检测 / 网页界面 / 交互面板 |
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
- **登录用的是你自己的浏览器。** 在槽内登录 Claude Code 时，授权那一步在你打开链接的浏览器里完成，走的是那个浏览器所在机器的网络，不经过槽位的出口；之后 Claude Code 自己的访问都从该槽的出口发出。
- **硬件信息。** 槽内没有 `/.dockerenv`，DMI 默认被遮成空的。`--fake-dmi` 与 `--fake-proc` 可以另外覆盖机型和 `/proc` 里的 CPU、内存显示（`/proc/stat` 仍是宿主的核数）。
- **凭据不进命令行。** 订阅地址、节点密码都从文件读；预览配置时默认隐藏。数据目录的权限是 0700，里面有这些凭据，注意备份和保管。
- **运行过的环境。** 在 RHEL 8.6 和 Ubuntu 上用真实的订阅与家宽落地实际使用过，包括在槽内安装 Claude Code、用账号登录后日常使用、宿主 tmux 桥接。macOS 的二进制只确认过能编译出来，没有运行过；开启了 SELinux 容器标签的宿主没有试过。每次推送还会在 GitHub Actions 上跑单元测试和只用假节点的端到端测试。

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
- 分享链接的解析改写自 [Resin](https://github.com/Resinat/Resin)（MIT）的订阅解析器，订阅页与总览页的信息安排也参考了它；取用的范围、改动和许可全文见 [internal/subscription/NOTICE.md](internal/subscription/NOTICE.md)。国旗图形来自 [country-flag-icons](https://gitlab.com/catamphetamine/country-flag-icons)（MIT），图表用 [recharts](https://github.com/recharts/recharts)（MIT）。
- 网关核心是 [Mihomo](https://github.com/MetaCubeX/mihomo)。
- 宿主桥接使用 [tmux-mcp](https://github.com/bnomei/tmux-mcp)（MIT），由 worker 镜像经 npm 安装；镜像里的 [tmux](https://github.com/tmux/tmux)（ISC）来自发行版的软件包，本机构建时改为从官方源码包编译。本仓库不含它们的代码。
- 默认 worker 镜像的软件包选择参考了 Claude Code 官方的开发容器，Dockerfile 为重新编写。

发布包的 `licenses/` 目录里附有随二进制分发的第三方代码的许可说明。
