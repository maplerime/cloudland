# CloudLand Callback 模块单元测试

本目录包含 callback 模块的完整单元测试套件。

## 测试文件

| 文件 | 测试内容 | 覆盖率目标 |
|------|----------|-----------|
| `config_test.go` | 配置管理函数测试 | 100% |
| `event_test.go` | 事件结构序列化测试 | 100% |
| `queue_test.go` | 事件队列管理测试 | 90%+ |
| `worker_test.go` | HTTP 推送 worker 测试 | 85%+ |
| `metadata_test.go` | 元数据提取器测试 | 90%+ |

## 运行测试

### 方式一：使用 Makefile（推荐）

```bash
# 运行所有测试
make test

# 运行特定类型的测试
make test-config    # 配置相关测试
make test-queue     # 队列相关测试
make test-worker    # Worker 相关测试
make test-metadata  # 元数据相关测试
make test-event     # 事件相关测试

# 运行测试并生成覆盖率报告
make test-coverage

# 查看覆盖率摘要
make coverage-summary

# 运行性能测试
make benchmark

# 运行竞态检测
make race

# 代码检查
make lint
```

### 方式二：使用测试脚本

```bash
# 运行所有测试
./run_tests.sh

# 运行测试并生成覆盖率报告
./run_tests.sh --coverage

# 运行性能测试
./run_tests.sh --benchmark

# 运行竞态检测
./run_tests.sh --race

# 运行特定测试
./run_tests.sh --test TestPushEvent

# 查看帮助
./run_tests.sh --help
```

### 方式三：直接使用 Go 命令

```bash
# 运行所有测试
go test -v ./...

# 运行特定测试
go test -v -run TestPushEvent ./...

# 运行测试并生成覆盖率
go test -v -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -html=coverage.out -o coverage.html

# 运行性能测试
go test -bench=. -benchmem ./...

# 运行竞态检测
go test -race -v ./...
```

## 测试覆盖范围

### 1. 配置管理 (config_test.go)

- ✅ `IsEnabled()` - 检查功能是否启用
- ✅ `GetCallbackURL()` - 获取回调 URL
- ✅ `GetWorkerCount()` - 获取 worker 数量
- ✅ `GetQueueSize()` - 获取队列大小
- ✅ `GetTimeout()` - 获取超时时间
- ✅ `GetRetryMax()` - 获取最大重试次数
- ✅ `GetRetryInterval()` - 获取重试间隔
- ✅ 从配置文件加载配置

### 2. 事件结构 (event_test.go)

- ✅ `ResourceType.String()` - 资源类型字符串表示
- ✅ `ResourceChangeEvent` 序列化/反序列化
- ✅ `Resource` 序列化/反序列化
- ✅ `Event` 序列化/反序列化
- ✅ `RetryCount` 不被序列化
- ✅ 可选字段（`omitempty`）正确处理
- ✅ 时间戳格式验证

### 3. 队列管理 (queue_test.go)

- ✅ `InitQueue()` - 队列初始化
- ✅ `PushEvent()` - 事件推送
- ✅ 队列满时的行为
- ✅ `GetEventQueue()` - 获取队列
- ✅ `GetQueueLength()` - 获取队列长度
- ✅ 并发推送测试
- ✅ Queue 与 Worker 集成测试
- ✅ 性能基准测试

### 4. Worker (worker_test.go)

- ✅ `sendEvent()` - 发送事件
- ✅ HTTP 响应处理（成功/失败）
- ✅ 无效 JSON 处理
- ✅ 连接错误处理
- ✅ `StartWorkers()` - 启动 workers
- ✅ 重试逻辑测试
- ✅ 最大重试次数测试
- ✅ Context 取消测试
- ✅ 空事件处理
- ✅ 性能基准测试

### 5. 元数据提取 (metadata_test.go)

- ✅ `defaultExtractor()` - 默认提取器
- ✅ `extractInstanceInfo()` - 虚拟机信息提取
- ✅ `extractVolumeInfo()` - 存储卷信息提取
- ✅ `extractImageInfo()` - 镜像信息提取
- ✅ `extractInterfaceInfo()` - 网络接口信息提取
- ✅ `extractInstanceStatusBatch()` - 批量状态更新
- ✅ 命令元数据注册表测试
- ✅ `ExtractAndPushEvent()` - 提取并推送事件
- ✅ 性能基准测试

## 依赖项

### 必需的 Go 包

```go
github.com/jinzhu/gorm
github.com/spf13/viper
github.com/jinzhu/gorm/dialects/sqlite  // 仅用于测试
```

### 测试工具

- `go test` - Go 标准测试框架
- `go tool cover` - 覆盖率工具
- `golangci-lint` - 代码检查（可选）

## 持续集成

建议在 CI/CD 流程中运行以下测试：

```yaml
# .github/workflows/test.yml
name: Tests
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v2
        with:
          go-version: '1.21'
      - name: Run tests
        run: |
          cd web/src/callback
          make test
          make race
          make test-coverage
```

## 性能基准

运行性能测试后，你会看到类似以下的输出：

```
BenchmarkPushEvent-8              1000000    1234 ns/op    456 B/op    12 allocs/op
BenchmarkConcurrentPushEvent-8    500000     2345 ns/op    789 B/op    23 allocs/op
BenchmarkSendEvent-8              200000     5678 ns/op    1234 B/op   45 allocs/op
BenchmarkEventSerialization-8     3000000     890 ns/op    234 B/op     5 allocs/op
```

这些数据可以帮助你：
- 识别性能瓶颈
- 追踪性能回归
- 优化关键路径

## 已知限制

1. **数据库测试**: 使用内存 SQLite 进行单元测试，可能与生产环境（PostgreSQL）有差异
2. **HTTP 测试**: 使用 `httptest.NewServer`，可能在某些边缘情况下与真实服务器行为不同
3. **并发测试**: 虽然有竞态检测，但某些并发场景可能难以完全覆盖

## 扩展测试

如需添加新的测试用例：

1. 在对应的 `*_test.go` 文件中添加测试函数
2. 使用 `Test` 前缀命名测试函数
3. 使用表驱动测试风格（见现有测试）
4. 添加必要的性能基准测试（`Benchmark` 前缀）
5. 运行 `make test` 验证

## 故障排查

### 测试失败

如果测试失败，检查：

1. Go 版本是否正确 (`go version`)
2. 依赖是否安装 (`go mod download`)
3. 环境变量是否正确设置
4. 端口是否被占用（某些测试使用固定端口）

### 覆盖率低

如果覆盖率不达标：

1. 运行 `make test-coverage` 查看详细报告
2. 打开 `coverage.html` 查看未覆盖的代码
3. 为未覆盖的分支添加测试用例

### 竞态条件

如果竞态检测失败：

1. 使用 `make race` 重新运行
2. 检查报告中的竞态位置
3. 添加适当的同步机制（mutex、channel 等）

## 联系方式

如有问题或建议，请联系：
- 项目维护者
- 查看 GitHub Issues
- 参考项目 README

## 许可证

Apache-2.0
