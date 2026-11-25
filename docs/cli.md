# 命令行

网页界面能做的事，命令行都能做；进入槽位只能在命令行里做。每个命令和子命令都可以加 `-h` 看全部参数。

数据目录默认是 `~/.cc-cluster`。要换成别的，设置环境变量 `CC_CLUSTER_DATA`，或者每次加 `--data-dir 目录`。

- [从零开始](#从零开始)
- [订阅](#订阅)
- [日常使用](#日常使用)
- [出口的两种来源](#出口的两种来源)
- [命令速查](#命令速查)
- [名称与备注](#名称与备注)

## 从零开始

下面假设你有一个自己的落地（SOCKS5 或 HTTP 代理）和一份机场订阅（Clash 格式或通用的 base64 订阅都可以）。没有落地的看[直出](#出口的两种来源)，没有订阅的可以不要中转。

```sh
# 1. 登记落地。密码放在文件里，不出现在命令行上
cc-cluster node add home-jp --role landing --type socks5 \
    --server 198.51.100.20 --port 1080 --username u --password-file ./pw.txt

# 2. 导入订阅，挑一个能接通落地的节点当中转
cc-cluster sub add air --url-file ./sub.url            # 订阅地址同样放在文件里
cc-cluster sub test air --landing home-jp              # 接上落地测整条链路，按延迟排序
cc-cluster node add hk --role transit --sub air --landing home-jp   # 自动取能接通的里延迟最低的

# 3. 建网关和槽位
cc-cluster gateway add main --transit hk
cc-cluster slot add work --gateway main --landing home-jp --memory 8g --cpus 4

# 4. 启动：对齐网关 → 检查链路 → 探测出口 → 启动 worker → 接线 → 从 worker 里复测出口
cc-cluster slot up work
cc-cluster status

# 5. 在项目目录里进去干活
cd ~/projects/foo
cc-cluster claude work         # 首次会询问是否在槽内安装 Claude Code
```

`slot up` 打印出的出口地址应当是你的落地。两次探测结果不一致，或者与落地登记的预期出口不符，槽位不会被接上线。

**不要中转**：`cc-cluster gateway add direct-gw`（不带 `--transit`），槽位直连各自的落地。

**其他协议的节点**：`node add … --file 片段.json`，内容是 Mihomo 原生的 proxy 片段（JSON 对象）。

## 订阅

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
cc-cluster enter work [目录]       # 进入槽位的 shell；目录默认是当前目录，不在挂载范围内时询问并挂载；当前目录挂不了（比如就在主目录）时进第一个挂载目录
cc-cluster claude work [目录]      # 同上，直接启动 Claude Code
cc-cluster status                  # 网关与各槽位的现状
cc-cluster slot stop work          # 停掉 worker，HOME 与身份保留
cc-cluster slot up work            # 再启动；worker 或网关重启过之后也要执行它来重新接线

cc-cluster slot hold work --reason "出口异常"     # 立即断网并保持
cc-cluster slot release work && cc-cluster slot up work

cc-cluster slot rm work            # 删除槽位；加 --purge 连 HOME 与身份一起删
cc-cluster                         # 不带参数：列出槽位，选一个进去启动 Claude Code 或打开终端；记得每个目录上次的选择
cc-cluster panel                   # 文字菜单的交互面板
```

槽内的路径与宿主相同：在 `/data/projects/foo` 下进入，槽内的当前目录也是它。

**持续检测**：`cc-cluster watch` 每隔几分钟测一遍各槽位的连通性和出口；出口地址变了就给那个槽位断网并保持，等你 `slot release` 后重新 `slot up`。网页界面开着时自动进行，`watch --once` 只测一遍。

**流量与连接**：`cc-cluster traffic` 列出各槽位此刻的速率和最近 24 小时的流量；`cc-cluster traffic work` 列出它当前的连接和这段时间里连过的目标。

- 能看到的是连接这一层：目标的域名与端口、上下行流量、时长。HTTPS 的网址路径和内容是加密的，这里看不到，也不做解密。
- 历史（五分钟一档的流量、持续检测的结果、已结束的连接）在 `cc-cluster web` 或 `cc-cluster watch` 运行期间记录，存在数据目录的 `history/` 下，保留 30 天。两个都没开的时段没有曲线。网页界面默认常驻后台，不用网页界面的机器让 `cc-cluster watch` 常驻。
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
cc-cluster slot add research --direct "air:节点名" && cc-cluster slot up research
```

## 命令速查

| 命令 | 作用 |
| --- | --- |
| `init` / `doctor` / `version` | 初始化数据目录 / 检查运行环境 / 版本 |
| `sub add\|list\|nodes\|note\|set\|test\|refresh\|rm` | 订阅：远程的订阅地址，或贴进来的订阅内容与分享链接；`set` 改来源和自动刷新 |
| `node add\|list\|note\|resolve\|report\|rm` | 代理节点（中转 / 落地）；`report` 重新查询落地出口的第三方评估 |
| `gateway add\|list\|note\|set-transit\|up\|down\|rm` | 网关容器 |
| `slot add\|list\|note\|up\|stop\|hold\|release\|rm\|exec\|enter\|mounts\|unmount` | 工作槽位 |
| `enter` / `claude` / `tmux` | 进入槽位 / 直接启动 Claude Code / [宿主 tmux 桥接](tmux-bridge.md) |
| `status` / `traffic` / `watch` / `web` / `panel` | 现状 / 流量与连接 / 持续检测 / 网页界面 / 交互面板 |
| `render <网关>` | 预览网关的核心配置（默认隐藏凭据） |
| `exit-report [on\|off]` | 启动槽位时是否顺带查询出口 IP 的第三方评估 |
| `images [--gateway-image 镜像] [--worker-image 镜像]` | 查看或更换网关镜像与默认的 worker 镜像；运行中的容器重建后才用上新的 |

`init` 的参数：`--gateway-image`、`--worker-image` 指定两个镜像（发布版默认是与自己同版本的发布镜像，从源码构建的默认是本机构建的 `cc-cluster-gateway:dev` 和 `cc-cluster-worker:dev`），`--name` 是容器名前缀，默认是 `cc-<你的用户名>`，所以容器叫 `cc-<用户名>-w-<槽位>`、`cc-<用户名>-gw-<网关>`，同一台机器上的多个用户互不重名；一个用户用多个数据目录时各给一个不同的前缀。

`web`：默认把网页界面放到后台并打印地址，输出写在数据目录的 `web.log`；同一个数据目录只会有一个在运行，重复执行只打印现有的地址，二进制升级后再执行会把在跑的旧版本换掉。`web stop` 停止它；`--foreground` 在前台运行（交给 systemd 之类管理时用）。`--port` 指定端口（默认 7878，不指定时被占用会自动往后找空闲的；指定了就只用这一个）。访问令牌存在数据目录的 `web.token` 里，固定不变；想换掉就删除这个文件再重启。在终端里用 `enter`、`claude` 或不带参数的 `cc-cluster` 进入槽位时，如果它没在运行会顺手启动。

`slot add` 常用的参数：`--memory 8g`、`--cpus 4`、`--mount 宿主目录[:容器路径[:ro]]`、`--tz Asia/Tokyo`、`--user`（槽内的用户名，默认 `user`）、`--hostname`、`--env 名=值`、`--fake-dmi`、`--fake-proc`。

挂载的限制：不能挂你的主目录、数据目录，或包住它们的上级目录。每个挂载在槽内的 `~` 下有一个同名的快捷方式；`~` 里已有同名的文件时不建。

## 名称与备注

订阅、节点、网关、槽位的名称（ID）只能用小写字母、数字和连字符，以字母或数字开头，最长 31 个字符。它会原样用在容器名、主机名、数据目录的文件名和核心配置里，所以只收这些地方都接受的字符；写了下划线或大写时，报错里会给出改好的写法。想用中文、下划线、空格，或者写一句说明，放在备注里：

```sh
cc-cluster node add kr-home-lg --role landing --note "韩国家宽_LG" …   # 登记时带上
cc-cluster node note kr-home-lg 韩国家宽 LG 备用                       # 之后修改；不给内容就清掉
```

备注最长 64 个字符，显示在各个 `list`、`status` 和网页界面里，只用于展示：随时可以改，不影响运行中的槽位。`sub`、`gateway`、`slot` 的用法相同。
