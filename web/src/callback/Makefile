# Makefile for Callback Module Unit Tests

.PHONY: all test test-unit test-integration test-coverage clean help

# 默认目标
all: test

# 运行所有单元测试
test:
	@echo "========================================"
	@echo "Running all callback unit tests..."
	@echo "========================================"
	@go test -v ./...

# 运行单元测试（不包括集成测试）
test-unit:
	@echo "========================================"
	@echo "Running unit tests only..."
	@echo "========================================"
	@go test -v -short ./...

# 运行特定文件的测试
test-config:
	@echo "Running config tests..."
	@go test -v -run TestConfig ./...

test-queue:
	@echo "Running queue tests..."
	@go test -v -run TestQueue ./...

test-worker:
	@echo "Running worker tests..."
	@go test -v -run TestWorker ./...

test-metadata:
	@echo "Running metadata tests..."
	@go test -v -run TestMetadata ./...

test-event:
	@echo "Running event tests..."
	@go test -v -run TestEvent ./...

# 运行测试并生成覆盖率报告
test-coverage:
	@echo "========================================"
	@echo "Running tests with coverage..."
	@echo "========================================"
	@go test -v -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo ""
	@echo "Coverage report generated: coverage.html"
	@go tool cover -func=coverage.out

# 显示覆盖率摘要
coverage-summary:
	@go test -coverprofile=coverage.out -covermode=atomic ./... > /dev/null
	@echo "========================================"
	@echo "Coverage Summary"
	@echo "========================================"
	@go tool cover -func=coverage.out | grep total

# 运行性能测试
benchmark:
	@echo "========================================"
	@echo "Running benchmark tests..."
	@echo "========================================"
	@go test -bench=. -benchmem ./...

# 运行性能测试并保存结果
benchmark-save:
	@echo "Running benchmark tests and saving results..."
	@go test -bench=. -benchmem -benchtime=3s ./... > benchmark_results.txt
	@echo "Benchmark results saved to: benchmark_results.txt"

# 运行竞态检测
race:
	@echo "========================================"
	@echo "Running tests with race detector..."
	@echo "========================================"
	@go test -race -v ./...

# 代码检查
lint:
	@echo "Running code linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install it with: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b \$$(go env GOPATH)/bin"; \
	fi

# 格式化代码
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# 运行 go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

# 清理测试产物
clean:
	@echo "Cleaning up..."
	@rm -f coverage.out coverage.html benchmark_results.txt
	@echo "Clean complete"

# 显示测试帮助
help:
	@echo "CloudLand Callback Module - Unit Test Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make test              - Run all unit tests"
	@echo "  make test-unit         - Run unit tests only (no integration)"
	@echo "  make test-config       - Run config-related tests"
	@echo "  make test-queue        - Run queue-related tests"
	@echo "  make test-worker       - Run worker-related tests"
	@echo "  make test-metadata     - Run metadata-related tests"
	@echo "  make test-event        - Run event-related tests"
	@echo "  make test-coverage     - Run tests with coverage report"
	@echo "  make coverage-summary  - Show coverage summary"
	@echo "  make benchmark         - Run performance benchmarks"
	@echo "  make benchmark-save    - Run benchmarks and save results"
	@echo "  make race              - Run tests with race detector"
	@echo "  make lint              - Run code linter"
	@echo "  make fmt               - Format code"
	@echo "  make vet               - Run go vet"
	@echo "  make clean             - Clean up test artifacts"
	@echo "  make help              - Show this help message"
	@echo ""
	@echo "Examples:"
	@echo "  # Run all tests"
	@echo "  make test"
	@echo ""
	@echo "  # Run tests with coverage"
	@echo "  make test-coverage"
	@echo "  # Open coverage report in browser"
	@echo "  open coverage.html"
	@echo ""
	@echo "  # Run benchmarks"
	@echo "  make benchmark"
	@echo ""
	@echo "  # Run specific test"
	@echo "  go test -v -run TestPushEventQueueFull ./..."
