# 宿主 tmux 桥接（可选）

需要使槽内的 Claude Code 操作宿主上的程序（EDA、GDB 等）时，按槽位开启。

> **经桥接执行的命令在宿主上运行**：使用当前用户的权限与宿主网络，不经过该槽位的出口，手动断网对其无效。请只为需要的槽位开启。

## 准备镜像

槽内的 tmux 作为客户端连接宿主上的 tmux，两者的版本必须相同，否则无法连接。发布的 worker 镜像使用 Debian 自带的版本（可用 `docker run --rm <镜像> tmux -V` 查看）；与宿主的 `tmux -V`（需 3.0 及以上）不同时，请在本机构建镜像，并让需要桥接的槽位使用它：

```sh
git clone https://github.com/sheyestherrr/cc-cluster.git
make -C cc-cluster image-worker                                   # 按宿主的 tmux 版本编译，得到 cc-cluster-worker:dev
cc-cluster slot add work … --image cc-cluster-worker:dev --tmux-bridge
```

槽位的镜像在创建时确定。已有的槽位更换镜像时：先执行 `slot rm`（不加 `--purge`，保留 HOME 与身份），再以相同的名称重新 `slot add --image …`。

## 开启与使用

```sh
cc-cluster tmux work --on                  # 或在创建时使用 slot add … --tmux-bridge
cc-cluster slot up work --recreate         # 重建 worker 后生效
cc-cluster claude work                     # 启动时自动加载名为 tmux 的 MCP 服务器
cc-cluster tmux work                       # 在另一个终端接入同一个 tmux，用于观察或接管
cc-cluster tmux work --kill                # 结束宿主 tmux，并终止其中的程序
```

每个槽位在宿主上有一个专用的 tmux，槽内经 [tmux-mcp](https://github.com/bnomei/tmux-mcp) 操作它，默认不开放结束 session / window / pane 的工具（可用 `--tools` 修改）。

宿主升级 tmux 后需重新执行 `make image-worker` 与 `slot up --recreate`（客户端与 server 的版本必须一致）。macOS 上不可用。
