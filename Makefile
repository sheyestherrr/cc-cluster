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

.PHONY: build web release test vet fmt check images image-gateway image-worker e2e clean

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

images: image-gateway image-worker

image-gateway:
	$(DOCKER) build -t $(GATEWAY_IMG) images/gateway

image-worker:
	$(DOCKER) build -t $(WORKER_IMG) images/worker

# 端到端测试：需要 Docker 和网关镜像。全部使用 internal 网络与假节点，没有通往宿主外部的路径。
e2e:
	mkdir -p $(E2E_DIR)
	CC_E2E_DIR=$(E2E_DIR) CC_E2E_GATEWAY_IMAGE=$(GATEWAY_IMG) $(GO) test -tags e2e -count=1 -timeout 15m -v ./test/e2e/

clean:
	rm -rf bin
