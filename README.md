# cc-cluster

为 Claude Code 提供相互独立的工作槽位：每个槽位有自己的 HOME、稳定的身份和固定的代理出口。

每个槽位的 worker 容器默认没有网络，全部流量由网关容器透明转发到绑定的落地（中转 → 落地，或直连落地）。worker 内没有代理变量，也看不到 TUN 网卡；网关出问题时的结果是没网，不是直连。宿主侧只要求“用户在 docker 组里”，不依赖宿主的 iptables / nft 或特定内核版本。

## 当前状态

Go 管理器已实现：命令行、交互面板、订阅导入与测速、网页界面。除了全程使用假节点的端到端测试，还在一台机器上用真实的落地与中转节点跑通过完整链路。Claude Code 在槽内的安装、经第三方接口的调用和宿主 tmux 桥接都实际运行过；**用 Anthropic 账号登录和登录后的使用尚未验证**，登录出口对齐尚未实现。

Docker 共享宿主内核，独立配置与资源限额不能证明物理机级隔离或账号不会关联。

## 下载

[Releases](https://github.com/sheyestherrr/cc-cluster/releases) 里有带网页界面的单个二进制，Linux 与 macOS、amd64 与 arm64 各一个压缩包：

- 带版本号的（`v1.2.3`）是正式版，推标签时生成。
- `nightly` 是 master 上最新一次提交的自动构建，每次推送都会被覆盖，文件名固定（`cc-cluster_nightly_linux_amd64.tar.gz` 等）。

```sh
tar -xzf cc-cluster_v1.2.3_linux_amd64.tar.gz
sha256sum --check --ignore-missing SHA256SUMS      # 核对下载的文件
```

运行还需要两个镜像。网关镜像随每次发布推到 GHCR，拉下来后在初始化时指给它；worker 镜像里的 tmux 要与宿主的版本一致（宿主桥接用），仍然要在源码目录里 `make image-worker` 构建。

```sh
docker pull ghcr.io/sheyestherrr/cc-cluster-gateway:v1.2.3
cc-cluster init --gateway-image ghcr.io/sheyestherrr/cc-cluster-gateway:v1.2.3
```

## 构建与测试

发布版是带网页界面的单个二进制，下载后不需要 Go、Node.js 或 pnpm；运行时只需要 Docker 和两个镜像。

从源码构建需要 Go 1.23 或更新版本，有一个第三方 Go 依赖（`gopkg.in/yaml.v3`，用于解析订阅）。网页界面是可选的，构建它另外需要 Node.js 与 pnpm。

```sh
make check          # go vet、gofmt 检查、单元测试；不需要 Docker
make build          # 生成 bin/cc-cluster（没有执行过 make web 时不带网页界面）
make web            # 构建网页界面，放进 Go 的嵌入目录；之后再 make build
make release        # make web + make build：带网页界面的单个二进制
make images         # 构建网关镜像与默认的 worker 镜像
make e2e            # 端到端测试：需要 Docker 与网关镜像，只用 internal 网络和假节点
make dist           # 各平台的发布包与校验和，放在 dist/（发布工作流调用的就是它）
make repo-check     # 仓库卫生检查：不该提交的路径、订阅地址与带密码的代理地址；HISTORY=1 连历史一起查
```

推到 GitHub 后由 Actions 自动检查（`.github/workflows/ci.yml`）：`make check`、带网页界面的构建、凭据扫描（gitleaks 加上面的仓库卫生检查，都查全部历史）、Go 依赖的已知漏洞、工作流自查；推到 master 和提 PR 时另外跑端到端测试。发布见 `.github/workflows/release.yml`。

```sh
git tag v1.2.3 && git push origin v1.2.3            # 出一个正式版
```

## 使用

数据目录必须显式给出，用 `--data-dir` 或环境变量 `CC_CLUSTER_DATA`。

```sh
export CC_CLUSTER_DATA=/path/to/cc-data

cc-cluster init
cc-cluster doctor                                   # 检查这台机器能否运行，不访问外部地址
```

之后有三种用法，做的是同一件事：

- **交互面板**：在终端里不带参数运行 `cc-cluster`，按编号选择。
- **网页界面**：`cc-cluster web`，在本机浏览器里打开它打印的地址。只监听 `127.0.0.1`；在别的机器上使用时先做 SSH 端口转发。在源码目录里预览可以用 `sh scripts/web.sh [数据目录]`，它每次先重新构建再启动。
- **命令行**，如下。每个命令和子命令都可以加 `-h` 查看参数。

```sh
# 落地：槽位的出口。简单的 SOCKS5 / HTTP 节点用参数描述，其他协议用 --file 给出 Mihomo 片段（JSON）
cc-cluster node add home-a --role landing --type socks5 --server 198.51.100.20 --port 1080 \
    --username u --password-file ./pw.txt

# 中转：多个槽位共用的第一跳，通常取自订阅
cc-cluster sub add air --url-file ./sub.url         # Clash 格式的订阅；地址放在文件里，不出现在命令行
cc-cluster sub test air --landing home-a            # 接上落地测整条链路：节点自己能用不代表它肯转发到落地
cc-cluster node add hk --role transit --sub air --landing home-a    # 在能接通落地的节点里用延迟最低的

# 网关：不带 --transit 即不经中转，各槽位直连自己的落地
cc-cluster gateway add main --transit hk --backup sg      # --backup 是候选中转：主用的不通时自动改用，出口不变
cc-cluster gateway set-transit main sg hk                 # 在线更换：第一个是主用的，其余是候选；先验证它们都能接通各槽位的落地，接不通就不换

# 槽位
cc-cluster slot add alice --gateway main --landing home-a --memory 8g --cpus 4
cc-cluster slot up alice          # 对齐网关 → 分段检查链路 → 探测出口 → 启动无网的 worker → 接线 → 从 worker 位置复测出口
cc-cluster status

# 没有落地：直接用订阅里的一个节点当出口（直出）。节点被固定，不会自动更换
cc-cluster slot add bob --direct "air:节点名" && cc-cluster slot up bob

# 持续检测（网页开着时自动进行）：出口变了就给该槽断网并保持
cc-cluster watch                  # --once 只测一遍并列出结果

# 在项目目录里进入槽位：目录不在挂载范围内时询问并挂载，槽内路径与宿主相同
cd /data/projects/foo && cc-cluster enter alice
cc-cluster claude alice           # 同上，但直接启动 Claude Code

cc-cluster slot hold alice --reason "出口异常"      # 立即断网并保持，直到 release
cc-cluster slot release alice && cc-cluster slot up alice
```

几点说明：

- 出口有两种来源，规则不同。**用自己的落地**：流量只从落地出去，落地连不上就是没网，不会改从中转出去；中转只是第一跳，可以登记候选并自动切换。**直出**（订阅里的节点直接当出口）：出口是多人共用的地址，节点固定、不自动切换，不通就是没网，要换只能手动。两种情况下，持续检测发现出口地址变了都会给该槽断网并保持，等你 `slot release` 后重新 `slot up`。
- 落地没有设置时区时，首次启动槽位会按探测到的出口所在地设置（依次问 `ipinfo.io`、`ipwho.is`、`ipapi.co`，Cloudflare 的 trace 保底），之后不再自动变动；都拿不到时区时暂用 `America/Los_Angeles` 并提醒，下次启动再探测。也可以用 `node add --tz` 或 `slot add --tz` 指定。
- 启动槽位时会顺带经该槽的链路查一次第三方对出口 IP 的评估（机房 / 代理 / 滥用标记与评分），只用于展示，取不到不影响使用；`cc-cluster node report <落地>` 重新查询并多问一个汇总来源，网页的槽位与落地详情里显示同一份结果。各家的判断分开列出，它们不代表任何具体服务的结论。不想在启动时自动查询可以关掉：`cc-cluster exit-report off`（或网页“系统”页的开关），手动的重新查询不受影响。
- 槽内解析域名得到的是 `100.64.0.0/10` 里的映射地址（fake-ip）：域名交给落地那一侧解析，外部看到的解析器在落地所在地。
- 同一个落地最多同时给 5 个槽位接线。哪个槽位用哪个落地由你配置。
- 每个槽位有自己的用户名、HOME、hostname 和 machine-id，槽内没有 `/.dockerenv`。`slot add --fake-dmi` 与 `--fake-proc` 可以另外覆盖机型信息和 `/proc` 里的 CPU、内存显示，默认关闭；`--fake-proc` 设了 CPU 限额时还会把槽位绑到相应数量的核上，让 `nproc` 一类的结果与之一致（`/proc/stat` 仍是宿主的核数）。
- 默认的 worker 镜像不预装 Claude CLI：首次用 `claude` 进入时会询问是否在槽内安装，它装进该槽的 HOME 并随 HOME 持久化，安装与更新产生的访问都从该槽的出口发出。
- worker 或网关重启后，线不会自动接回：重新执行 `slot up`。在那之前该槽保持没网。

### 宿主 tmux 桥接（可选）

需要让槽内的 Claude Code 驱动宿主上的程序（EDA、GDB 等）时，按槽位开启：

```sh
cc-cluster tmux alice --on                 # 或登记时 slot add … --tmux-bridge
cc-cluster slot up alice --recreate        # 重建 worker 后生效
cc-cluster claude alice                    # 启动时自动带上 tmux 这个 MCP 服务器
cc-cluster tmux alice                      # 在另一个终端接入同一个 tmux，观察或接管
cc-cluster tmux alice --kill               # 结束它，连同其中的程序
```

每个槽位在宿主上有一个专用的 tmux，槽内经 [tmux-mcp](https://github.com/bnomei/tmux-mcp) 操作它，默认不开放结束 session / window / pane 的工具（`--tools` 可改）。停止或删除槽位不会结束宿主的 tmux。

**经桥接执行的命令是宿主上的命令：用的是你的权限和宿主网络，不走该槽的出口，手动断网也拦不住。** 只给需要它的槽位开启。宿主 pane 里是你自己的登录 shell，会读你的启动文件；历史单独记在该槽目录下的 `tmux-history`（靠 `HISTFILE` 环境变量，启动文件里自己重设了它或用 fish 时不生效）。

tmux 的客户端和 server 要同一版本才连得上：`make image-worker` 按宿主 `tmux -V` 的版本在镜像里编译 tmux，所以镜像要在使用它的机器上构建，宿主升级 tmux 后重建镜像并 `slot up --recreate`。宿主需要 tmux 3.0 以上；macOS 上不可用。

## 目录

| 路径 | 内容 |
| --- | --- |
| `cmd/cc-cluster` | 程序入口 |
| `internal/` | 配置模型、文件状态、编址、核心配置生成、订阅解析、Docker API 客户端、编排、命令行与网页服务 |
| `web/` | 网页界面的前端源码（React），构建后嵌入二进制 |
| `scripts/` | 辅助脚本：`web.sh` 一键构建并启动网页界面；`ci/check-repo.sh` 仓库卫生检查 |
| `.github/` | GitHub Actions 的检查与发布工作流、依赖更新配置 |
| `images/gateway` | 网关镜像：固定版本的 Mihomo 与监督脚本 |
| `images/worker` | 默认的 worker 基础镜像 |
| `test/e2e` | 端到端测试与假服务 |

## 致谢

网页界面的风格、基础组件和页面骨架取自 [Claude-Hydra](https://github.com/jin-yu-liu/Claude-Hydra)（MIT 许可），订阅导入的做法也参考了它；取用的文件、改动和许可全文见 [web/NOTICE.md](web/NOTICE.md)。感谢该项目。

默认 worker 镜像的软件包选择参考了 Claude Code 官方的开发容器，Dockerfile 为重新编写。网关核心是 [Mihomo](https://github.com/MetaCubeX/mihomo)。

宿主桥接使用 [tmux-mcp](https://github.com/bnomei/tmux-mcp)（MIT），由 worker 镜像经 npm 安装；镜像里的 [tmux](https://github.com/tmux/tmux)（ISC）在构建时从官方源码包编译。本仓库不含它们的代码。
