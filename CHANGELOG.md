# 更新记录 / Changelog

每个版本一段，新的在上面。版本号是 `v主.次.修订`；预发布写成 `v0.1.0-alpha.1`、`v1.0.0-rc.1`。

One section per release, newest first. Versions follow `vMAJOR.MINOR.PATCH`; pre-releases look like `v0.1.0-alpha.1` or `v1.0.0-rc.1`.

## v0.0.2

日常用法收成两条命令：`cc-cluster web` 配置，在项目目录里执行 `cc-cluster` 进槽位干活。

- **`cc-cluster web` 默认在后台运行**：打印地址后立即返回，关掉终端不会停；重复执行只打印同一个地址，升级后再执行会换掉在跑的旧版本。`cc-cluster web stop` 停止，`--foreground` 保留原来的前台方式。
- **访问令牌固定**：存在数据目录里，重启不再变化；浏览器长期记住，用带令牌的地址打开一次之后直接输地址就能进。没有令牌时页面会说明怎么取得链接。
- **不带参数的 `cc-cluster` 改为选槽位**：列出槽位，选一个进去启动 Claude Code 或打开终端，并记住每个目录上次的选择。原来的文字菜单改用 `cc-cluster panel` 打开。
- 在终端里进入槽位时，后台的网页界面（连同持续检测）没在运行会顺手启动。
- 网页界面：各页的标题加粗；侧栏左上角只留放大的 `cc-cluster`，去掉副标题和底部的“只在本机提供服务”；槽位详情里用户名与主机名分开显示。
- 升级后可以更换镜像：新增 `cc-cluster images` 和系统页的“更换镜像”；重新运行安装脚本会自动把数据目录里的发布镜像换成新版本的。运行中的容器重建后才用上新镜像。
- 发布版的 `cc-cluster init` 默认使用与自己同版本的发布镜像，不再是只有从源码构建才有的 `:dev`。

升级：重新运行安装命令。已有的网关停止再启动、槽位“重建 worker”之后才用上新镜像。

---

Everyday use is now two commands: `cc-cluster web` to configure, and `cc-cluster` in a project directory to enter a slot and work.

- **`cc-cluster web` now runs in the background by default**: it prints the address and returns, and survives closing the terminal. Running it again prints the same address; after an upgrade it replaces the old running version. Stop it with `cc-cluster web stop`; `--foreground` keeps the previous behaviour.
- **The access token is now fixed**: it is stored in the data directory and no longer changes on restart. The browser remembers it, so after opening the tokenised address once you can use the bare address. Without a token the page explains how to get the link.
- **Bare `cc-cluster` is now a slot picker**: it lists the slots, enters the chosen one to start Claude Code or a shell, and remembers the last choice per directory. The old text menu moved to `cc-cluster panel`.
- Entering a slot from a terminal starts the background web UI (and with it the continuous checks) if it is not running.
- Web UI: page titles are bold again; the sidebar header shows only a larger `cc-cluster`, without the tagline or the "local only" footer; the slot detail shows username and hostname separately.
- Images can be changed after an upgrade: a new `cc-cluster images` command and a "change images" action on the System page. Re-running the install script switches a data directory that uses the published images to the new version. Running containers pick up the new image once recreated.
- In release builds, `cc-cluster init` defaults to the published images of the same version instead of the source-build-only `:dev` tags.

To upgrade, run the install command again. Existing gateways use the new image after a stop and start, and slots after "重建 worker" (recreate worker).

## v0.0.1

第一个带版本号的发布。此前只有随 `master` 滚动的 `nightly` 构建，现已停用。

### 主要功能

- **槽位**：每个槽位是一个没有自己网络的 Docker 容器，有独立的 HOME、用户名、hostname、machine-id 和 MAC，流量只能经它绑定的网关出去。
- **固定出口**：网关容器里运行 Mihomo，按“中转 → 落地”的链路出网；也支持不带中转的直出槽位。DNS 用 fake-ip，域名在落地一侧解析。
- **持续检测**：定期检查各槽位的连通性与出口，出口变了就给该槽断网并保持。
- **订阅**：远程订阅与直接粘贴的本地订阅，支持 Clash 配置、base64 订阅和分享链接；可定时自动刷新，节点可测延迟。
- **网页界面**：`cc-cluster web` 打开只监听本机的界面，包含总览、槽位、网络（节点与网关）、订阅和系统几页；记录各槽位的流量、连接与检测历史。
- **命令行**：与网页等价的全部操作，另有交互面板、`enter` / `claude`（按当前目录进入槽位）和按槽位开启的宿主 tmux 桥接。
- **一键安装**：`scripts/install.sh` 下载二进制、核对校验和、拉取两个镜像并初始化数据目录。

### 相对此前 nightly 的变化

- 网页界面换成取自 [Resin](https://github.com/Resinat/Resin)（MIT）的版面与样式：卡片加详情页，只有浅色主题。
- 数据目录默认是 `~/.cc-cluster`，不必再设置 `CC_CLUSTER_DATA`。原来放在别处的，继续用这个环境变量或 `--data-dir` 指过去即可。
- `init` 的默认容器名前缀从 `cc` 改为 `cc-<用户名>`，同一台机器上多个用户的容器不再重名。已有的数据目录保持原来的前缀。
- 新建网关的出口网络默认是 `<前缀>-egress`，每个用户一个，不再共用 `cc-egress`。已有的网关不变。
- `web` 默认端口 7878 被占用时自动往后找空闲端口；同一个数据目录重复执行不会再开一个，只打印现有的地址。
- 槽内用户名默认是 `user`，不再随机挑选；新建槽位时可以改。已有槽位不变。
- 槽内的提示符改为两行：用户名、目录和时间，下一行输入。已有槽位在重建 worker 后生效。
- 每个挂载在槽内的 `~` 下有一个同名的快捷方式；不能再把整个主目录或数据目录挂进槽位（已有的挂载不受影响）。
- 操作记录用中文显示；命令行文档移到 `docs/`。
- 不再发布 `nightly` 构建和 `:nightly` 镜像；安装脚本默认安装最新的正式版。

### 已知的限制

- 各槽位共用宿主内核，内核版本、开机时长、CPU 型号等信息相同；本项目不承诺账号之间不被关联。

---

The first versioned release. Until now there was only a rolling `nightly` build tracking `master`, which is discontinued.

### Highlights

- **Slots**: each slot is a Docker container with no network of its own and a separate HOME, username, hostname, machine-id and MAC. Its traffic can only leave through the gateway it is bound to.
- **Fixed exits**: a gateway container runs Mihomo and routes through a "transit → landing" chain; slots that exit directly without a transit are supported too. DNS uses fake-ip, so names are resolved on the landing side.
- **Continuous checks**: connectivity and the exit address of every slot are checked periodically; if the exit changes, the slot is cut off and held.
- **Subscriptions**: remote subscriptions and pasted local ones, in Clash config, base64 or share-link form, with scheduled refresh and latency tests.
- **Web UI**: `cc-cluster web` serves a localhost-only interface with Overview, Slots, Network (nodes and gateways), Subscriptions and System pages, and records per-slot traffic, connections and check history.
- **CLI**: everything the web UI does, plus an interactive panel, `enter` / `claude` (enter a slot from the current directory) and an opt-in host tmux bridge per slot.
- **One-line install**: `scripts/install.sh` downloads the binary, verifies its checksum, pulls both images and initialises the data directory.

### Changes since the nightly builds

- The web UI now uses the layout and styles of [Resin](https://github.com/Resinat/Resin) (MIT): card grids with detail pages, light theme only.
- The data directory defaults to `~/.cc-cluster`; setting `CC_CLUSTER_DATA` is no longer required. If yours lives elsewhere, keep pointing at it with that variable or `--data-dir`.
- The default container-name prefix for `init` changed from `cc` to `cc-<username>`, so several users on one machine no longer collide. Existing data directories keep their prefix.
- New gateways use `<prefix>-egress` as their egress network, one per user, instead of a shared `cc-egress`. Existing gateways are unchanged.
- `web` moves on to the next free port when the default 7878 is taken, and running it twice for the same data directory prints the existing address instead of starting a second instance.
- The in-slot username defaults to `user` instead of a random name and can be changed when creating a slot. Existing slots are unchanged.
- The in-slot prompt is now two lines: username, directory and time, then the input line. Existing slots pick it up after their worker is recreated.
- Every mount gets a shortcut of the same name under `~` inside the slot. Mounting your whole home directory or the data directory is no longer allowed (existing mounts are unaffected).
- The operation log is shown in Chinese; CLI documentation moved to `docs/`.
- `nightly` builds and `:nightly` images are no longer published; the install script installs the latest release by default.

### Known limitations

- Slots share the host kernel, so kernel version, uptime, CPU model and similar details are identical across slots. This project does not promise that accounts cannot be linked.
