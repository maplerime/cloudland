# 一致性组

## 需求场景

1. 公有云用户使用 IaaS 创建了多台虚拟机，并用于构建数据库集群，在做快照时，需要将多台虚拟机的磁盘同时做一组快照，恢复时，需要将多台虚拟机的磁盘同时恢复到一致的状态。
2. 公有云用户使用 IaaS 创建了一台虚拟机，并挂载了多块磁盘，在做快照时，需要将多块磁盘同时做一组快照，恢复时，需要将多块磁盘同时恢复到一致的状态。

## 实现方式

### 一致性组管理功能

1. 在 cloudland 中，提供一致性组接口，用户可以创建一致性组，将多台虚拟机的磁盘或多个磁盘加入一致性组，可以对组内磁盘成员进行管理，包括添加、删除、修改等操作。
   - 创建一致性组：
     ```
     POST /v1/consistency-groups
     ```
     ```json
     {
         "name": "cg-1",
         "description": "consistency group 1"
         "volumes": [
             "volume-UUID-1",
             "volume-UUID-2"
         ]
     }
     ```
   - 删除一致性组：
     ```
     DELETE /v1/consistency-groups/{cg-UUID}
     ```
   - 添加磁盘到一致性组：
     ```
     POST /v1/consistency-groups/{cg-UUID}/volumes
     ```
     ```json
     {
       "volumes": ["volume-UUID-1", "volume-UUID-2"]
     }
     ```
   - 删除磁盘从一致性组：
     ```
     DELETE /v1/consistency-groups/{cg-UUID}/volumes/{volume-UUID}
     ```
   - 修改一致性组：
     ```
     PUT /v1/consistency-groups/{cg-UUID}
     ```
     ```json
     {
       "name": "cg-1",
       "description": "consistency group 1"
     }
     ```

**注意：**

- 添加/修改/删除一致性组中的磁盘成员时，需检查该组是否有快照，如果有快照，则不能修改组内成员。
- 一致性组 volumes 必须在同一个存储池中。
- 在添加成员时，需检查 volume 状态是否正常。
- 在修改一致性组时，需要检查是否正在做快照，如果正在做快照，则不能修改一致性组。

2. 一致性组管理功能，需要调用相关的 WDS 一致性组接口

   - POST /api/v2/sync/block/consistency-groups 同步创建一致性组
     - 传给 WDS 参数的名称使用 "cg\_<cloudland CG UUID>"
   - GET /api/v2/sync/block/consistency-groups/{cg_id} 同步获取一致性组详情
   - PUT /api/v2/sync/block/consistency-groups/{cg_id}/add_volumes 同步添加磁盘到一致性组
   - PUT /api/v2/sync/block/consistency-groups/{cg_id}/remove_volumes 同步删除磁盘从一致性组
   - GET /api/v2/sync/block/consistency-groups/{cg_id}/volumes 同步获取一致性组磁盘列表

3. 一致性组相关数据需要保存在 cloudland 数据库中，数据记录中需要保存对应 WDS 的 cg_id

### 一致性组快照功能

注：一致性组只提供快照功能，不提供备份和跨池备份功能。

1. cloudland 接口：
   - 创建一致性组快照：
     ```
     POST /v1/consistency-groups/{cg-UUID}/snapshots
     ```
     ```json
     {
       "name": "snapshot-1",
       "description": "snapshot 1"
     }
     ```
   - 获取一致性组快照列表：
     ```
     GET /v1/consistency-groups/{cg-UUID}/snapshots
     ```
   - 获取一致性组快照详情：
     ```
     GET /v1/consistency-groups/{cg-UUID}/snapshots/{snapshot-UUID}
     ```
   - 删除一致性组快照：
     ```
     DELETE /v1/consistency-groups/{cg-UUID}/snapshots/{snapshot-UUID}
     ```
   - 恢复一致性组快照：
     ```
     PUT /v1/consistency-groups/{cg-UUID}/restore
     ```
     ```json
     {
       "snapshot_id": "snapshot-UUID",
       "force": true
     }
     ```

**注意：**

- 创建快照时需要检查成员 volume 是否 busy，如果 busy，则不能创建快照。
- 创建快照过程中需要设置 cg 的快照状态为 pending，快照创建完成后设置 cg 的快照状态为 available。同时需要设置成员 volume 的状态为“backuping”，快照创建完成后设置成员 volume 的状态为之前状态。

2. 需调用的 WDS 接口
   - POST /api/v2/sync/block/cg_snaps/ 同步创建一致性组快照
   - GET /api/v2/sync/block/cg_snaps/{cg_snap_id} 同步获取一致性组快照详情
   - DELETE /api/v2/sync/block/cg_snaps/{cg_snap_id} 同步删除一致性组快照
   - PUT /api/v2/sync/block/consistency_groups/{cg_id}/recovery 同步恢复一致性组快照

### 数据模型

#### 1. ConsistencyGroup 模型

```go
package model

import (
	"web/src/dbs"
)

type CGStatus string

const (
	CGStatusPending   CGStatus = "pending"   // 创建中
	CGStatusAvailable CGStatus = "available" // 可用
	CGStatusError     CGStatus = "error"     // 错误
	CGStatusUpdating  CGStatus = "updating"  // 更新中
	CGStatusDeleting  CGStatus = "deleting"  // 删除中
)

func (s CGStatus) String() string {
	return string(s)
}

// ConsistencyGroup 一致性组模型
type ConsistencyGroup struct {
	Model
	Owner       int64    `gorm:"default:1;index"` // 组织 ID
	Name        string   `gorm:"type:varchar(128)"`
	Description string   `gorm:"type:varchar(512)"`
	Status      CGStatus `gorm:"type:varchar(32)"`
	PoolID      string   `gorm:"type:varchar(128)"` // 存储池 ID，组内所有 volume 必须在同一个池
	WdsCgID     string   `gorm:"type:varchar(128)"` // WDS 一致性组 ID
}

// IsBusy 检查一致性组是否处于繁忙状态
func (cg *ConsistencyGroup) IsBusy() bool {
	if cg.Status == CGStatusPending ||
		cg.Status == CGStatusUpdating ||
		cg.Status == CGStatusDeleting {
		return true
	}
	return false
}

// IsAvailable 检查一致性组是否可用
func (cg *ConsistencyGroup) IsAvailable() bool {
	return cg.Status == CGStatusAvailable
}

// IsError 检查一致性组是否处于错误状态
func (cg *ConsistencyGroup) IsError() bool {
	return cg.Status == CGStatusError
}

// CanDelete 检查一致性组是否可以删除
func (cg *ConsistencyGroup) CanDelete() bool {
	return !cg.IsBusy()
}

// CanUpdate 检查一致性组是否可以更新
func (cg *ConsistencyGroup) CanUpdate() bool {
	return cg.IsAvailable()
}
```

#### 2. ConsistencyGroupVolume 模型（关联表）

```go
// ConsistencyGroupVolume 一致性组与卷的关联表
type ConsistencyGroupVolume struct {
	Model
	CGID     int64              `gorm:"index"` // 一致性组 ID
	CG       *ConsistencyGroup  `gorm:"foreignkey:CGID"`
	VolumeID int64              `gorm:"index"` // 卷 ID
	Volume   *Volume            `gorm:"foreignkey:VolumeID"`
}
```

#### 3. ConsistencyGroupSnapshot 模型

```go
type CGSnapshotStatus string

const (
	CGSnapshotStatusPending   CGSnapshotStatus = "pending"   // 创建中
	CGSnapshotStatusAvailable CGSnapshotStatus = "available" // 可用
	CGSnapshotStatusError     CGSnapshotStatus = "error"     // 错误
	CGSnapshotStatusRestoring CGSnapshotStatus = "restoring" // 恢复中
	CGSnapshotStatusDeleting  CGSnapshotStatus = "deleting"  // 删除中
)

func (s CGSnapshotStatus) String() string {
	return string(s)
}

// ConsistencyGroupSnapshot 一致性组快照模型
type ConsistencyGroupSnapshot struct {
	Model
	Owner       int64                `gorm:"default:1;index"` // 组织 ID
	Name        string               `gorm:"type:varchar(128)"`
	Description string               `gorm:"type:varchar(512)"`
	Status      CGSnapshotStatus     `gorm:"type:varchar(32)"`
	CGID        int64                `gorm:"index"` // 一致性组 ID
	CG          *ConsistencyGroup    `gorm:"foreignkey:CGID"`
	Size        int64                // 快照总大小（所有卷快照大小之和）
	WdsSnapID   string               `gorm:"type:varchar(128)"` // WDS 一致性组快照 ID
	TaskID      int64                `gorm:"index"`             // 关联的任务 ID
	Task        *Task                `gorm:"foreignkey:TaskID"`
}

// CanDelete 检查一致性组快照是否可以删除
func (cgs *ConsistencyGroupSnapshot) CanDelete() bool {
	return cgs.Status != CGSnapshotStatusRestoring &&
	       cgs.Status != CGSnapshotStatusPending &&
	       cgs.Status != CGSnapshotStatusDeleting
}

// CanRestore 检查一致性组快照是否可以恢复
func (cgs *ConsistencyGroupSnapshot) CanRestore() bool {
	return cgs.Status == CGSnapshotStatusAvailable
}

// IsBusy 检查一致性组快照是否处于繁忙状态
func (cgs *ConsistencyGroupSnapshot) IsBusy() bool {
	if cgs.Status == CGSnapshotStatusPending ||
		cgs.Status == CGSnapshotStatusRestoring ||
		cgs.Status == CGSnapshotStatusDeleting {
		return true
	}
	return false
}

func init() {
	dbs.AutoMigrate(&ConsistencyGroup{}, &ConsistencyGroupVolume{}, &ConsistencyGroupSnapshot{})
}
```

#### 4. 错误码定义

在 `web/src/common/error_codes.go` 中添加一致性组相关错误码：

```go
// Consistency Group related errors (1252xx)
ErrCGNotFound                     ErrCode = 125200
ErrCGCreationFailed               ErrCode = 125201
ErrCGUpdateFailed                 ErrCode = 125202
ErrCGDeleteFailed                 ErrCode = 125203
ErrCGInvalidState                 ErrCode = 125204
ErrCGIsBusy                       ErrCode = 125205
ErrCGSnapshotExists               ErrCode = 125206
ErrCGVolumeNotInSamePool          ErrCode = 125207
ErrCGVolumeIsBusy                 ErrCode = 125208
ErrCGVolumeInvalidState           ErrCode = 125209
ErrCGSnapshotNotFound             ErrCode = 125210
ErrCGSnapshotCreationFailed       ErrCode = 125211
ErrCGSnapshotDeleteFailed         ErrCode = 125212
ErrCGSnapshotRestoreFailed        ErrCode = 125213
ErrCGSnapshotIsBusy               ErrCode = 125214
ErrCGCannotModifyWithSnapshots    ErrCode = 125215
```

### 测试用例

#### 1. 一致性组管理测试

##### 1.1 创建一致性组

**测试场景：正常创建一致性组**

- 前置条件：
  - 用户已登录并有权限
  - 存储池 pool-1 存在
  - 卷 volume-1 和 volume-2 已创建并在 pool-1 中，状态为 available
- 测试步骤：
  1. 发送 POST 请求到 `/v1/consistency-groups`
  2. 请求体包含 name、description、volumes（包含 volume-1 和 volume-2 的 UUID）
- 预期结果：
  - HTTP 状态码：200
  - 返回一致性组信息，包含 UUID、名称、描述、状态（pending）
  - 数据库中创建 ConsistencyGroup 记录
  - WDS 中成功创建一致性组
  - 最终状态变为 available

**测试场景：创建一致性组失败 - 卷不在同一存储池**

- 前置条件：
  - volume-1 在 pool-1 中
  - volume-2 在 pool-2 中
- 测试步骤：
  1. 发送 POST 请求创建一致性组，包含 volume-1 和 volume-2
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125207 (ErrCGVolumeNotInSamePool)
  - 错误信息：卷不在同一个存储池中

**测试场景：创建一致性组失败 - 卷状态不正常**

- 前置条件：
  - volume-1 状态为 available
  - volume-2 状态为 error
- 测试步骤：
  1. 发送 POST 请求创建一致性组
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125209 (ErrCGVolumeInvalidState)
  - 错误信息：卷状态不正常

##### 1.2 获取一致性组列表

**测试场景：获取一致性组列表**

- 前置条件：
  - 已创建 3 个一致性组
- 测试步骤：
  1. 发送 GET 请求到 `/v1/consistency-groups?offset=0&limit=10`
- 预期结果：
  - HTTP 状态码：200
  - 返回一致性组列表，包含总数、偏移量、限制数量和一致性组数组
  - 每个一致性组包含基本信息

##### 1.3 获取一致性组详情

**测试场景：获取一致性组详情**

- 前置条件：
  - 一致性组 cg-1 已创建
- 测试步骤：
  1. 发送 GET 请求到 `/v1/consistency-groups/{cg-1-UUID}`
- 预期结果：
  - HTTP 状态码：200
  - 返回一致性组详细信息，包含所有字段和关联的卷列表

##### 1.4 修改一致性组

**测试场景：修改一致性组名称和描述**

- 前置条件：
  - 一致性组 cg-1 已创建，状态为 available
  - 该组没有快照
- 测试步骤：
  1. 发送 PUT 请求到 `/v1/consistency-groups/{cg-1-UUID}`
  2. 请求体包含新的 name 和 description
- 预期结果：
  - HTTP 状态码：200
  - 返回更新后的一致性组信息
  - 数据库中记录已更新

**测试场景：修改一致性组失败 - 存在快照**

- 前置条件：
  - 一致性组 cg-1 已创建
  - 该组有一个快照
- 测试步骤：
  1. 发送 PUT 请求修改一致性组
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125206 (ErrCGSnapshotExists)
  - 错误信息：一致性组有快照，不能修改

##### 1.5 添加卷到一致性组

**测试场景：添加卷到一致性组**

- 前置条件：
  - 一致性组 cg-1 已创建，包含 volume-1
  - volume-2 在同一存储池，状态为 available
  - 该组没有快照
- 测试步骤：
  1. 发送 POST 请求到 `/v1/consistency-groups/{cg-1-UUID}/volumes`
  2. 请求体包含 volumes 数组，包含 volume-2 的 UUID
- 预期结果：
  - HTTP 状态码：200
  - volume-2 成功添加到一致性组
  - WDS 中成功添加卷

**测试场景：添加卷失败 - 存在快照**

- 前置条件：
  - 一致性组 cg-1 有快照
- 测试步骤：
  1. 尝试添加 volume-2
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125206 (ErrCGSnapshotExists)

**测试场景：添加卷失败 - 卷忙碌**

- 前置条件：
  - volume-2 状态为 backuping
- 测试步骤：
  1. 尝试添加 volume-2
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125208 (ErrCGVolumeIsBusy)

##### 1.6 从一致性组删除卷

**测试场景：从一致性组删除卷**

- 前置条件：
  - 一致性组 cg-1 包含 volume-1 和 volume-2
  - 该组没有快照
- 测试步骤：
  1. 发送 DELETE 请求到 `/v1/consistency-groups/{cg-1-UUID}/volumes/{volume-2-UUID}`
- 预期结果：
  - HTTP 状态码：204
  - volume-2 从一致性组中移除
  - WDS 中成功删除卷

**测试场景：删除卷失败 - 存在快照**

- 前置条件：
  - 一致性组 cg-1 有快照
- 测试步骤：
  1. 尝试删除 volume-2
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125206 (ErrCGSnapshotExists)

##### 1.7 删除一致性组

**测试场景：删除一致性组**

- 前置条件：
  - 一致性组 cg-1 已创建，状态为 available
  - 该组没有快照
- 测试步骤：
  1. 发送 DELETE 请求到 `/v1/consistency-groups/{cg-1-UUID}`
- 预期结果：
  - HTTP 状态码：204
  - 一致性组从数据库中删除
  - WDS 中成功删除一致性组

**测试场景：删除一致性组失败 - 存在快照**

- 前置条件：
  - 一致性组 cg-1 有快照
- 测试步骤：
  1. 尝试删除一致性组
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125206 (ErrCGSnapshotExists)
  - 错误信息：一致性组有快照，不能删除

**测试场景：删除一致性组失败 - 组忙碌**

- 前置条件：
  - 一致性组 cg-1 状态为 updating
- 测试步骤：
  1. 尝试删除一致性组
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125205 (ErrCGIsBusy)

#### 2. 一致性组快照测试

##### 2.1 创建一致性组快照

**测试场景：正常创建一致性组快照**

- 前置条件：
  - 一致性组 cg-1 已创建，状态为 available
  - 组内所有卷状态正常，不忙碌
- 测试步骤：
  1. 发送 POST 请求到 `/v1/consistency-groups/{cg-1-UUID}/snapshots`
  2. 请求体包含 name 和 description
- 预期结果：
  - HTTP 状态码：200
  - 返回快照信息，状态为 pending
  - 数据库中创建 ConsistencyGroupSnapshot 记录
  - 创建关联的 Task 记录
  - 组内所有卷状态变为 backuping
  - WDS 中成功创建一致性组快照
  - 最终快照状态变为 available
  - 组内所有卷状态恢复为之前状态

**测试场景：创建快照失败 - 卷忙碌**

- 前置条件：
  - 一致性组 cg-1 包含 volume-1 和 volume-2
  - volume-1 状态为 backuping
- 测试步骤：
  1. 尝试创建一致性组快照
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125208 (ErrCGVolumeIsBusy)
  - 错误信息：卷正在忙碌中，不能创建快照

**测试场景：创建快照失败 - 一致性组忙碌**

- 前置条件：
  - 一致性组 cg-1 状态为 updating
- 测试步骤：
  1. 尝试创建快照
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125205 (ErrCGIsBusy)

##### 2.2 获取一致性组快照列表

**测试场景：获取一致性组快照列表**

- 前置条件：
  - 一致性组 cg-1 已创建 3 个快照
- 测试步骤：
  1. 发送 GET 请求到 `/v1/consistency-groups/{cg-1-UUID}/snapshots?offset=0&limit=10`
- 预期结果：
  - HTTP 状态码：200
  - 返回快照列表，包含总数、偏移量、限制数量和快照数组
  - 每个快照包含基本信息

##### 2.3 获取一致性组快照详情

**测试场景：获取快照详情**

- 前置条件：
  - 快照 snap-1 已创建
- 测试步骤：
  1. 发送 GET 请求到 `/v1/consistency-groups/{cg-1-UUID}/snapshots/{snap-1-UUID}`
- 预期结果：
  - HTTP 状态码：200
  - 返回快照详细信息，包含所有字段和关联的一致性组信息

##### 2.4 恢复一致性组快照

**测试场景：正常恢复快照**

- 前置条件：
  - 快照 snap-1 已创建，状态为 available
  - 一致性组 cg-1 状态为 available
  - 组内所有卷状态正常，不忙碌
- 测试步骤：
  1. 发送 PUT 请求到 `/v1/consistency-groups/{cg-1-UUID}/restore`
  2. 请求体包含 snapshot_id 和 force=false
- 预期结果：
  - HTTP 状态码：200
  - 快照状态变为 restoring
  - 组内所有卷状态变为 restoring
  - 创建恢复任务
  - WDS 中成功触发恢复操作
  - 最终快照状态恢复为 available
  - 组内所有卷状态恢复为 available

**测试场景：强制恢复快照**

- 前置条件：
  - 快照 snap-1 已创建
  - 组内有卷处于 attached 状态
- 测试步骤：
  1. 发送 PUT 请求，force=true
- 预期结果：
  - HTTP 状态码：200
  - 成功触发强制恢复
  - 组内所有卷强制恢复

**测试场景：恢复快照失败 - 快照不可用**

- 前置条件：
  - 快照 snap-1 状态为 error
- 测试步骤：
  1. 尝试恢复快照
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125214 (ErrCGSnapshotIsBusy) 或相应错误
  - 错误信息：快照状态不可用

**测试场景：恢复快照失败 - 卷忙碌且未强制**

- 前置条件：
  - 组内有卷处于 backuping 状态
  - force=false
- 测试步骤：
  1. 尝试恢复快照
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125208 (ErrCGVolumeIsBusy)

##### 2.5 删除一致性组快照

**测试场景：删除快照**

- 前置条件：
  - 快照 snap-1 已创建，状态为 available
- 测试步骤：
  1. 发送 DELETE 请求到 `/v1/consistency-groups/{cg-1-UUID}/snapshots/{snap-1-UUID}`
- 预期结果：
  - HTTP 状态码：204
  - 快照从数据库中删除
  - WDS 中成功删除快照

**测试场景：删除快照失败 - 快照忙碌**

- 前置条件：
  - 快照 snap-1 状态为 restoring
- 测试步骤：
  1. 尝试删除快照
- 预期结果：
  - HTTP 状态码：400
  - 错误码：125214 (ErrCGSnapshotIsBusy)
  - 错误信息：快照正在使用中，不能删除

#### 3. 权限和多租户测试

##### 3.1 权限验证

**测试场景：无权限访问其他组织的一致性组**

- 前置条件：
  - 用户 A 属于组织 org-1
  - 一致性组 cg-1 属于组织 org-2
- 测试步骤：
  1. 用户 A 尝试访问 cg-1
- 预期结果：
  - HTTP 状态码：401 或 403
  - 错误码：100004 (ErrPermissionDenied)

**测试场景：读权限用户无法创建快照**

- 前置条件：
  - 用户 B 对组织 org-1 只有读权限
- 测试步骤：
  1. 用户 B 尝试创建一致性组或快照
- 预期结果：
  - HTTP 状态码：401 或 403
  - 错误码：100004 (ErrPermissionDenied)

#### 4. 并发和边界测试

##### 4.1 并发操作

**测试场景：并发创建快照**

- 前置条件：
  - 一致性组 cg-1 已创建
- 测试步骤：
  1. 同时发送 2 个创建快照请求
- 预期结果：
  - 只有一个请求成功
  - 另一个请求返回 400，提示一致性组忙碌

**测试场景：并发修改成员**

- 前置条件：
  - 一致性组 cg-1 已创建
- 测试步骤：
  1. 同时发送添加卷和删除卷请求
- 预期结果：
  - 操作按顺序执行，确保数据一致性

##### 4.2 边界测试

**测试场景：空一致性组**

- 测试步骤：
  1. 创建不包含任何卷的一致性组
- 预期结果：
  - 创建成功（或根据业务规则返回错误）

**测试场景：大量卷的一致性组**

- 测试步骤：
  1. 创建包含 100 个卷的一致性组
  2. 创建快照
- 预期结果：
  - 创建成功
  - 快照操作正常

#### 5. 异常恢复测试

##### 5.1 WDS 服务异常

**测试场景：WDS 服务不可用**

- 前置条件：
  - WDS 服务停止或网络不通
- 测试步骤：
  1. 尝试创建一致性组
- 预期结果：
  - 返回错误信息
  - 一致性组状态标记为 error
  - 用户收到明确的错误提示

##### 5.2 部分卷操作失败

**测试场景：快照部分卷失败**

- 前置条件：
  - 一致性组包含 3 个卷
  - 在快照过程中，某个卷的快照失败
- 测试步骤：
  1. 创建一致性组快照
- 预期结果：
  - 整个快照操作失败
  - 快照状态标记为 error
  - 所有卷状态恢复为之前状态
  - WDS 中清理失败的快照

#### 6. 集成测试

##### 6.1 完整工作流测试

**测试场景：完整的一致性组生命周期**

- 测试步骤：
  1. 创建存储池
  2. 创建 3 个卷
  3. 创建一致性组，添加 3 个卷
  4. 获取一致性组列表和详情
  5. 创建快照 1
  6. 创建快照 2
  7. 获取快照列表
  8. 恢复快照 1
  9. 删除快照 2
  10. 删除快照 1
  11. 添加新卷到一致性组
  12. 从一致性组删除卷
  13. 删除一致性组
  14. 删除卷
- 预期结果：
  - 所有操作成功
  - 状态转换正确
  - 数据一致性保持
  - WDS 中资源正确创建和清理

#### 7. 性能测试

##### 7.1 快照性能

**测试场景：大规模快照性能**

- 测试步骤：
  1. 创建包含 50 个卷的一致性组
  2. 创建快照
  3. 记录耗时
- 预期结果：
  - 快照创建在合理时间内完成
  - 系统稳定，无超时

##### 7.2 并发请求性能

**测试场景：并发 API 请求**

- 测试步骤：
  1. 并发发送 100 个获取一致性组列表请求
- 预期结果：
  - 所有请求成功
  - 响应时间在可接受范围内
  - 无数据库连接耗尽等问题