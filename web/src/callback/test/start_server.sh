#!/bin/bash

# Quick Start Script for Callback Test Server

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$SCRIPT_DIR"

echo "=================================="
echo "CloudLand Callback Test Server"
echo "Quick Start Script"
echo "=================================="
echo ""

# 检查是否已编译
if [ ! -f "callback_test_server" ]; then
    echo "Building test server..."
    go build -o callback_test_server callback_test_server.go
    if [ $? -ne 0 ]; then
        echo "ERROR: Build failed!"
        exit 1
    fi
    echo "Build complete."
    echo ""
fi

# 解析命令行参数
PORT=8080
VERBOSE=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -p|--port)
            PORT="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE="-verbose"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  -p, --port PORT    Server port (default: 8080)"
            echo "  -v, --verbose      Enable verbose logging"
            echo "  -h, --help         Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                 # Start on default port 8080"
            echo "  $0 -p 9000         # Start on port 9000"
            echo "  $0 -p 9000 -v      # Start on port 9000 with verbose logging"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use -h or --help for usage information"
            exit 1
            ;;
    esac
done

# 启动服务器
echo "Starting server on port $PORT..."
echo ""
echo "To test the server, open another terminal and run:"
echo "  cd $SCRIPT_DIR"
echo "  ./test_callback.sh"
echo ""
echo "Or manually test with curl:"
echo "  curl http://localhost:$PORT/health"
echo ""
echo "Press Ctrl+C to stop the server"
echo ""

./callback_test_server -port $PORT $VERBOSE
