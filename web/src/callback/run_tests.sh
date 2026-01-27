#!/bin/bash

# CloudLand Callback Module - Test Runner Script
# 用于运行 callback 模块的所有单元测试

set -e  # 遇到错误立即退出

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印带颜色的消息
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 Go 是否安装
check_go() {
    if ! command -v go &> /dev/null; then
        print_error "Go is not installed. Please install Go first."
        exit 1
    fi
    print_info "Go version: $(go version)"
}

# 检查依赖
check_dependencies() {
    print_info "Checking dependencies..."

    # 检查必要的包
    local required_packages=("github.com/jinzhu/gorm" "github.com/spf13/viper")
    for pkg in "${required_packages[@]}"; do
        if ! go list -m "$pkg" &> /dev/null; then
            print_warning "Package $pkg not found, may need to be installed"
        fi
    done
}

# 运行所有测试
run_all_tests() {
    print_info "Running all unit tests (short mode - skips integration/long tests)..."
    echo "========================================"

    # 使用 short 模式运行，跳过耗时的集成测试
    go test -v -short ./...

    if [ $? -eq 0 ]; then
        print_success "All tests passed!"
        return 0
    else
        print_error "Some tests failed!"
        return 1
    fi
}

# 运行测试并生成覆盖率报告
run_coverage() {
    print_info "Running tests with coverage..."
    echo "========================================"

    go test -v -coverprofile=coverage.out -covermode=atomic ./...

    if [ $? -ne 0 ]; then
        print_error "Tests failed!"
        return 1
    fi

    # 生成 HTML 覆盖率报告
    go tool cover -html=coverage.out -o coverage.html

    # 显示覆盖率摘要
    print_info "Coverage Summary:"
    go tool cover -func=coverage.out | grep total

    print_success "Coverage report generated: coverage.html"
    print_info "Open coverage.html in a browser to view the detailed report"

    return 0
}

# 运行性能测试
run_benchmarks() {
    print_info "Running benchmark tests..."
    echo "========================================"

    go test -bench=. -benchmem -benchtime=3s ./...

    if [ $? -eq 0 ]; then
        print_success "Benchmarks completed!"
        return 0
    else
        print_error "Benchmarks failed!"
        return 1
    fi
}

# 运行竞态检测
run_race_detection() {
    print_info "Running tests with race detector..."
    echo "========================================"

    go test -race -v ./...

    if [ $? -eq 0 ]; then
        print_success "No race conditions detected!"
        return 0
    else
        print_error "Race conditions detected!"
        return 1
    fi
}

# 运行特定测试
run_specific_test() {
    local test_name=$1
    print_info "Running specific test: $test_name"
    echo "========================================"

    go test -v -run "$test_name" ./...

    if [ $? -eq 0 ]; then
        print_success "Test $test_name passed!"
        return 0
    else
        print_error "Test $test_name failed!"
        return 1
    fi
}

# 运行特定文件的测试
run_file_test() {
    local file=$1
    print_info "Running tests for file: $file"
    echo "========================================"

    go test -v "$file"

    if [ $? -eq 0 ]; then
        print_success "Tests for $file passed!"
        return 0
    else
        print_error "Tests for $file failed!"
        return 1
    fi
}

# 显示帮助信息
show_help() {
    cat << EOF
CloudLand Callback Module - Test Runner

Usage: $0 [OPTIONS]

Options:
    --all, -a              Run all tests (default)
    --coverage, -c         Run tests with coverage report
    --benchmark, -b        Run performance benchmarks
    --race, -r             Run tests with race detector
    --test <name>, -t      Run specific test by name
    --file <file>, -f      Run tests for specific file
    --help, -h             Show this help message

Examples:
    $0                      # Run all tests
    $0 --coverage           # Run tests with coverage report
    $0 --benchmark          # Run benchmarks
    $0 --race               # Run tests with race detector
    $0 --test TestPushEvent # Run specific test
    $0 --file queue_test.go # Run tests for queue_test.go

Makefile targets:
    make test               - Run all tests (same as $0)
    make test-coverage      - Run tests with coverage
    make benchmark          - Run benchmarks
    make race               - Run with race detector
    make clean              - Clean up test artifacts
    make help               - Show all Makefile targets

EOF
}

# 主函数
main() {
    check_go
    check_dependencies

    # 如果没有参数，默认运行所有测试
    if [ $# -eq 0 ]; then
        run_all_tests
        exit $?
    fi

    # 解析命令行参数
    while [[ $# -gt 0 ]]; do
        case $1 in
            --all|-a)
                run_all_tests
                exit $?
                ;;
            --coverage|-c)
                run_coverage
                exit $?
                ;;
            --benchmark|-b)
                run_benchmarks
                exit $?
                ;;
            --race|-r)
                run_race_detection
                exit $?
                ;;
            --test|-t)
                if [ -z "$2" ]; then
                    print_error "Test name is required for --test option"
                    show_help
                    exit 1
                fi
                run_specific_test "$2"
                shift
                exit $?
                ;;
            --file|-f)
                if [ -z "$2" ]; then
                    print_error "File name is required for --file option"
                    show_help
                    exit 1
                fi
                run_file_test "$2"
                shift
                exit $?
                ;;
            --help|-h)
                show_help
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                show_help
                exit 1
                ;;
        esac
        shift
    done
}

# 运行主函数
main "$@"
