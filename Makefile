# cc-cluster 构建入口。构建二进制只需要 Go 工具链（依赖见 go.mod），镜像需要 docker 命令行；
# 网页界面是可选的，构建它另外需要 Node.js 与 pnpm。
GO          ?= go
DOCKER      ?= docker
PNPM        ?= pnpm
PKGS        := ./cmd/... ./internal/...
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GATEWAY_IMG ?= cc-cluster-gateway:dev
WORKER_IMG  ?= cc-cluster-worker:dev
# e2e 的临时数据放在仓库 tmp/ 下（已被 .gitignore 排除），不散落到系统临时目录。
E2E_DIR     ?= $(CURDIR)/tmp/e2e
WEB_DIST    := internal/web/dist

LDFLAGS     := -X github.com/sheyestherrr/cc-cluster/internal/cli.Version=$(VERSION)
# 发布包：每个平台一个压缩包。DIST_TAG 是文件名里的版本段，默认与 VERSION 相同；
# 滚动的预发布用固定的名字（nightly），下载地址不随提交变化。
DIST_PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
DIST_TAG       ?= $(VERSION)
DIST_DIR       := dist

.PHONY: build web release dist test vet fmt check repo-check images image-gateway image-worker e2e clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X github.com/sheyestherrr/cc-cluster/internal/cli.Version=$(VERSION)" -o bin/cc-cluster ./cmd/cc-cluster

# 构建网页界面并放进 Go 的嵌入目录；之后再 make build，二进制里就带上了它。
# 没有执行过这一步时二进制照常可用，只是 cc-cluster web 会说明不带网页界面。
web:
	$(PNPM) --dir web install --frozen-lockfile
	rm -rf $(WEB_DIST)/assets $(WEB_DIST)/index.html
	$(PNPM) --dir web build

# 发布用：带网页界面的单个二进制。使用者下载后不需要 Node.js、pnpm 或 Go。
release: web build

# 各平台的发布包与校验和，放在 dist/ 下；发布工作流只调用这一步，本地也能照样生成。
# 包里除了带网页界面的二进制，还有 README 和随二进制分发的第三方代码的许可说明。
dist: web
	rm -rf $(DIST_DIR)
	$(GO) mod download
	set -e; yaml="$$($(GO) list -m -f '{{.Dir}}' gopkg.in/yaml.v3)"; \
	for p in $(DIST_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; name=cc-cluster_$(DIST_TAG)_$${os}_$${arch}; \
		mkdir -p $(DIST_DIR)/$$name/licenses; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$$name/cc-cluster ./cmd/cc-cluster; \
		cp README.md LICENSE $(DIST_DIR)/$$name/; \
		cp web/NOTICE.md $(DIST_DIR)/$$name/licenses/web-NOTICE.md; \
		cp "$$yaml/LICENSE" $(DIST_DIR)/$$name/licenses/yaml.v3-LICENSE; \
		cp "$$yaml/NOTICE" $(DIST_DIR)/$$name/licenses/yaml.v3-NOTICE; \
		tar -C $(DIST_DIR) -czf $(DIST_DIR)/$$name.tar.gz $$name; \
		rm -rf $(DIST_DIR)/$$name; \
	done
	cd $(DIST_DIR) && sha256sum *.tar.gz > SHA256SUMS

test:
	$(GO) test $(PKGS)

vet:
	$(GO) vet $(PKGS) ./test/...
	$(GO) vet -tags e2e ./test/...

fmt:
	gofmt -l -w cmd internal test

# 提交前的静态检查与单元测试；不需要 Docker。
check: vet test
	@test -z "$$(gofmt -l cmd internal test)" || { echo "以下文件未经 gofmt："; gofmt -l cmd internal test; exit 1; }

# 仓库卫生检查：不该进版本库的路径、订阅地址与带密码的代理地址一类的内容。加 HISTORY=1 连历史一起查。
repo-check:
	sh scripts/ci/check-repo.sh $(if $(HISTORY),--history)

images: image-gateway image-worker

image-gateway:
	$(DOCKER) build -t $(GATEWAY_IMG) images/gateway

# worker 里的 tmux 客户端要与宿主的 tmux 同一版本才连得上（宿主桥接用）：
# 取宿主 tmux -V 的版本号，在镜像里从源码编译同一版本。宿主没有 tmux 时为空，镜像里只有发行版自带的。
TMUX_VERSION ?= $(shell tmux -V 2>/dev/null | awk '{print $$2}')
image-worker:
	$(DOCKER) build --build-arg TMUX_VERSION=$(TMUX_VERSION) -t $(WORKER_IMG) images/worker

# 端到端测试：需要 Docker 和网关镜像。全部使用 internal 网络与假节点，没有通往宿主外部的路径。
e2e:
	mkdir -p $(E2E_DIR)
	CC_E2E_DIR=$(E2E_DIR) CC_E2E_GATEWAY_IMAGE=$(GATEWAY_IMG) CC_E2E_WORKER_IMAGE=$(WORKER_IMG) $(GO) test -tags e2e -count=1 -timeout 15m -v ./test/e2e/

clean:
	rm -rf bin $(DIST_DIR)
