# CloudLand Resource Change Callback Module

这个模块提供了资源变化事件的异步回调功能，当资源状态发生变化时，自动将事件推送到配置的 URL。

## 功能特性

- **零侵入设计**: 只修改 `rpcs/frontback.go` 一行代码
- **元数据驱动**: 通过注册表配置监控的命令
- **异步推送**: 不阻塞主业务流程
- **重试机制**: 支持失败重试
- **类型安全**: 使用枚举定义资源类型
- **易扩展**: 新增资源监控只需在注册表中添加配置

## 模块结构

```
callback/
├── event.go          # 事件定义和资源类型枚举
├── config.go         # 配置管理
├── queue.go          # 事件队列管理
├── worker.go         # HTTP 推送 Worker
├── metadata.go       # 命令元数据注册表
├── README.md         # 本文档
└── test/             # 测试服务器
    ├── callback_test_server.go    # 测试服务器程序
    ├── test_callback.sh           # 测试脚本
    ├── start_server.sh            # 快速启动脚本
    ├── Makefile                   # 构建工具
    ├── config.example.toml        # 配置示例
    └── README.md                  # 测试服务器文档
```

## 配置说明

在 `conf/config.toml` 中添加以下配置：

```toml
[callback]
# 是否启用资源变化回调功能
enabled = true

# 回调目标 URL
url = "http://your-monitoring-service.com/api/v1/resource-changes"

# Worker 并发数量（建议 3-5）
workers = 3

# 事件队列大小（建议 10000）
queue_size = 10000

# HTTP 请求超时时间（秒）
timeout = 30

# 最大重试次数
retry_max = 3

# 重试间隔（秒）
retry_interval = 5
```

## 监控的资源类型

当前已注册监控的命令：

### 虚拟机实例 (instance)
- `launch_vm` - 虚拟机启动
- `action_vm` - 虚拟机操作（启动、停止等）
- `clear_vm` - 虚拟机清理
- `migrate_vm` - 虚拟机迁移

### 存储卷 (volume)
- `create_volume_local` - 创建本地卷
- `create_volume_wds_vhost` - 创建 WDS vhost 卷
- `attach_volume_local` - 挂载本地卷
- `attach_volume_wds_vhost` - 挂载 WDS vhost 卷
- `detach_volume` - 卸载卷
- `resize_volume` - 扩容卷

### 镜像 (image)
- `create_image` - 创建镜像
- `capture_image` - 捕获镜像

### 网络接口 (interface)
- `attach_vm_nic` - 挂载网卡
- `detach_vm_nic` - 卸载网卡

## 事件格式

```json
{
  "resource_type": "instance",
  "resource_uuid": "550e8400-e29b-41d4-a716-446655440000",
  "resource_id": 123,
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
}
```

## 测试方法

### 启动测试服务器

```bash
cd web/src/callback/test
./start_server.sh
```

或使用 Makefile:

```bash
cd web/src/callback/test
make run
```

### 运行测试脚本

在另一个终端窗口：

```bash
cd web/src/callback/test
./test_callback.sh
```

详细使用说明请查看 [`test/README.md`](test/README.md)

## 扩展新资源

如果需要监控新的资源类型，只需在 `metadata.go` 的 `commandMetadataRegistry` 中添加：

```go
"your_new_command": {
    ResourceType: ResourceTypeYourResource,
    IDArgIndex:   1, // 资源 ID 在参数中的位置
},
```

## 许可证

Apache-2.0