#!/bin/bash

# CloudLand Callback Test Script
# 用于测试 callback 功能，发送模拟的资源变化事件

SERVER_URL="${1:-http://localhost:8080/api/v1/resource-changes}"

echo "=================================="
echo "CloudLand Callback Test Script"
echo "=================================="
echo "Target URL: $SERVER_URL"
echo ""

# 测试 1: 虚拟机启动事件
echo "[Test 1] Sending instance launch event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "instance",
    "resource_uuid": "550e8400-e29b-41d4-a716-446655440000",
    "resource_id": 1,
    "status": "running",
    "previous_status": "pending",
    "timestamp": "2025-10-30T10:30:00Z",
    "metadata": {
      "hostname": "test-vm-001",
      "hyper_id": 5,
      "zone_id": 1,
      "cpu": 4,
      "memory": 8192,
      "disk": 100
    }
  }' | jq .
echo ""
sleep 1

# 测试 2: 卷创建事件
echo "[Test 2] Sending volume create event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "volume",
    "resource_uuid": "660e8400-e29b-41d4-a716-446655440001",
    "resource_id": 2,
    "status": "available",
    "previous_status": "creating",
    "timestamp": "2025-10-30T10:31:00Z",
    "metadata": {
      "name": "test-volume-001",
      "size": 100,
      "instance_id": 0,
      "target": "",
      "format": "qcow2",
      "path": "local:///var/lib/cloudland/volumes/volume-2.qcow2"
    }
  }' | jq .
echo ""
sleep 1

# 测试 3: 卷挂载事件
echo "[Test 3] Sending volume attach event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "volume",
    "resource_uuid": "660e8400-e29b-41d4-a716-446655440001",
    "resource_id": 2,
    "status": "attached",
    "previous_status": "available",
    "timestamp": "2025-10-30T10:32:00Z",
    "metadata": {
      "name": "test-volume-001",
      "size": 100,
      "instance_id": 1,
      "target": "vdb",
      "format": "qcow2"
    }
  }' | jq .
echo ""
sleep 1

# 测试 4: 镜像创建事件
echo "[Test 4] Sending image create event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "image",
    "resource_uuid": "770e8400-e29b-41d4-a716-446655440002",
    "resource_id": 3,
    "status": "active",
    "previous_status": "saving",
    "timestamp": "2025-10-30T10:33:00Z",
    "metadata": {
      "name": "ubuntu-22.04",
      "format": "qcow2",
      "os_code": "linux",
      "size": 2147483648,
      "architecture": "x86_64"
    }
  }' | jq .
echo ""
sleep 1

# 测试 5: 网络接口挂载事件
echo "[Test 5] Sending interface attach event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "interface",
    "resource_uuid": "880e8400-e29b-41d4-a716-446655440003",
    "resource_id": 4,
    "status": "active",
    "timestamp": "2025-10-30T10:34:00Z",
    "metadata": {
      "name": "eth0",
      "mac_addr": "52:54:00:12:34:56",
      "instance_id": 1,
      "hyper_id": 5,
      "type": "vxlan"
    }
  }' | jq .
echo ""
sleep 1

# 测试 6: 虚拟机关机事件
echo "[Test 6] Sending instance shutdown event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "instance",
    "resource_uuid": "550e8400-e29b-41d4-a716-446655440000",
    "resource_id": 1,
    "status": "shut_off",
    "previous_status": "running",
    "timestamp": "2025-10-30T10:35:00Z",
    "metadata": {
      "hostname": "test-vm-001",
      "hyper_id": 5,
      "zone_id": 1
    }
  }' | jq .
echo ""

# 查看统计信息
echo "=================================="
echo "Checking server statistics..."
echo "=================================="
curl -s "http://localhost:8080/stats" | jq .

echo ""
echo "Test completed!"
