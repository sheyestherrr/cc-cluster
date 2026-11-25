# 从源码构建与开发

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

不想用发布的镜像时在本机构建（只需要 docker 和 make），`cc-cluster init` 不带镜像参数时默认就用这两个：

```sh
make image-gateway       # 得到 cc-cluster-gateway:dev
make image-worker        # 得到 cc-cluster-worker:dev，里面的 tmux 按这台机器的 tmux 版本编译
```

| 路径 | 内容 |
| --- | --- |
| `cmd/cc-cluster` | 程序入口 |
| `internal/` | 配置模型、文件状态、编址、核心配置生成、订阅解析、Docker API 客户端、编排、命令行与网页服务 |
| `web/` | 网页界面的前端源码（React），构建后嵌入二进制 |
| `images/` | 网关镜像（固定版本的 Mihomo 与监督脚本）和默认的 worker 镜像 |
| `test/e2e` | 端到端测试与假服务 |
| `scripts/` | `install.sh` 一键安装，`web.sh` 构建并启动网页界面，`ci/check-repo.sh` 仓库卫生检查 |
| `docs/` | 使用文档 |

各部分怎么配合见[架构说明](../ARCHITECTURE.md)。

开发在 `dev` 分支，`master` 只接收它的快进；PR 请提到 `dev`。

版本号是 `v主.次.修订`，预发布写成 `v0.1.0-alpha.1`、`v1.0.0-rc.1`。发版前先在 `CHANGELOG.md` 里写好这个版本的一段（中英文），再在 `master` 上打 `v*` 标签并推送；Release 的说明取自这一段，没有就不会发布。
