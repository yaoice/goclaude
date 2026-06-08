# ============================================================================
# GoClaude - Makefile
# 构建、测试、格式化、部署自动化
# ============================================================================

# ---------------------------------------------------------------------------
# 变量定义（集中管理）
# ---------------------------------------------------------------------------

# Go 与工具
GO          := go
GOFMT       := gofmt
GOLINT      := golangci-lint

# 项目信息
MODULE      := github.com/anthropics/goclaude
BINARY_NAME := goclaude
MAIN_PATH   := ./cmd/goclaude/

# 目录
BUILD_DIR   := ./bin
COVER_DIR   := ./coverage

# 版本信息（通过 git 自动注入）
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME  := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# 链接器标志（注入运行时版本信息）
LDFLAGS     := -s -w \
               -X main.version=$(VERSION) \
               -X main.buildTime=$(BUILD_TIME) \
               -X main.gitCommit=$(GIT_COMMIT)

# 测试标志
TEST_FLAGS  := -race -cover -count=1 -timeout 120s

# 获取原生平台信息
GOOS_NATIVE := $(shell go env GOOS)
GOARCH_NATIVE := $(shell go env GOARCH)

# ---------------------------------------------------------------------------
# 辅助宏
# ---------------------------------------------------------------------------

# 彩色输出
ECHO = @echo ">> $(1)"

# 构建函数引用
# $(call BUILD_PLATFORM,os,arch[,ext])
define BUILD_PLATFORM
	@mkdir -p $(BUILD_DIR)/$(1)_$(2)
	$(call ECHO,构建 $(BINARY_NAME)-$(1)-$(2)...)
	CGO_ENABLED=0 GOOS=$(1) GOARCH=$(2) $(GO) build \
		-ldflags "$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(1)_$(2)/$(BINARY_NAME)$(3) \
		$(MAIN_PATH)
endef

# ---------------------------------------------------------------------------
# 伪目标声明
# ---------------------------------------------------------------------------
.PHONY: all build build-all \
        build-linux-amd64 build-linux-arm64 \
        build-darwin-amd64 build-darwin-arm64 \
        build-windows-amd64 \
        build-native \
        run install clean \
        test test-verbose test-coverage test-race \
        fmt vet lint deps generate e2e help

# ============================================================================
# 默认目标：完整开发流程
# ============================================================================
all: fmt vet lint test build
	$(call ECHO,全部完成 ✓)

# ============================================================================
# 构建目标
# ============================================================================

# 构建当前平台
build:
	$(call ECHO,构建 $(BINARY_NAME) ($(GOOS_NATIVE)/$(GOARCH_NATIVE))...)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build \
		-ldflags "$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(BINARY_NAME) \
		$(MAIN_PATH)
	$(call ECHO,输出: $(BUILD_DIR)/$(BINARY_NAME) (版本: $(VERSION)))

# 交叉编译 — Linux
build-linux-amd64:
	$(call BUILD_PLATFORM,linux,amd64)
build-linux-arm64:
	$(call BUILD_PLATFORM,linux,arm64)

# 交叉编译 — macOS
build-darwin-amd64:
	$(call BUILD_PLATFORM,darwin,amd64)
build-darwin-arm64:
	$(call BUILD_PLATFORM,darwin,arm64)

# 交叉编译 — Windows
build-windows-amd64:
	$(call BUILD_PLATFORM,windows,amd64,.exe)

# 交叉编译所有平台
build-all: \
	build-linux-amd64 \
	build-linux-arm64 \
	build-darwin-amd64 \
	build-darwin-arm64 \
	build-windows-amd64
	$(call ECHO,所有平台构建完成 ✓)

# 构建原生平台（等价于 build，作为显式目标）
build-native: build

# ============================================================================
# 运行
# ============================================================================

run:
	$(GO) run $(MAIN_PATH)

# ============================================================================
# 安装
# ============================================================================

install: build
	$(call ECHO,安装 $(BINARY_NAME) 到 $$GOPATH/bin...)
	$(GO) install $(MAIN_PATH)

# ============================================================================
# 测试目标
# ============================================================================

# 标准测试（含竞态检测和覆盖率概要）
test:
	$(call ECHO,运行全部测试...)
	$(GO) test $(TEST_FLAGS) ./...

# 详细输出测试
test-verbose:
	$(call ECHO,运行全部测试（详细输出）...)
	$(GO) test $(TEST_FLAGS) -v ./...

# 生成覆盖率 HTML 报告
test-coverage:
	$(call ECHO,运行测试并生成覆盖率报告...)
	@mkdir -p $(COVER_DIR)
	$(GO) test $(TEST_FLAGS) -coverprofile=$(COVER_DIR)/coverage.out ./...
	$(GO) tool cover -html=$(COVER_DIR)/coverage.out -o $(COVER_DIR)/coverage.html
	$(call ECHO,覆盖率报告: $(COVER_DIR)/coverage.html)

# 竞态检测
test-race:
	$(call ECHO,运行竞态检测...)
	$(GO) test -race -count=1 -timeout 120s ./...

# ============================================================================
# E2E 端到端测试
# ============================================================================

e2e: build
	$(call ECHO,运行 E2E 端到端测试...)
	bash tests/e2e/run_e2e.sh

# ============================================================================
# 代码质量
# ============================================================================

# 格式化代码
fmt:
	$(call ECHO,格式化代码...)
	$(GOFMT) -s -w .

# 静态分析
vet:
	$(call ECHO,静态分析...)
	$(GO) vet ./...

# Lint 检查
lint:
	$(call ECHO,Lint 检查...)
	$(GOLINT) run ./...

# ============================================================================
# 依赖管理
# ============================================================================

deps:
	$(call ECHO,下载依赖...)
	$(GO) mod download
	$(GO) mod tidy

# 验证依赖
deps-verify: deps
	$(call ECHO,验证依赖...)
	$(GO) mod verify

# ============================================================================
# 代码生成
# ============================================================================

generate:
	$(GO) generate ./...

# ============================================================================
# 清理
# ============================================================================

clean:
	$(call ECHO,清理构建产物...)
	rm -rf $(BUILD_DIR) $(COVER_DIR)
	$(GO) clean -cache -testcache -modcache 2>/dev/null || true

# 深度清理（含 Go 模块缓存）
clean-all: clean
	$(call ECHO,深度清理...)
	$(GO) clean -cache -testcache -modcache

# ============================================================================
# 帮助
# ============================================================================

help:
	@echo ""
	@echo " GoClaude Makefile"
	@echo " ═══════════════════════════════════════════════════"
	@echo ""
	@echo " 构建"
	@echo "   build                  构建当前平台二进制"
	@echo "   build-all              交叉编译所有平台"
	@echo "   build-linux-amd64      构建 Linux x86_64"
	@echo "   build-linux-arm64      构建 Linux ARM64"
	@echo "   build-darwin-amd64     构建 macOS x86_64"
	@echo "   build-darwin-arm64     构建 macOS Apple Silicon"
	@echo "   build-windows-amd64    构建 Windows x86_64"
	@echo ""
	@echo " 运行 & 安装"
	@echo "   run                    直接运行 (go run)"
	@echo "   install                安装到 \$GOPATH/bin"
	@echo ""
	@echo " 测试"
	@echo "   test                   运行全部测试 (含 race + cover)"
	@echo "   test-verbose           详细输出测试"
	@echo "   test-coverage          生成覆盖率 HTML 报告"
	@echo "   test-race              仅竞态检测"
	@echo "   e2e                    端到端真实运行测试"
	@echo ""
	@echo " 代码质量"
	@echo "   fmt                    格式化代码 (gofmt -s)"
	@echo "   vet                    静态分析 (go vet)"
	@echo "   lint                   Lint 检查 (golangci-lint)"
	@echo ""
	@echo " 依赖"
	@echo "   deps                   下载并整理依赖"
	@echo "   deps-verify            验证依赖完整性"
	@echo ""
	@echo " 其他"
	@echo "   all                    完整流程: fmt → vet → lint → test → build"
	@echo "   generate               执行 go generate"
	@echo "   clean                  清理构建产物与缓存"
	@echo "   clean-all              深度清理（含模块缓存）"
	@echo "   help                   显示此帮助"
	@echo ""
	@echo " 变量"
	@echo "   BINARY_NAME=$(BINARY_NAME)"
	@echo "   BUILD_DIR=$(BUILD_DIR)"
	@echo "   VERSION=$(VERSION)"
	@echo "   GOOS_NATIVE=$(GOOS_NATIVE)"
	@echo "   GOARCH_NATIVE=$(GOARCH_NATIVE)"
	@echo ""
