# CloudLand CLI

CloudLand 运维 CLI 工具集。

## 安装

```bash
cd tools/

# 一键安装（创建 venv 并安装）
make install
```

安装完成后使用方式：

```bash
# 方式一：直接调用 venv 中的命令
.venv/bin/cloudland --help

# 方式二：激活 venv 后使用
source .venv/bin/activate
cloudland --help
```

其他 make 命令：

```bash
make clean    # 清理 venv 和构建产物
make help     # 查看帮助
```

## 配置

复制并编辑配置文件：

```bash
cp config.toml.example config.toml
vim config.toml
```

`config.toml` 示例：

```toml
[wds]
address = "https://wds-server:port"
admin = "admin"
password = "xxx"

[cloudland]
config_path = "../web/conf/config.toml"
```

- `[wds]`: WDS 分布式存储服务连接信息
- `[cloudland].config_path`: CloudLand 配置文件路径，工具从中读取 `[db]` 段获取数据库连接

WDS 连接信息也可以通过命令行参数指定（优先级高于配置文件）：

```bash
cloudland --wds-address="https://wds:port" --wds-user=admin --wds-password=xxx <command>
```

## 子命令

### clean - 僵尸资源排查与清理

扫描并清理 WDS 中的僵尸资源。默认 dry-run 模式仅报告，加 `--execute` 执行实际删除。

**断点续查**: Volume 扫描按 ID 从小到大逐个检查 WDS，每检查一个 volume 都会保存进度（checkpoint）。如果中途中断（Ctrl+C、网络超时等），下次运行会自动从上次中断的位置继续，已发现的僵尸资源也会保留。

扫描结果缓存在 `.cache/` 目录。`--execute` 会直接使用缓存的僵尸列表进行删除，无需重新扫描。使用 `--no-cache` 清除缓存从头开始。

#### 典型工作流

```bash
# 第一步：dry-run 扫描（耗时），进度自动保存
cloudland clean volumes --boot
# 如果中途中断，再次运行会自动从断点继续
cloudland clean volumes --boot

# 第二步：扫描完成后确认报告，执行删除（使用缓存，无需重新扫描）
cloudland clean volumes --boot --execute

# 如需清除缓存从头重新扫描
cloudland clean volumes --boot --no-cache
```

#### clean volumes

清理已在数据库中软删除但仍存在于 WDS 的僵尸 volume。

```bash
# 扫描所有僵尸 volume（dry-run）
cloudland clean volumes --all

# 仅扫描 boot volume
cloudland clean volumes --boot

# 仅扫描 data volume
cloudland clean volumes --data

# 实际执行删除（优先使用缓存）
cloudland clean volumes --all --execute

# 强制重新扫描并删除
cloudland clean volumes --all --execute --no-cache
```

#### clean images

清理 WDS 中没有 clone 卷的孤儿 image snapshot。

```bash
# 扫描孤儿 image snapshot（dry-run）
cloudland clean images --snapshot

# 实际执行删除（优先使用缓存）
cloudland clean images --snapshot --execute

# 强制重新扫描并删除
cloudland clean images --snapshot --execute --no-cache
```

### 通用选项

| 选项 | 说明 |
|------|------|
| `-c, --config` | 配置文件路径（默认 `config.toml`） |
| `-v, --verbose` | 启用详细日志 |
| `--wds-address` | WDS 服务器地址（覆盖配置文件） |
| `--wds-user` | WDS 管理员用户名（覆盖配置文件） |
| `--wds-password` | WDS 管理员密码（覆盖配置文件） |

### clean 子命令选项

| 选项 | 说明 |
|------|------|
| `--execute` | 实际执行删除（默认 dry-run 仅报告） |
| `--no-cache` | 忽略缓存，强制重新扫描 |
