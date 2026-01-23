# Callback Test Server

一个简单的 HTTP 服务器，用于测试 CloudLand 资源变化 callback 功能。

## 功能特性

- 接收和解析资源变化事件
- 实时打印事件详细信息
- 统计接收到的事件数量
- 健康检查端点

## 构建

```bash
cd web/src/test
go build -o callback_test_server callback_test_server.go
```

## 使用方法

### 启动服务器

使用默认配置（监听 0.0.0.0:8080）：

```bash
./callback_test_server
```

自定义端口和主机：

```bash
./callback_test_server -host 127.0.0.1 -port 9000
```

启用详细日志（每 30 秒输出统计信息）：

```bash
./callback_test_server -verbose
```

查看所有选项：

```bash
./callback_test_server -h
```

### 命令行参数

- `-host string`: 服务器监听地址（默认 "0.0.0.0"）
- `-port int`: 服务器监听端口（默认 8080）
- `-verbose`: 启用详细日志模式

## API 端点

### POST /api/v1/resource-changes

接收资源变化事件的主要端点。

**请求示例：**

```bash
curl -X POST http://localhost:8080/api/v1/resource-changes \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "instance",
    "resource_uuid": "550e8400-e29b-41d4-a716-446655440000",
    "resource_id": 123,
    "status": "running",
    "timestamp": "2025-10-30T10:30:00Z",
    "metadata": {
      "hostname": "test-vm-001",
      "hyper_id": 5,
      "zone_id": 1
    }
  }'
```

**响应示例：**

```json
{
  "status": "ok",
  "message": "Event received successfully",
  "count": 1
}
```

### GET /stats

查看服务器统计信息。

**请求示例：**

```bash
curl http://localhost:8080/stats
```

**响应示例：**

```json
{
  "total_received": 10,
  "total_success": 9,
  "total_failed": 1,
  "uptime": "5m30s"
}
```

### GET /health

健康检查端点。

**请求示例：**

```bash
curl http://localhost:8080/health
```

**响应示例：**

```json
{
  "status": "healthy",
  "time": "2025-10-30 10:35:00"
}
```

## 配置 CloudLand

在 CloudLand 的配置文件 `conf/config.toml` 中添加：

```toml
[callback]
enabled = true
url = "http://localhost:8080/api/v1/resource-changes"
workers = 3
queue_size = 10000
timeout = 30
retry_max = 3
retry_interval = 5
```

## 输出示例

当服务器接收到事件时，会在控制台打印：

```
================================================================================
Event #1 received at 2025-10-30 10:35:12.345
================================================================================
  Resource Type : instance
  Resource UUID : 550e8400-e29b-41d4-a716-446655440000
  Resource ID   : 123
  Status        : running
  Timestamp     : 2025-10-30 10:35:12.345
  Metadata      :
    - hostname    : test-vm-001
    - hyper_id    : 5
    - zone_id     : 1
    - cpu         : 4
    - memory      : 8192
    - disk        : 100
================================================================================
```

## 测试脚本

创建一个测试脚本 `test_callback.sh`：

```bash
#!/bin/bash

# 测试发送不同类型的资源事件

echo "Testing instance event..."
curl -X POST http://localhost:8080/api/v1/resource-changes \
  -H "Content-Type: application/json" \
  -d '{
    "resource_type": "instance",
    "resource_uuid": "550e8400-e29b-41d4-a716-446655440000",
    "resource_id": 1,
    "status": "running",
    "timestamp": "2025-10-30T10:30:00Z",
    "metadata": {
      "hostname": "test-vm-001",
      "cpu": 4,
      "memory": 8192
    }
  }'

echo -e "\n\nTesting volume event..."
curl -X POST http://localhost:8080/api/v1/resource-changes \
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
      "size": 100
    }
  }'

echo -e "\n\nChecking stats..."
curl http://localhost:8080/stats | jq .
```

## 故障排查

### 问题：无法接收事件

1. 检查防火墙设置
2. 确认端口未被占用：`lsof -i :8080`
3. 检查 CloudLand 配置中的 callback URL 是否正确

### 问题：事件解析失败

检查发送的 JSON 格式是否正确，确保包含必需字段：
- `resource_type`
- `resource_uuid`
- `resource_id`
- `status`
- `timestamp`

## 许可证

Apache-2.0