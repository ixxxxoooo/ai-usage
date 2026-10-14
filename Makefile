# AI Usage 常用命令。作者：ygw
.PHONY: help test vet fmt build build-universal run live shots clean

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

test: ## 运行全部测试（含竞态检测）
	go test -race -count=1 ./...

vet: ## go vet + mygo vet
	go vet ./...
	go tool mygo vet

fmt: ## 检查格式（有未格式化文件时失败）
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "请先运行 gofmt -w ." && exit 1)

build: ## 构建当前架构的 .app 与 .dmg（输出到 build/）
	go tool mygo build

build-universal: ## 构建 Intel + Apple 芯片通用的 .app 与 .dmg
	go tool mygo build -platform darwin/universal

run: ## 开发模式运行（热重载）
	go tool mygo dev

live: ## 用本机真实登录做只读联调（需要已登录 Cursor / Chrome 的 DeepSeek）
	go test -tags live -v -count=1 ./internal/live

shots: ## 无头渲染各页面截图到 /tmp/aiusage-shots
	AIUSAGE_SHOTS=/tmp/aiusage-shots go test -count=1 -run TestRenderScreenshots ./internal/gui

clean: ## 清理构建产物
	rm -rf build
