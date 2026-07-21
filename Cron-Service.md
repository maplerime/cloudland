# Cron-Service 开发设计文档

> 版本：v1.0 | 日期：2026-06-04 | 状态：POC → 生产就绪

---

## 一、概述

Cron-Service 是 Cloudland 平台的定时任务调度引擎，支持对**实例**和**卷**进行自动化定时操作。

### 1.1 核心能力

| 能力 | 描述 |
|------|------|
| **实例操作** | 定时 start / stop / hard_stop / restart / hard_restart |
| **卷备份** | 定时 snapshot / backup，支持保留策略（retention） |
| **灵活调度** | one-time（一次性）/ daily / weekly / monthly，支持标准 cron 表达式 |
| **分布式锁** | 基于数据库的乐观锁，防止多实例重复执行 |
| **执行历史** | 每次执行记录状态、耗时、错误信息 |

### 1.2 技术栈

| 组件 | 选型 |
|------|------|
| 调度框架 | `gocron` — 每分钟 ticker |
| cron 解析 | `robfig/cron/v3` — 5-field parser |
| 分布式锁 | PostgreSQL `locks` 表 + unique constraint |
| 数据持久化 | GORM + PostgreSQL |
| API 框架 | Gin（API）/ Macaron（Web） |

---

## 二、架构设计

```
┌──────────────────────────────────────────────────────────┐
│                   入口层 (main.go)                         │
│  g.Go(routes.RunScheduler)  — 阻塞式 goroutine            │
└──────────────────────┬───────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────┐
│              调度引擎 (scheduler.go)                       │
│  RunScheduler()                                          │
│    └── gocron.Every(1).Minute()                          │
│          └── checkAndRunTasks()                          │
│                ├── ListEnabledTasks()                    │
│                ├── shouldRun(task)? — cron 判定           │
│                ├── tryLock(taskID)  — DB 分布式锁         │
│                └── go runTask(task)  — 异步执行            │
│                      ├── CreateAdminContext (权限提升)     │
│                      ├── context.WithTimeout(30min)      │
│                      ├── instance_op → runInstanceOperation │
│                      └── volume_backup → runVolumeBackup  │
│                          └── handleRetention (保留策略)    │
└──────────────────────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────┐
│             管理接口层 (scheduled_task.go)                  │
│  ScheduledTaskAdmin: CRUD 业务逻辑                        │
│    ├── Create() + validateScheduledTaskConfig() 输入校验  │
│    │     + validateScheduledTaskResource() 资源确认       │
│    ├── List/Get —— 组织隔离分页查询                       │
│    ├── Update(ScheduledTaskUpdateOptions) —— 选择性更新    │
│    ├── Delete                                           │
│    ├── ListEnabledTasks —— 调度器专用，跨组织查询          │
│    └── ListHistory —— 执行历史（支持 task_id=0 全量）      │
│  ScheduledTaskHistoryAdmin → 委托给 ScheduledTaskAdmin     │
│  ScheduledTaskView → Web Console HTML                    │
└──────────────────────┬───────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────┐
│              API 层 (apis/scheduled_task.go)              │
│  POST   /api/v1/scheduled_tasks         — Create         │
│  GET    /api/v1/scheduled_tasks         — List           │
│  GET    /api/v1/scheduled_tasks/:id     — Get            │
│  PATCH  /api/v1/scheduled_tasks/:id     — Patch          │
│  DELETE /api/v1/scheduled_tasks/:id     — Delete         │
│  GET    /api/v1/scheduled_tasks/:id/history — ListHistory│
└──────────────────────┬───────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────┐
│              数据模型 (model/)                              │
│  ScheduledTask: 任务定义表                                 │
│  ScheduledTaskHistory: 执行历史表                          │
│  Lock: 分布式锁表 (unique constraint)                      │
└──────────────────────────────────────────────────────────┘
```

---

## 三、调度流程

### 3.1 `shouldRun()` 判定逻辑

```go
func shouldRun(task *ScheduledTask) bool {
    now := time.Now()

    switch task.ScheduleType {
    case "one-time":
        // 执行时间已过 → true (执行后自动 disabled)
        if task.ExecutionTime.IsZero() { return false }
        return !now.Before(task.ExecutionTime)

    case "daily", "weekly", "monthly":
        // cron 表达式为空 → false
        if task.CronExpression == "" { return false }
        // 已缓存的 schedule 直接用
        if v, ok := cronScheduleCache.Load(expr); ok {
            return v.Next(now - 1min).Before(now)
        }
        // 新解析并缓存
        sched := cronParser.Parse(expr)
        cronScheduleCache.Store(expr, sched)
        return sched.Next(now - 1min).Before(now)

    default: return false
    }
}
```

### 3.2 执行流程

```
checkAndRunTasks() [每分钟]
  │
  ├─ ListEnabledTasks() → 所有 status=enabled 的任务
  │
  ├─ for each task:
  │    ├─ shouldRun(task)? ──No──→ continue
  │    │
  │    ├─ tryLock(taskID):
  │    │    ├─ 已有锁 (fresh)  → continue (防重复)
  │    │    ├─ 已有锁 (stale)  → 清理后新建
  │    │    └─ 无锁            → 新建
  │    │
  │    └─ go runTask(task):           [异步]
  │         ├─ defer unlock(taskID)
  │         ├─ ctx = WithTimeout(30min)
  │         ├─ if one-time → defer disable(task)
  │         │
  │         ├─ switch task.TaskType:
  │         │    case instance_op:
  │         │      ├─ instanceAdmin.Get() + owner 校验
  │         │      └─ instanceAdmin.Update(action)
  │         │    case volume_backup:
  │         │      ├─ volumeAdmin.Get() + owner 校验
  │         │      ├─ CreateSnapshotByID / CreateBackupByID
  │         │      └─ handleRetention() — 清理旧备份
  │         │
  │         └─ defer recordTaskHistory(status, duration)
```

---

## 四、数据模型

### 4.1 `scheduled_tasks`

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | bigint | PK auto | 自增主键 |
| owner | bigint | index, default:1 | 组织 ID |
| name | varchar(128) | not null | 任务名称 |
| task_type | varchar(32) | | instance_op / volume_backup |
| resource_type | varchar(32) | | instance / volume |
| resource_id | bigint | | 目标资源 ID |
| operation | varchar(32) | | stop/hard_stop/start/restart/hard_restart/snapshot/backup |
| schedule_type | varchar(32) | | one-time / daily / weekly / monthly |
| execution_time | timestamp | | 一次性任务执行时间 |
| cron_expression | varchar(128) | | 重复任务 cron 表达式 |
| retention_count | int | default:0 | 备份保留数量（0=无限） |
| status | varchar(32) | | enabled / disabled |
| created_at | timestamp | | |
| updated_at | timestamp | | |
| deleted_at | timestamp | index | 软删除 |

### 4.2 `scheduled_task_histories`

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | bigint | PK auto | |
| scheduled_task_id | bigint | FK → scheduled_tasks | 关联任务 |
| status | varchar(32) | | success / failed |
| message | text | | 错误信息或成功消息 |
| execution_time | timestamp | | 实际执行时间 |
| duration | bigint | | 执行耗时（秒） |
| created_at | timestamp | | |

### 4.3 `locks`（分布式锁）

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | bigint | PK auto | |
| name | varchar(128) | UNIQUE | 锁名，格式：`scheduled_task_{id}` |
| created_at | timestamp | | 用于 TTL 判定 |

**锁 TTL**：`lockTTL = taskTimeout + 5min = 35min`，超过 TTL 的锁视为 stale，自动清理。

---

## 五、输入校验（validateScheduledTaskConfig）

| 校验项 | 规则 |
|--------|------|
| name | 必填，自动 trim |
| resource_id | > 0 |
| retention_count | ≥ 0 |
| status | "" / "enabled" / "disabled" |
| task_type | instance_op 或 volume_backup |
| resource_type | 必须与 task_type 匹配（instance ↔ instance_op; volume ↔ volume_backup） |
| operation | instance 任务: stop/hard_stop/start/restart/hard_restart<br>volume 任务: snapshot/backup |
| schedule_type | one-time / daily / weekly / monthly |
| one-time | execution_time 必填，cron_expression 清空 |
| daily/weekly/monthly | cron_expression 必填且合法（5-field cron），execution_time 清空 |

---

## 六、安全性

| 层面 | 机制 |
|------|------|
| **权限隔离** | 所有 CRUD 通过 `memberShip.GetWhere()` 做组织隔离 |
| **执行权限** | 调度器通过 `CreateAdminContext` 提升为管理员上下文执行任务 |
| **Owner 校验** | `runInstanceOperation` / `runVolumeBackup` 执行前验证 `resource.Owner == task.Owner` |
| **防重入** | DB 唯一约束保证同一任务同一时刻只有一个实例执行 |
| **崩溃恢复** | stale lock 自动清理（TTL 35min），进程重启后锁不残留 |

---

## 七、API 接口

### 7.1 创建任务

```
POST /api/v1/scheduled_tasks
Content-Type: application/json

{
    "name": "每日凌晨3点备份",
    "task_type": "volume_backup",
    "resource_type": "volume",
    "resource_id": 42,
    "operation": "backup",
    "schedule_type": "daily",
    "cron_expression": "0 3 * * *",
    "retention_count": 7
}
```

### 7.2 更新任务

```
PATCH /api/v1/scheduled_tasks/:id
Content-Type: application/json

{
    "name": "updated name",
    "status": "disabled",
    "schedule_type": "weekly",
    "cron_expression": "0 0 * * 1"
}
```

### 7.3 查询执行历史

```
GET /api/v1/scheduled_tasks/:id/history?offset=0&limit=20&order=-created_at

Response:
{
    "total": 42,
    "history": [
        {
            "id": 1,
            "scheduled_task_id": 100,
            "status": "success",
            "message": "Task executed successfully",
            "execution_time": "2026-06-04T03:00:05Z",
            "duration": 15,
            "scheduled_task": { "id": 100, "name": "..." }
        }
    ]
}
```

---

## 八、测试覆盖

| 类别 | 测试数 | 覆盖内容 |
|------|--------|----------|
| `shouldRun` 调度判定 | 10 | one-time(due/not-due/zero) daily(valid/invalid/empty) weekly/monthly/unknown |
| `validateScheduledTaskConfig` | 5 | one-time成功 重复任务成功 非法组合 非法cron 非法status |
| `parseScheduledTaskExecutionTime` | 3 | datetime-local 空值 非法格式 |
| 模型/常量 | 8 | STaskAction 7个常量 模型默认值 字段完整性 |
| 锁机制 | 3 | TTL正值 超时合理性 TTL>timeout关系 |
| cron 缓存 | 1 | 同表达式命中缓存无panic |
| Update 选择性 | 2 | 仅非空字段更新 全空=无更新 |
| 委托模式 | 1 | HistoryAdmin→TaskAdmin 编译检查 |
| **总计** | **31** | **全部 PASS** |

---

## 九、Roadmap（已知局限与后续规划）

### 9.1 当前已知局限

| 类别 | 当前现状 | 影响 |
|------|------|------|
| 结果反馈 | 调度结果仅记录到 DB / History，无外部通知 | 运维感知滞后，失败任务需要人工发现 |
| 超时处理 | `context.WithTimeout` 只能触发本地 cancel，无法保证远端脚本优雅终止 | 可能出现超时后底层任务仍继续执行 |
| 可观测性 | 缺少 Prometheus 指标、统一告警、失败趋势统计 | 难以做容量评估和故障定位 |
| 数据治理 | `scheduled_task_histories` 缺少自动清理策略 | 长期运行后历史表持续膨胀 |
| 执行动作范围 | 当前仅支持 `instance_op` 与 `volume_backup` | 自动化覆盖面有限 |
| 编排能力 | 不支持任务依赖、任务链、DAG | 无法表达复杂批处理或串行流程 |

### 9.2 优先级原则

- **P1**：直接提升生产可用性、可观测性、故障响应速度，且改动面较小。
- **P2**：提升能力边界或执行质量，需要新增模型、配置项或更复杂的流程控制。
- **P3**：增强型特性，收益明确，但不阻塞当前版本稳定上线。

### 9.3 分阶段 Roadmap

| 优先级 | 项目 | 目标 | 预期收益 |
|--------|------|------|------|
| P1 | 调度执行结果通知 | 任务成功/失败后主动通知 | 缩短故障发现时间 |
| P1 | 监控指标暴露 | 暴露运行数、成功率、失败率、耗时等指标 | 支撑监控面板与告警规则 |
| P1 | 调度历史自动清理 | 为 History 增加保留周期和定时清理 | 控制表增长，降低存储压力 |
| P2 | 超时优雅中断 | 超时后增加明确状态、错误码和中断语义 | 减少“超时但底层仍运行”的不确定性 |
| P2 | 更多 task_type | 增加 resize / migrate 等成熟动作 | 扩展自动化覆盖范围 |
| P2 | 重试与补偿策略 | 支持失败重试、重试间隔、错误分类 | 提升瞬时故障场景下的成功率 |
| P2 | misfire 策略 | 明确停机恢复后错过任务如何处理 | 提升调度语义的一致性 |
| P2 | 时区支持 | 任务级 timezone 配置与统一时间计算 | 避免跨时区误触发 |
| P3 | Webhook 通知 | 对接外部系统的标准回调 | 方便接入告警平台、审批流、审计系统 |
| P3 | 任务链 / DAG | 支持任务依赖与编排 | 支撑复杂自动化流程 |

### 9.4 P1 落地任务列表（建议当前迭代完成）

#### P1-1 调度执行结果通知

- 新增通知配置模型：支持全局开关、通知级别、接收目标。
- 在 `recordTaskHistory()` 后增加通知分发入口，避免在任务执行主流程里散落通知逻辑。
- 第一阶段只做**失败必通知、成功可选通知**，降低噪音。
- 通知内容至少包含：任务 ID、任务名、资源类型、资源 ID、状态、执行时间、耗时、错误消息。
- 增加通知失败日志，但通知失败不能影响主任务结果。

#### P1-2 监控指标暴露

- 增加基础指标：
  - `scheduled_task_running`
  - `scheduled_task_total`
  - `scheduled_task_success_total`
  - `scheduled_task_failed_total`
  - `scheduled_task_duration_seconds`
- 维度建议控制在最小集合：`task_type`、`operation`、`status`。
- 在任务开始、结束、失败、超时等关键路径统一上报，避免重复埋点。
- 输出一份 Prometheus 告警建议：
  - 连续失败告警
  - 失败率升高告警
  - 执行耗时异常告警

#### P1-3 调度历史自动清理

- 新增配置项：如 `scheduler.history_retention_days`。
- 增加每日定时清理任务，按 `created_at` 删除过期 History。
- 清理任务本身也要记录日志和清理条数，便于追踪。
- 先只清理 `scheduled_task_histories`，不直接清理 `scheduled_tasks` 主表。
- 为 `scheduled_task_histories` 核查索引：至少确认 `scheduled_task_id`、`created_at` 的查询性能。

### 9.5 P2 落地任务列表（建议下一迭代拆分实施）

#### P2-1 超时优雅中断

- 为 History 增加更清晰的超时语义：`failed(timeout)` 或新增标准错误码。
- 统一超时错误消息格式，便于前端和告警侧识别。
- 梳理远端脚本是否支持中断信号、取消标记或轮询退出。
- 若底层脚本暂不支持优雅退出，至少保证：
  - 锁正确释放
  - 状态正确记录
  - 不会被立即重复调度

#### P2-2 更多 task_type

- 先筛选“已有手工入口、执行语义稳定、失败后可观测”的动作进入调度器。
- 推荐顺序：
  1. `instance resize`
  2. `instance migrate`
  3. 其他成熟实例生命周期动作
- 每新增一个 `task_type`，同步补齐：
  - 输入校验
  - owner 校验
  - 执行 history
  - 最小单测
  - Web/API 表单联动

#### P2-3 重试与补偿策略

- 为任务增加可选字段：
  - `max_retries`
  - `retry_interval`
  - `retry_on`（可选，按错误类别）
- 仅对瞬时错误重试，不对配置错误/权限错误重试。
- History 中记录 `attempt` 次数，避免排障时信息丢失。

#### P2-4 misfire 策略

- 定义服务停机或调度阻塞后的补偿行为：
  - `skip`
  - `run_once_on_recover`
  - `catch_up_all`
- 第一版建议默认 `skip` 或 `run_once_on_recover`，避免恢复时任务风暴。
- 在文档和 UI 中明确该策略，否则用户容易误解调度语义。

#### P2-5 时区支持

- 为任务增加 `timezone` 字段，默认跟随系统或配置项。
- `shouldRun()`、cron 解析、页面展示统一按任务时区处理。
- 重点覆盖夏令时、跨时区、UTC/本地时间混用场景。

### 9.6 暂不建议当前阶段立即做的事项

- **任务链 / DAG**：收益大，但会显著抬高复杂度，涉及依赖关系、失败传播、循环检测、并发控制，不建议与当前稳定性优化混做。
- **大规模 task_type 扩张**：若过快增加动作类型，会把调度器拉成通用编排器，建议先保证一类动作做深做稳。

### 9.7 推荐实施顺序

1. P1：结果通知
2. P1：Prometheus 指标
3. P1：History 自动清理
4. P2：超时语义与优雅中断
5. P2：重试策略
6. P2：misfire 策略
7. P2：时区支持
8. P2：新增 task_type
9. P3：Webhook 扩展
10. P3：任务链 / DAG

---

## 十、文件清单

| 文件 | 行数 | 职责 |
|------|------|------|
| `web/src/routes/scheduler.go` | 405 | 调度引擎核心 |
| `web/src/routes/scheduled_task.go` | 693 | 管理逻辑 + Web View |
| `web/src/routes/scheduler_test.go` | 411 | 31 个单元测试 |
| `web/src/model/scheduled_task.go` | 60 | 数据模型 |
| `web/src/model/lock.go` | 19 | 分布式锁模型 |
| `web/src/apis/scheduled_task.go` | 216 | REST API |
| `web/src/apis/routes.go` | +10 | API 路由注册 |
| `web/src/routes/routes.go` | +10 | Web 路由注册 |
| `web/cmds/base/main.go` | +1 | 启动入口：`g.Go(RunScheduler)` |
| `web/conf/locale/locale_*.ini` | +50 | 中英文翻译 |
| `web/templates/scheduled_*.tmpl` | 4 文件 | Web Console 模板 |
