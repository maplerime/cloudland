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

### iaas - CloudLand IaaS API 快捷命令

用于测试和运维场景，封装常用 CloudLand REST API 调用。

```bash
# 查询 hypers
cloudland iaas --endpoint https://dev-sv01.raksmart.com --username admin --password '***' --insecure hypers --json

# 查询 instances
cloudland iaas --endpoint https://dev-sv01.raksmart.com --username admin --password '***' --insecure instances list --json

# 通过 payload 文件创建 instance
cloudland iaas --endpoint https://dev-sv01.raksmart.com --username admin --password '***' --insecure \
	instances create --payload-file /tmp/instance_payload.json --json

# 等待 instance 到 running
cloudland iaas --endpoint https://dev-sv01.raksmart.com --username admin --password '***' --insecure \
	instances wait <instance_uuid> --status running --timeout 900

# 删除 instance
cloudland iaas --endpoint https://dev-sv01.raksmart.com --username admin --password '***' --insecure \
	instances delete <instance_uuid>
```

## 排查客户 VM 高 CPU 进程

两步走：第一步在 hypervisor 上跑，抓出高 CPU VM 里的进程；第二步在管理机上跑，把
VM 反查成客户。

### 第一步：hypervisor 上抓 top 进程（`scripts/kvm/operation/vm_top_cpu_procs.sh`）

```bash
# 逐台 hypervisor 执行，自动找 qemu %CPU >= 50 的 VM，抓其内部 top10 进程 + 所属用户，
# 输出 CSV 存到以主机名区分的文件
ssh hyper01 'CSV=1 /opt/cloudland/scripts/kvm/operation/vm_top_cpu_procs.sh' > top_cpu_hyper01.csv
ssh hyper02 'CSV=1 /opt/cloudland/scripts/kvm/operation/vm_top_cpu_procs.sh' > top_cpu_hyper02.csv
# ...对所有 hypervisor 重复（用 ansible/pssh 批量跑更省事）

# 合并成一份表
{ echo "hostname,vm_id,host_qemu_pcpu,user,pid,proc_pcpu,comm"; cat top_cpu_hyper*.csv | grep -v '^hostname,'; } > all_top_cpu.csv
```

不带 `CSV=1` 直接跑是人读的文本格式；调阈值用 `CPU_THRESHOLD=80`，只想查某几台 VM
用 `./vm_top_cpu_procs.sh inst-42 inst-7`（跳过宿主机预过滤）。`-h` 看完整参数。

#### 部分 VM 抓不到进程：guest-exec-status 被禁用

有些 VM 的 qemu-ga 把 `guest-exec-status`（以及所有 `guest-file-*`）RPC 禁掉了——能
下发命令但读不到执行结果，这看着像是平台故意留的防窃听边界（能控制，不能窥探）。
`vm_top_cpu_procs.sh` 遇到这种 VM 会直接报 `guest-exec-status unavailable`，不会傻等
超时。

先用 `scripts/kvm/operation/check_guest_rpc_locked.sh` 批量找出这批 VM（纯只读，只调
`guest-info`，不改任何东西）：

```bash
# 本机所有运行中 VM，跳过 Windows，列出被锁的 vm_id
./check_guest_rpc_locked.sh

# CSV 模式，方便多台 hypervisor 汇总
CSV=1 ./check_guest_rpc_locked.sh   # hostname,vm_id,status（exec_status_disabled|agent_unreachable）
```

确认要解锁某台 VM 后，手动跑（`vm_top_cpu_procs.sh` 报错时也会打印同样这条命令）：

```bash
virsh qemu-agent-command <vm_ID> '{"execute":"guest-exec","arguments":{"path":"/bin/sh","arg":["-c","sed -i '\''s/--allow-rpcs=/--allow-rpcs=guest-exec-status,guest-file-open,guest-file-read,guest-file-close,/'\'' /etc/sysconfig/qemu-ga && systemctl restart qemu-guest-agent"]}}'
```

跑完等几秒，用 `virsh qemu-agent-command <vm_ID> '{"execute":"guest-info"}'` 确认
`guest-exec-status` 变成 `enabled:true` 再重新跑 `vm_top_cpu_procs.sh`。这是改客户 VM
内部配置并重启一个系统服务，必须每台单独人工确认，脚本不会自动执行。

### 第二步：VM 反查客户（`cloudland instance-owner`）

```bash
# 从 all_top_cpu.csv 的 vm_id 列取值查询，拿到 user_id/username/org_name
awk -F, 'NR>1{print $2}' all_top_cpu.csv | sort -u | xargs cloudland instance-owner --json
```

把结果按 `vm_id` 和 `all_top_cpu.csv` 关联，就是「客户 VM → 高 CPU 进程 → 所属用户」
的完整表，可以丢进 Excel 或 `sort -t, -k6 -rn all_top_cpu.csv` 直接看全局 CPU 大户。
