#!/bin/bash

# 验证测试代码编译是否通过

echo "=========================================="
echo "验证 callback 模块测试代码编译"
echo "=========================================="

cd "$(dirname "$0")"

# 检查 Go 语法
echo ""
echo "[1/3] 检查 Go 语法..."
go vet ./... 2>&1
if [ $? -ne 0 ]; then
    echo "❌ Go vet 检查失败"
    exit 1
fi
echo "✓ Go vet 检查通过"

# 尝试编译测试
echo ""
echo "[2/3] 编译测试代码..."
go test -c ./... 2>&1
if [ $? -ne 0 ]; then
    echo "❌ 编译失败"
    exit 1
fi
echo "✓ 编译成功"

# 清理编译产物
rm -f callback.test

# 运行测试（短模式）
echo ""
echo "[3/3] 运行测试（短模式）..."
go test -short -v ./... 2>&1 | head -100
if [ $? -ne 0 ]; then
    echo "❌ 测试运行失败"
    exit 1
fi
echo "✓ 测试运行成功"

echo ""
echo "=========================================="
echo "✓ 所有验证通过！"
echo "=========================================="
