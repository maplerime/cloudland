# 高 CPU VM 本地检测与 CPU 隔离

智能 CPU firing/limit_cpu webhook 复用原告警过滤，通过 SCI 通知指定 VM 所在计算节点。
原 CPU quota 限速、恢复和原通知独立执行。检测不等待限速完成。

## 运行流程

```text
webhook → SCI → kvm/inspect_vm.sh → 原 async_exec
  → async_job/inspect_vm.sh → runners/inspect_vm.sh
      → common 下原 PoC 的 RPC、解锁、进程、CUPS、时间和 WDS 查询
  → common/evaluate_inspection.sh 判断扩展点
  → store.sh 保存完整报告到计算节点
  → confirmed 时调用 common/quarantine_vm.sh，检查本轮自动隔离开关
  → 每轮完成后发布通知指标（未比对、未命中、采集失败也通知）
      → 重新查询 WDS → 排除全部绑定 CPU 及 SMT 兄弟 → 保存原亲和性 → 隔离并核验
  → 作业执行结束 → .done / report_rc 回收 → 清理本轮完整报告
  → 每次扫描的结构化采集结果及可选处置结果 → SCI → clbase 入库
  → 本轮检测结果指标 → Prometheus / Alertmanager / Teams
```

结构化进程、文件和 WDS 结果通过原分片回调回收；原始命令输出保留在节点完整报告中。
判断函数目前固定返回 `not_evaluated`，没有特征库。普通高 CPU 扫描仍回收结构化采集结果并入库；未产生的判断、隔离状态和匹配说明为空。
手动隔离不会被表述为病毒确诊。

## 文件职责

| 文件 | 功能 |
|---|---|
| runners/inspect_vm.sh | 按告警目标调用原 PoC 的独立 common 脚本 |
| common/check_guest_rpc_locked.sh、unlock_guest_exec_status.sh | RPC 查询、必要时解锁并复查 |
| common/vm_top_cpu_procs.sh | 前 N 个高 CPU 进程，默认十个 |
| common/outer_check.sh、inner_check.sh、inner_stat_check.sh | guest 内 CUPS 特征、文件与修改时间查询 |
| common/check_uss_core_bind.sh | 沿用 PoC 的 USS 查询，支持超过 64 位的 CPU mask |
| result.sh | 解析各项采集结果，构造完整本地 JSON |
| store.sh、report.sh | 报告按 VM 保留最新一份；完成结果回收后清理对应报告，保留处置及恢复状态 |
| common/evaluate_inspection.sh、evaluation.sh | 未来统一特征库的空判断函数和返回结构校验 |
| metrics.sh、metrics.py | 本轮检测通知摘要、最多 10 项进程、指标发布和 report_rc 回收后延迟清理 |
| common/select_quarantine_cpus.sh | 使用新查询的 WDS 信息、在线 CPU、QEMU/服务允许的集合选核 |
| isolation.sh、common/cpu_affinity.py | 保存、应用和恢复 live/config 亲和性；核验 vCPU、emulator 和 IOThread |
| common/quarantine_vm.sh、common/release_vm.sh | 独立执行隔离和释放，复用同一把 VM 锁及原始恢复文件 |
| report.sh | 保存处置摘要并输出有界 SCI 分片；支持内部重读 |
| metrics.sh | 发布每轮检测通知，供 report_rc 标记和延迟清理 |

PoC 的进程、RPC、CUPS 和文件查询仍按独立文件维护。guest 不需要安装常驻服务。
检测下发 VM UUID、domain、有效隔离开关和特征库地址；用户、组织、镜像等资料不放入扫描参数或结果。
未配置地址时控制端下发 NONE，异步检测脚本将其转为空值并跳过比对；公共 cloudrc/async_exec 沿用原始行为。NONE 仅为调用占位，不保存到 DB 或报告中。
节点编号复用原 SCI_CLIENT_ID 环境，回调实际来源仍由 SCI extra 提供。
进程 CPU 是 ps 的进程生命周期平均百分比；文件 mtime 是时间线证据，不等同入侵时间。

## 采集结果

- `cputop_processes`：按 CPU 排序的实际运行进程（数量由 top_n 表示），含用户、PID、CPU 百分比和进程名。
- `required_check.cupsd`：独立的 CUPS 软件包、文件、cron、隐藏目录、连接与修改时间检查。包未登记安装不等于没有 cupsd 进程。
- Agent、步骤状态、WDS 绑核、错误和时间独立记录；完整原始输出仅在节点报告中。

## 数据位置和配置

- 完整报告：`/opt/cloudland/cache/vm_inspection/reports/<instance_uuid>/<UTC采集时间>.json.gz`，内容带 scan_id。
- 当前处置摘要：`/opt/cloudland/cache/vm_inspection/state/<instance_uuid>/result.json`。
- 原始亲和性：同一 state 目录下的 affinity.json；隔离或恢复未完成时不清理。
- 路径复用原 cloudrc 的 cache_dir/run_dir；不增加路径配置。
- 存储分支复用原 cloudrc.local 的 wds_address：为空是本地盘，非空查询 WDS；查询超时脚本内部固定 10 秒。报告不使用保留天数或独立清理 cron。
- 每台 VM 只保留最新完整报告。`report_rc` 回收已完成输出后，仅删除该 scan_id 对应的报告。
- 自动开关及管理记录保留天数：管理 DB `vm_inspection_settings`；节点优先、全局其次，默认 false，管理记录默认七天。
- 管理记录复用 `vm_infection_records`：保存最新结构化采集结果、采集完成时间，以及可选的特征库版本、匹配说明和隔离状态。原始输出不入库。
- 同一处理周期更新原记录；没有特征库时也生成记录。新扫描更新采集时间，单独保留既有处置时间和恢复状态。UI/API 展示两类信息。

每台计算节点最多同时运行 6 个检测；满额等待任一空闲槽位，取得槽位后才开始 90 秒采集预算。
管理接入 `vm_inspection.enabled` 缺省 true，显式 false 才关闭；部署模板仅在关闭时输出这一项。
木马规则复用 node alarm 的 `rule_type=vm_malware` 和 config JSON；Teams 沿用 slack_security_webhook_url，不增加接收地址或分组时间配置。

节点使用现有 scripts 整目录发布，依赖 jq、Python 3、coreutils、util-linux 和现有 virsh。
WDS 查询失败或无法确认可用 CPU 时不隔离。
自动隔离开关控制后续任务，关闭不释放已有隔离；人工隔离/释放不受自动开关限制。
释放仅恢复 CPU 亲和性，不取消原智能 CPU quota。亲和性恢复只合并对应字段，不恢复旧整份 XML；
与现有 adjust_cpu_hotplug.sh 共用 per-domain 锁，保护同时进行的 quota 配置更新。

完整报告默认上限 64 KiB；内部结构化结果上限 64 KiB，每片 768 个 Base64 字符、最多 128 片，单行最多 1022 字节。
report_rc 只读取 `.done`，读取完成不代表数据库已经保存。UI/API 直接查询管理数据库，
不再提供从计算节点手动重传结果的接口或同步按钮。用户没有创建/改写结果的 API。

扫描、隔离/释放共用 per-VM 处理锁；清理遇到处理锁占用时保留 .done，
由下一轮既有 report_rc 继续回收。完整流程结束前不输出清理标记，`.in_progress` 不参与回收。
清理只处理 reports 内对应的 scan_id，不删除 state/result.json 或 affinity.json。
同一内容的分片须到同一 clbase 进程；在途缓冲不持久化。库写失败不标记已保存。
管理记录保留期以最新采集或处置时间判断；空隔离状态允许正常删除和到期清理。
用户通过 UI/API 删除记录和到期清理都执行物理删除，不保留整行数据。仍需恢复 CPU 绑定的记录继续保护。
普通旧回调可以重新创建记录，保留期仍按其原采集/处置时间计算；重新入库时间不延长保留期。

每轮完成后导出 vm_inspection_completed_timestamp_seconds / vm_inspection_info。指标按 VM 保存最新文件；report_rc 回收后核对 scan_id 并标记，发布满 300 秒再由后续周期清理。规则读取 for_duration＋300 秒的历史窗口。手动同步/隔离/释放不重发检测通知，也不提前删通知指标；恢复不发送 Teams 消息。
亲和性命令依据：[libvirt virsh](https://www.libvirt.org/manpages/virsh.html)。

接口和采集时间统一使用 UTC RFC3339（Z），页面明确显示 UTC。状态及错误归入各自采集分组；隔离/释放字段在 API 中组成 isolation_result。节点名称按原 hyper_id 查询，不新增数据库列。NA/NONE 仅用于页面显示：无数据 NA、明确无错误 NONE、失败保留原因，false/0 等有效值保留。

特征库地址保存于管理 DB vm_inspection_settings 的全局行 feature_library_url，通过原设置 UI/API 修改；默认空，PATCH 不提交则保留原值，提交空字符串则清空。每次检测下发四个参数：VM UUID、domain、有效隔离开关、特征库 URL（未配置也传空参数）。节点 URL 为空或只有空白时跳过比对；非空时调用 evaluate_inspection <采集JSON文件> <URL>。函数当前固定返回 {"verdict":"not_evaluated","ruleset_version":"","match_rule_ids":[],"match_summary":""}，保留完整输入输出契约，不访问特征库。采集、回收和入库继续；只有未来 confirmed 且开关开启才自动隔离。不写 cloudrc.local，不由 Ansible 覆盖比对脚本。

2026-10-19：本轮通知与指标清理流程已更新代码，未运行测试。规则继续由原 node-alarm-rules API 创建；已有规则须重新生成。消息不发送完整命令行或 DB JSON，只有实际最多 10 项进程及比对/隔离摘要。
