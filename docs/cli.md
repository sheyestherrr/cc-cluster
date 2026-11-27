# 命令行

网页界面的全部功能均可通过命令行完成；进入槽位只能在命令行中进行。各命令与子命令均可加 `-h` 查看全部参数。

数据目录默认为 `~/.cc-cluster`，可用环境变量 `CC_CLUSTER_DATA` 或参数 `--data-dir 目录` 指定其他目录。

- [快速开始](#快速开始)
- [订阅](#订阅)
- [日常使用](#日常使用)
- [出口的两种来源](#出口的两种来源)
- [命令速查](#命令速查)
- [名称与备注](#名称与备注)

## 快速开始

以下假设已有一个落地（SOCKS5 或 HTTP 代理）和一份订阅（Clash 格式或通用的 base64 订阅）。没有落地时见[直出](#出口的两种来源)；没有订阅时可不使用中转。

```sh
# 1. 添加落地。密码保存在文件中，不出现在命令行参数中
cc-cluster node add home-jp --role landing --type socks5 \
    --server 198.51.100.20 --port 1080 --username u --password-file ./pw.txt

# 2. 导入订阅，选择一个可连通落地的节点作为中转
cc-cluster sub add air --url-file ./sub.url            # 订阅地址同样保存在文件中
cc-cluster sub test air --landing home-jp              # 经落地测试整条链路，按延迟排序
cc-cluster node add hk --role transit --sub air --landing home-jp   # 自动选用可连通落地且延迟最低的节点

# 3. 创建网关与槽位
cc-cluster gateway add main --transit hk
cc-cluster slot add work --gateway main --landing home-jp --memory 8g --cpus 4

# 4. 启动：应用网关配置 → 检查链路 → 探测出口 → 启动 worker → 接线 → 在 worker 侧复测出口
cc-cluster slot up work
cc-cluster status

# 5. 在项目目录中进入槽位
cd ~/projects/foo
cc-cluster claude work         # 首次使用时询问是否在槽内安装 Claude Code
```

`slot up` 输出的出口地址应为落地的地址。两次探测结果不一致，或与落地设置的预期出口不符时，槽位不会接线。

**不使用中转**：`cc-cluster gateway add direct-gw`（不带 `--transit`），槽位直连各自的落地。

**其他协议的节点**：`node add … --file 片段.json`，内容为 Mihomo 原生的 proxy 片段（JSON 对象）。

## 订阅

订阅提供一批节点；从中选择并添加的节点才供槽位使用。订阅有两种来源：

```sh
cc-cluster sub add air --url-file ./sub.url     # 远程订阅：提供地址，之后可刷新
cc-cluster sub add mine --file ./links.txt      # 本地订阅：直接提供内容；--file - 从标准输入读取

cc-cluster sub set air --every 12h              # 远程订阅每 12 小时自动刷新；--every off 关闭
cc-cluster sub set mine --file ./more.txt --append    # 向本地订阅追加内容
cc-cluster sub set air --url-file ./new.url     # 更换地址；已添加的节点按名称或配置匹配后随之更新
```

- 内容可以是 Clash（Mihomo）配置，也可以是每行一个的分享链接或其 base64 编码：`ss://`、`vmess://`、`vless://`、`trojan://`、`hysteria2://`、`socks5://`、`http(s)://`。无法识别的行将跳过，并给出行数；暂不支持 sing-box 的 JSON 配置。
- 订阅地址与本地订阅的内容只保存在数据目录中（权限 0600），保存后不再显示，也不写入操作记录。
- 自动刷新在 `cc-cluster web` 或 `cc-cluster watch` 运行期间进行，随持续检测的周期执行；间隔最短 10 分钟。刷新失败时节点保持不变，原因显示在 `sub list` 的状态一栏。
- 订阅中的节点仅名称变更而配置未变时，取自该节点的节点随之改名；配置有变更的节点在重新启动网关或槽位后生效。

## 日常使用

```sh
cc-cluster enter work [目录]       # 进入槽位的 shell；目录默认为当前目录，不在挂载范围内时询问是否挂载；当前目录不能挂载（例如主目录）时进入第一个挂载目录
cc-cluster claude work [目录]      # 同上，并直接启动 Claude Code
cc-cluster status                  # 网关与各槽位的状态
cc-cluster slot stop work          # 停止 worker，保留 HOME 与身份
cc-cluster slot up work            # 再次启动；worker 或网关重启后也需执行，以重新接线

cc-cluster slot hold work --reason "出口异常"     # 立即断网并保持
cc-cluster slot release work && cc-cluster slot up work

cc-cluster slot rm work            # 删除槽位；加 --purge 时同时删除 HOME 与身份
cc-cluster                         # 不带参数：列出槽位，选择后启动 Claude Code 或打开终端；保存每个目录上次的选择
cc-cluster panel                   # 交互式菜单
```

槽内的路径与宿主相同：在 `/data/projects/foo` 下进入，槽内的当前目录也是该路径。

**持续检测**：`cc-cluster watch` 每隔数分钟检测各槽位的连通性与出口；出口地址变更时将该槽位断网并保持，需执行 `slot release` 后重新 `slot up`。网页界面运行期间自动进行；`watch --once` 仅检测一次。

**流量与连接**：`cc-cluster traffic` 列出各槽位的当前速率与最近 24 小时的流量；`cc-cluster traffic work` 列出该槽位的当前连接与这段时间内访问过的目标。

- 只显示连接层的信息：目标的域名与端口、上下行流量、时长。HTTPS 的路径与内容已加密，无法查看，也不做解密。
- 历史（每五分钟一段的流量、持续检测的结果、已结束的连接）在 `cc-cluster web` 或 `cc-cluster watch` 运行期间记录，保存在数据目录的 `history/` 下，保留 30 天。两者均未运行的时段没有记录。网页界面默认在后台运行；不使用网页界面时，请保持 `cc-cluster watch` 运行。
- 连接每十秒采样一次，持续时间更短的连接可能未被记录；流量取自网卡的计数，不受此影响。
- `history/` 中包含各槽位访问过的域名，文件仅当前用户可读；不需要保留时可删除该目录。

**备用中转**：主用中转不可用时，自动切换到预先指定的备用中转，出口不变。

```sh
cc-cluster gateway add main --transit hk --backup sg       # 创建时指定备用中转
cc-cluster gateway set-transit main sg hk                  # 在线更换：第一个为主用，其余为备用
```

`set-transit` 先验证每个中转均可连通该网关各槽位的落地，无法连通时不更换（`--skip-check` 跳过验证）。

## 出口的两种来源

两种来源的规则不同，目的均为避免出口自行变更：

| | 使用落地 | 直出（以订阅节点为出口） |
| --- | --- | --- |
| 出口地址 | 落地的地址 | 订阅中选定的节点，通常多人共用 |
| 落地 / 节点无法连接 | **断网**，不会改由中转直接出站 | **断网** |
| 自动切换 | 仅在指定的备用**中转**之间切换，出口不变 | 不切换，只能手动更换 |
| 出口地址变更 | 持续检测发现后自动断网并保持 | 同左 |

直出的用法：

```sh
cc-cluster sub nodes air                                   # 查看订阅中的节点
cc-cluster slot add research --direct "air:节点名" && cc-cluster slot up research
```

## 命令速查

| 命令 | 作用 |
| --- | --- |
| `init` / `doctor` / `version` | 初始化数据目录 / 检查运行环境 / 显示版本 |
| `sub add\|list\|nodes\|note\|set\|test\|refresh\|rm` | 订阅：远程订阅地址，或本地的订阅内容与分享链接；`set` 修改来源与自动刷新 |
| `node add\|list\|note\|resolve\|report\|rm` | 代理节点（中转 / 落地）；`report` 重新查询落地出口的第三方评估 |
| `gateway add\|list\|note\|set-transit\|up\|down\|rm` | 网关 |
| `slot add\|list\|note\|up\|stop\|hold\|release\|rm\|exec\|enter\|mounts\|unmount` | 槽位 |
| `enter` / `claude` / `tmux` | 进入槽位 / 直接启动 Claude Code / [宿主 tmux 桥接](tmux-bridge.md) |
| `status` / `traffic` / `watch` / `web` / `panel` | 状态 / 流量与连接 / 持续检测 / 网页界面 / 交互式菜单 |
| `render <网关>` | 预览网关的核心配置（默认隐藏凭据） |
| `exit-report [on\|off]` | 启动槽位时是否查询出口 IP 的第三方评估 |
| `images [--gateway-image 镜像] [--worker-image 镜像]` | 查看或更换网关镜像与默认的 worker 镜像；运行中的容器重建后生效 |
| `update [--check] [--version 版本] [--images]` | 更新 `cc-cluster` 至最新或指定的版本 |

`init` 的参数：`--gateway-image`、`--worker-image` 指定两个镜像（发布版默认为与自身版本相同的发布镜像，源码构建的版本默认为本机构建的 `cc-cluster-gateway:dev` 与 `cc-cluster-worker:dev`）；`--name` 为容器名前缀，默认为 `cc-<用户名>`，容器名为 `cc-<用户名>-w-<槽位>`、`cc-<用户名>-gw-<网关>`，同一台机器上的多个用户不会重名；同一用户使用多个数据目录时，请为每个目录指定不同的前缀。

`web`：默认在后台启动网页界面并输出地址，日志写入数据目录的 `web.log`；同一数据目录只运行一个实例，重复执行只输出现有的地址；二进制更新后再次执行，会将运行中的旧版本重启为新版本（沿用原来的端口与选项），以 `--foreground` 运行的除外。`web stop` 停止；`--foreground` 在前台运行（由 systemd 等管理时使用）。`--port` 指定端口（默认 7878；未指定且被占用时自动改用后面的空闲端口，指定后只使用该端口）。访问令牌保存在数据目录的 `web.token` 中，固定不变；需要更换时删除该文件并重启。在终端中用 `enter`、`claude` 或不带参数的 `cc-cluster` 进入槽位时，网页界面未运行则自动启动。

`update`：从 GitHub Releases 下载与本机系统和架构对应的安装包，验证 `SHA256SUMS` 中的校验和，解出其中的 `cc-cluster` 并运行 `version` 确认版本，然后原子替换当前的二进制文件。任一步骤失败，原文件保持不变。更新只替换这一个文件：槽位、节点、订阅与设置不变，运行中的容器不受影响。后台运行的网页界面会自动重启为新版本，端口、令牌与启动选项不变；以 `--foreground` 运行的（例如由 systemd 管理）需手动重启。

- `--check`：仅显示当前版本、最新版本与更新说明，不安装。不能与 `--version` 同时使用。
- `--version v0.0.3`：安装指定版本，可用于回退到旧版本。
- `--images`：同时将镜像设置更新至该版本。两个镜像均拉取成功后才修改设置；只更新本项目发布的镜像，自定义的镜像不变。更新后，运行中的槽位显示“配置待应用”，其持续检测暂停，将网关停止后重新启动并重建 worker 后恢复，建议在槽位空闲时进行。不带此参数时不修改镜像设置；镜像版本低于 `cc-cluster` 的版本时给出提示。

源码构建的二进制不支持自动更新（`update --check` 仍可用）。二进制所在的目录需可写；安装在系统目录时，请以有权限的用户执行。网页界面每 6 小时检查一次新版本，结果只保存在内存中；启动网页界面时设置环境变量 `CC_CLUSTER_NO_UPDATE_CHECK=1` 可关闭自动检查，此时只在点击“检查更新”时检查。

`slot add` 的常用参数：`--memory 8g`、`--cpus 4`、`--mount 宿主目录[:容器路径[:ro]]`、`--tz Asia/Tokyo`、`--user`（槽内的用户名，默认为 `user`）、`--hostname`、`--env 名=值`、`--fake-dmi`、`--fake-proc`。

挂载的限制：不能挂载主目录、数据目录，或包含它们的上级目录。每个挂载在槽内的 `~` 下有同名的快捷方式；`~` 中已有同名文件时不创建。

## 名称与备注

订阅、节点、网关、槽位的名称（ID）只能包含小写字母、数字和连字符，以字母或数字开头，最长 31 个字符。名称直接用于容器名、主机名、数据目录的文件名和核心配置，因此只接受这些位置均支持的字符；包含下划线或大写字母时，报错中给出可用的写法。中文、下划线、空格或说明文字请写在备注中：

```sh
cc-cluster node add kr-home-lg --role landing --note "韩国家宽_LG" …   # 添加时指定
cc-cluster node note kr-home-lg 韩国家宽 LG 备用                       # 之后修改；不带内容时清除备注
```

备注最长 64 个字符，显示在各 `list`、`status` 和网页界面中，仅用于展示：可随时修改，不影响运行中的槽位。`sub`、`gateway`、`slot` 的用法相同。
