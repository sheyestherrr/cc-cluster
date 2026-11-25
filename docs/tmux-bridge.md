# 宿主 tmux 桥接（可选）

需要让槽内的 Claude Code 驱动宿主上的程序（EDA、GDB 等）时，按槽位开启。

> **经桥接执行的命令是宿主上的命令**：用的是你的权限和宿主的网络，不走该槽的出口，手动断网也拦不住。只给需要它的槽位开启。

## 先准备镜像

槽内的 tmux 只是客户端，要去连宿主上的 tmux，两边的版本必须相同，否则连不上。发布的 worker 镜像里是 Debian 自带的那一版（`docker run --rm <镜像> tmux -V` 可以看），和宿主的 `tmux -V`（需要 3.0 以上）不一样时，在本机构建一个，并让要用桥接的槽位使用它：

```sh
git clone https://github.com/sheyestherrr/cc-cluster.git
make -C cc-cluster image-worker                                   # 按宿主的 tmux 版本编译，得到 cc-cluster-worker:dev
cc-cluster slot add work … --image cc-cluster-worker:dev --tmux-bridge
```

槽位的镜像在登记时确定。已有的槽位要换镜像：`slot rm`（不加 `--purge`，HOME 与身份保留）后用同一个名字重新 `slot add --image …`。

## 开启与使用

```sh
cc-cluster tmux work --on                  # 或登记时 slot add … --tmux-bridge
cc-cluster slot up work --recreate         # 重建 worker 后生效
cc-cluster claude work                     # 启动时自动带上 tmux 这个 MCP 服务器
cc-cluster tmux work                       # 在另一个终端接入同一个 tmux，观察或接管
cc-cluster tmux work --kill                # 结束它，连同其中的程序
```

每个槽位在宿主上有一个专用的 tmux，槽内经 [tmux-mcp](https://github.com/bnomei/tmux-mcp) 操作它，默认不开放结束 session / window / pane 的工具（`--tools` 可改）。

宿主升级 tmux 后要重新 `make image-worker` 并 `slot up --recreate`（客户端和 server 版本必须一致）。macOS 上不可用。
