# Makefile for Callback Test Server

.PHONY: all build run test clean help

# 默认目标
all: build

# 构建测试服务器
build:
	@echo "Building callback test server..."
	@go build -o callback_test_server callback_test_server.go
	@echo "Build complete: callback_test_server"

# 运行测试服务器（默认配置）
run: build
	@echo "Starting callback test server on http://0.0.0.0:8080"
	@./callback_test_server

# 运行测试服务器（详细模式）
run-verbose: build
	@echo "Starting callback test server in verbose mode..."
	@./callback_test_server -verbose

# 运行测试服务器（自定义端口）
run-custom: build
	@echo "Starting callback test server on http://127.0.0.1:9000"
	@./callback_test_server -host 127.0.0.1 -port 9000

# 运行测试脚本
test:
	@echo "Running callback tests..."
	@bash test_callback.sh

# 清理构建产物
clean:
	@echo "Cleaning up..."
	@rm -f callback_test_server
	@echo "Clean complete"

# 显示帮助信息
help:
	@echo "CloudLand Callback Test Server Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make build         - Build the test server"
	@echo "  make run           - Build and run the server (default: 0.0.0.0:8080)"
	@echo "  make run-verbose   - Build and run with verbose logging"
	@echo "  make run-custom    - Build and run on custom host:port (127.0.0.1:9000)"
	@echo "  make test          - Run test script to send sample events"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make help          - Show this help message"
	@echo ""
	@echo "Examples:"
	@echo "  # Build and start server"
	@echo "  make run"
	@echo ""
	@echo "  # In another terminal, run tests"
	@echo "  make test"
	@echo ""
	@echo "  # Custom run"
	@echo "  ./callback_test_server -host 127.0.0.1 -port 9000 -verbose"
