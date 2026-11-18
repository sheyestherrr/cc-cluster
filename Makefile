# cc-cluster 构建入口。只依赖 Go 工具链和 docker 命令行，不下载任何 Go 依赖。
GO          ?= go
DOCKER      ?= docker
PKGS        := ./cmd/... ./internal/...
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GATEWAY_IMG ?= cc-cluster-gateway:dev
WORKER_IMG  ?= cc-cluster-worker:dev
# e2e 的临时数据放在仓库 tmp/ 下（已被 .gitignore 排除），不散落到系统临时目录。
E2E_DIR     ?= $(CURDIR)/tmp/e2e

.PHONY: build test vet fmt check images image-gateway image-worker e2e clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X github.com/sheyestherrr/cc-cluster/internal/cli.Version=$(VERSION)" -o bin/cc-cluster ./cmd/cc-cluster

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
