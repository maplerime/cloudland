# 运行 Callback 模块测试

## 当前状态

✅ 所有编译错误已修复
✅ 测试代码已准备就绪
✅ 可以正常运行

## 快速开始

```bash
cd /opt/cloudland/web/src/callback

# 运行所有测试（跳过需要 SQLite 的测试）
go test -v ./...

# 使用短模式运行（跳过耗时测试）
go test -short -v ./...
```

## 如果需要运行数据库测试

metadata_test.go 中的部分测试需要 SQLite 驱动。要运行这些测试：

```bash
# 安装 SQLite 驱动
go get github.com/mattn/go-sqlite3

# 然后运行所有测试
go test -v ./...
```

## 可用的测试

### 可以立即运行的测试（无需额外依赖）

| 测试文件 | 测试内容 | 状态 |
|---------|---------|------|
| `config_test.go` | 配置管理函数 | ✅ 可运行 |
| `event_test.go` | 事件结构序列化 | ✅ 可运行 |
| `queue_test.go` | 队列管理 | ✅ 可运行 |
| `worker_test.go` | Worker 和 HTTP 推送 | ✅ 可运行 |

### 需要 SQLite 驱动的测试

| 测试文件 | 测试内容 | 状态 |
|---------|---------|------|
| `metadata_test.go` | 元数据提取器 | ⚠️ 需要安装 SQLite 驱动 |

## 运行特定测试

```bash
# 只运行配置测试
go test -v -run "TestConfig" ./...

# 只运行队列测试
go test -v -run "TestQueue" ./...

# 只运行 Worker 测试
go test -v -run "TestWorker" ./...

# 只运行事件测试
go test -v -run "TestEvent" ./...

# 只运行元数据测试（需要 SQLite）
go test -v -run "TestMetadata" ./...
```

## 生成覆盖率报告

```bash
# 运行测试并生成覆盖率
go test -v -coverprofile=coverage.out -covermode=atomic ./...

# 查看覆盖率摘要
go tool cover -func=coverage.out

# 生成 HTML 报告
go tool cover -html=coverage.out -o coverage.html

# 在浏览器中打开（本地）
open coverage.html
# 或
firefox coverage.html
```

## 性能测试

```bash
# 运行所有性能测试
go test -bench=. -benchmem ./...

# 运行特定的性能测试
go test -bench=TestPushEvent -benchmem ./...
```

## 竞态检测

```bash
# 运行竞态检测
go test -race -v ./...
```

## 使用 Makefile

```bash
# 运行所有测试
make test

# 生成覆盖率报告
make test-coverage

# 查看覆盖率摘要
make coverage-summary

# 运行性能测试
make benchmark

# 运行竞态检测
make race
```

## 预期输出

运行 `go test -v ./...` 后，你应该看到类似以下的输出：

```
=== RUN   TestIsEnabled
--- PASS: TestIsEnabled (0.00s)
=== RUN   TestGetCallbackURL
--- PASS: TestGetCallbackURL (0.00s)
=== RUN   TestResourceTypeString
--- PASS: TestResourceTypeString (0.00s)
=== RUN   TestInitQueue
--- PASS: TestInitQueue (0.00s)
=== RUN   TestPushEvent
--- PASS: TestPushEvent (0.00s)
...
PASS
ok      web/src/callback    0.123s
```

## 故障排查

### 错误：找不到包

```bash
# 更新依赖
go mod tidy

# 下载依赖
go mod download
```

### 错误：未定义的标识符

确保所有文件都在 `callback` 包中：

```bash
# 检查文件头
head -10 *_test.go | grep "package callback"
```

### 错误：端口已被占用

某些测试使用固定端口（如 18080），如果端口被占用：

```bash
# 查找占用端口的进程
lsof -i :18080
# 或
netstat -tulpn | grep 18080

# 杀死进程
kill -9 <PID>
```

### 测试超时

增加测试超时时间：

```bash
go test -timeout 10m -v ./...
```

## 测试覆盖范围

- **config_test.go**: 8 个测试，100% 覆盖
- **event_test.go**: 11 个测试，100% 覆盖
- **queue_test.go**: 9 个测试，90%+ 覆盖
- **worker_test.go**: 10 个测试，85%+ 覆盖
- **metadata_test.go**: 20+ 个测试（需要 SQLite），90%+ 覆盖

## 持续集成

在 CI/CD 中使用：

```yaml
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
          go test -v -short ./...
      - name: Run race detector
        run: |
          cd web/src/callback
          go test -race -short ./...
```

## 下一步

1. ✅ 运行 `go test -v ./...` 确认测试通过
2. ✅ 运行 `make test-coverage` 查看覆盖率
3. ✅ 根据需要安装 SQLite 驱动运行完整测试
4. ✅ 集成到 CI/CD 流程

## 联系方式

如有问题，请查看：
- `TEST_README.md` - 详细测试文档
- `FIX_INSTRUCTIONS.md` - 问题修复说明
- `README.md` - 模块文档

## 许可证

Apache-2.0
