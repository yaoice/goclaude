# GoClaude - Makefile
# 构建、测试与开发工具命令

.PHONY: all build test lint clean run fmt vet

# 默认目标
all: fmt vet lint test build

# 构建二进制
BUILD_DIR := ./bin
BINARY := goclaude
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

build:
	@echo ">>> 构建 $(BINARY)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/goclaude/

# 运行
run:
	go run ./cmd/goclaude/

# 测试
test:
	@echo ">>> 运行测试..."
	go test -race -cover ./...

# 测试（详细输出）
test-verbose:
	go test -race -cover -v ./...

# 代码格式化
fmt:
	@echo ">>> 格式化代码..."
	gofmt -s -w .

# 静态检查
vet:
	@echo ">>> 静态分析..."
	go vet ./...

# Lint（需要安装 golangci-lint）
lint:
	@echo ">>> Lint 检查..."
	golangci-lint run ./...

# 清理构建产物
clean:
	@echo ">>> 清理..."
	rm -rf $(BUILD_DIR)
	go clean -cache

# 安装依赖
deps:
	@echo ">>> 下载依赖..."
	go mod download
	go mod tidy

# 生成（如有代码生成需求）
generate:
	go generate ./...

# E2E 真实运行测试（需 DEEPSEEK_API_KEY + python3 + expect）
e2e: build
	bash scripts/e2e/run_e2e.sh
