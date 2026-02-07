/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
)

const source = "Cloudland" // 事件来源系统名称

// ResourceMetadata 定义资源元数据
type ResourceMetadata struct {
	// ResourceType 资源类型
	ResourceType ResourceType
	// IDArgIndex 资源 ID 在 args 中的位置 (args[0] 是命令本身)
	IDArgIndex int
	// 资源操作类型
	ActionType string
	// Extractor 自定义提取器 (可选，优先级高于 IDArgIndex)
	Extractor ResourceExtractor
}

// ResourceExtractor 自定义资源信息提取函数
type ResourceExtractor func(ctx context.Context, args []string) (*ResourceChangeEvent, error)

// commandMetadataRegistry Command 到资源的映射注册表
var commandMetadataRegistry = map[string]*ResourceMetadata{
	// ==================== 虚拟机相关 ====================
	"launch_vm": { // ✅️
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1, // args[1] 是 instance ID
		ActionType:   ActionCreated,
	},
	/*
		//虚机状态查询，不做事件推送
		"inst_status": {
			ResourceType: ResourceTypeInstance,
			Extractor:    extractInstanceStatusBatch, // 批量处理，需要自定义
		},
	*/
	"action_vm": { // ✅️
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
		ActionType:   ActionStateChanged,
	},
	/*
		// 资源已经被删除，不需要推送事件
		"clear_vm": {
			ResourceType: ResourceTypeInstance,
			IDArgIndex:   1,
		},
	*/
	"migrate_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   3,
		ActionType:   ActionMigrated,
	},

	// ==================== 存储卷相关 ====================
	"create_volume_local": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1, // args[1] 是 volume ID
		ActionType:   ActionCreated,
	},
	"create_volume_wds_vhost": { // ✅️
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
		ActionType:   ActionCreated,
	},
	"attach_volume_local": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2, // args[2] 是 volume ID
		ActionType:   ActionAttached,
	},
	"attach_volume_wds_vhost": { // ✅️
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionAttached,
	},
	"detach_volume": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionDetached,
	},
	"detach_volume_wds_vhost": { // ✅️
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionDetached,
	},
	/*
		// 资源已经被删除，不需要推送事件
		"delete_volume": {
			ResourceType: ResourceTypeVolume,
			IDArgIndex:   1,
		},
	*/
	"resize_volume": { // ✅️
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
		ActionType:   ActionResized,
	},

	// ==================== 镜像相关 ====================
	"create_image": {
		ResourceType: ResourceTypeImage,
		IDArgIndex:   1,
		ActionType:   ActionCreated,
	},
	"capture_image": {
		ResourceType: ResourceTypeImage,
		IDArgIndex:   1,
		ActionType:   ActionCaptured,
	},

	// ==================== 网络接口相关 ====================
	"attach_vm_nic": { // ✅️
		ResourceType: ResourceTypeInterface,
		IDArgIndex:   1, // instance id, 通过instance id获取interface id 和mac address 然后拿到interface
		ActionType:   ActionAttached,
	},
	/*
		"detach_vm_nic": { // 资源已经被删除了，不需要推送事件
			ResourceType: ResourceTypeInterface,
			IDArgIndex:   1, // instance id, 通过instance id获取interface id 和mac address 然后拿到interface
		},
	*/
}

// 这些命令即是是debug mode 下的频繁上报命令，不做事件推送处理
var notTrackedCommands = map[string]bool{
	"report_rc":     true,
	"hyper_status":  true,
	"system_router": true,
	"inst_status":   true,
}

// ExtractAndPushEvent 提取资源信息并推送事件 (核心函数)
// 此函数在 RPC 命令执行完成后被调用
func ExtractAndPushEvent(ctx context.Context, cmd string, args []string, execError error) {
	// 功能未启用则直接返回
	if !IsEnabled() {
		return
	}
	// 如果命令执行失败，不推送事件
	// 可以根据需求修改这个逻辑，比如推送错误事件
	if execError != nil {
		logger.Debugf("Command %s failed with error, skipping event push: %v", cmd, execError)
		return
	}

	// 查找元数据
	metadata, exists := commandMetadataRegistry[cmd]
	if !exists {
		if !notTrackedCommands[cmd] { // 该命令没有注册元数据，不处理
			logger.Debugf("Command %s not registered in metadata registry", cmd)
		}
		return
	}

	var rcEvent *ResourceChangeEvent
	var err error

	// 使用自定义提取器或默认提取器
	if metadata.Extractor != nil {
		rcEvent, err = metadata.Extractor(ctx, args)
	} else {
		rcEvent, err = defaultExtractor(ctx, metadata, args)
	}

	if err != nil {
		logger.Errorf("Failed to extract resource info for command %s: %v", cmd, err)
		return
	}

	if rcEvent != nil {
		resource := &Resource{
			Type:   rcEvent.ResourceType.String(),
			ID:     rcEvent.ResourceUUID,
			Region: GetRegion(),
		}
		// 推送事件到队列
		event := &Event{
			EventType:  rcEvent.ResourceType.String() + "_" + metadata.ActionType,
			Source:     source,
			OccurredAt: time.Now(),
			TenantID:   rcEvent.TenantID,
			Resource:   *resource,
			Data:       rcEvent.Data,
			Metadata:   rcEvent.Metadata,
		}
		success := PushEvent(event)
		if !success {
			logger.Warningf("Failed to push event for command %s: queue full", cmd)
		}
	} else {
		logger.Debugf("ExtractAndPushEvent: no event extracted for command %s (rcEvent is nil)", cmd)
	}
}

// defaultExtractor 默认的资源信息提取器
func defaultExtractor(ctx context.Context, metadata *ResourceMetadata, args []string) (*ResourceChangeEvent, error) {
	// 检查参数索引是否有效
	if metadata.IDArgIndex >= len(args) {
		logger.Debugf("IDArgIndex %d out of range for args length %d", metadata.IDArgIndex, len(args))
		return nil, nil
	}

	// 提取资源 ID
	resourceIDStr := args[metadata.IDArgIndex]
	resourceID, err := strconv.ParseInt(resourceIDStr, 10, 64)
	if err != nil {
		logger.Errorf("Failed to parse resource ID '%s': %v from command %s", resourceIDStr, err, args)
		return nil, err
	}

	db := DB()

	// 根据资源类型查询数据库
	switch metadata.ResourceType {
	case ResourceTypeInstance:
		return extractInstanceInfo(db, resourceID)

	case ResourceTypeVolume:
		return extractVolumeInfo(db, resourceID)

	case ResourceTypeImage:
		return extractImageInfo(db, resourceID)

	case ResourceTypeInterface:
		return extractInterfaceInfo(db, resourceID, args)

	default:
		logger.Warningf("Unknown resource type: %s", metadata.ResourceType)
		return nil, nil
	}
}

// extractInstanceInfo 提取虚拟机实例信息
func extractInstanceInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	logger.Debugf("extractInstanceInfo: starting query for instance ID=%d", resourceID)
	instance := &model.Instance{}
	if err := db.Where("id = ?", resourceID).First(instance).Error; err != nil {
		logger.Errorf("Failed to query instance %d: %v", resourceID, err)
		return nil, err
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInstance,
		ResourceUUID: instance.UUID,
		TenantID:     instance.OwnerInfo.UUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"hostname": instance.Hostname,
			"status":   instance.Status.String(),
			"hyper_id": instance.Hyper,
			"zone_id":  instance.ZoneID,
			"cpu":      instance.Cpu,
			"memory":   instance.Memory,
			"disk":     instance.Disk,
		},
	}, nil
}

/*
// extractVolumeInfo 提取存储卷信息

	func extractVolumeInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
		volume := &model.Volume{}

		logger.Debugf("extractVolumeInfo: starting query for volume ID=%d", resourceID)

		// 使用原生 SQL 查询，避免 GORM 模型关联导致的死锁
		// 直接查询需要的字段，不触发任何关联加载
		query := `
			SELECT id, uuid, owner, name, status, size,
			       instance_id, target, format, path
			FROM volumes
			WHERE id = ?
		`

		if err := db.Raw(query, resourceID).Scan(volume).Error; err != nil {
			logger.Errorf("Failed to query volume %d: %v", resourceID, err)
			return nil, err
		}

		logger.Infof("Succeed to extractVolumeInfo, volume UUID=%s, tenant UUID=%s",
			volume.UUID, volume.OwnerInfo.UUID)

		return &ResourceChangeEvent{
			ResourceType: ResourceTypeVolume,
			ResourceUUID: volume.UUID,
			TenantID:     volume.OwnerInfo.UUID,
			Timestamp:    time.Now(),
			Data: map[string]interface{}{
				"name":        volume.Name,
				"status":      volume.Status.String(),
				"size":        volume.Size,
				"instance_id": volume.InstanceID,
				"target":      volume.Target,
				"format":      volume.Format,
				"path":        volume.Path,
			},
		}, nil
	}
*/
func extractVolumeInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	volume := &model.Volume{}

	// ====== 可调参数（不改变原逻辑，只加观测）======
	// 你原来 probe 里 stmt_timeout=3s/query_timeout=3s，这里对齐用 3s
	// 这里不使用 context，而是用 timer 触发诊断日志（gorm v1 不支持 WithContext）
	stmtTimeout := 3 * time.Second
	diagAfter := 1200 * time.Millisecond // 卡住多久开始打印一次 dbstats（可改小点）
	// ===========================================

	traceID := fmt.Sprintf("vol-%d-%d", resourceID, time.Now().UnixNano())
	start := time.Now()

	logger.Debugf("[%s] extractVolumeInfo: start volumeID=%d", traceID, resourceID)

	// 使用原生 SQL 查询，避免 GORM 模型关联导致的死锁
	query := `
		SELECT id, uuid, owner, name, status, size,
		       instance_id, target, format, path
		FROM volumes
		WHERE id = ?
	`
	logger.Debugf("[%s] extractVolumeInfo: sql=%s args=[%d] stmt_timeout=%s", traceID, compactSQL(query), resourceID, stmtTimeout)

	// —— 1) 设置 statement_timeout（只影响当前事务/当前连接），并打印设置结果
	// gorm v1 不保证一定在事务里，但 SET LOCAL 在事务外会报错；因此这里用 SET statement_timeout 更稳
	// 你原日志看到是 SET LOCAL statement_timeout = 3000，说明你那边可能包了事务；
	// 为了不改变行为，我们先尝试 SET LOCAL，失败再 fallback 到 SET。
	setStmtTimeoutWithFallback(db, stmtTimeout)

	// —— 2) 观察是否“卡住”并输出诊断
	done := make(chan struct{})
	go func() {
		defer func() { recover() }()

		// 第一次到点打印 dbstats
		timer := time.NewTimer(diagAfter)
		select {
		case <-timer.C:
			logger.Errorf("[%s] extractVolumeInfo: still running after %s (possible hung)", traceID, diagAfter)
			printDBStatsV1(db, traceID)

			// 到这里再等到 stmtTimeout（总时长），超时则拉取 pg 诊断信息
			timeoutTimer := time.NewTimer(stmtTimeout - diagAfter)
			select {
			case <-timeoutTimer.C:
				logger.Errorf("[%s] extractVolumeInfo: exceeded stmt_timeout=%s, dumping pg diagnostics", traceID, stmtTimeout)
				printDBStatsV1(db, traceID)
				dumpPostgresDiagnostics(traceID)
			case <-done:
				timeoutTimer.Stop()
				return
			}

		case <-done:
			timer.Stop()
			return
		}
	}()

	// —— 3) 执行原来的查询（逻辑不变）
	err := db.Raw(query, resourceID).Scan(volume).Error
	close(done)

	elapsed := time.Since(start)

	if err != nil {
		logger.Errorf("[%s] extractVolumeInfo: query FAILED elapsed=%s volumeID=%d err=%v", traceID, elapsed, resourceID, err)
		printDBStatsV1(db, traceID)
		// 失败时也做一次旁路诊断（不影响原逻辑，只增加信息）
		dumpPostgresDiagnostics(traceID)
		return nil, err
	}

	// 原逻辑：成功后记录 volume.UUID、volume.OwnerInfo.UUID
	// 这里额外打印关键字段，便于确认 scan 到底拿到什么
	logger.Infof("[%s] extractVolumeInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d instance_id=%d status=%s size=%d",
		traceID, elapsed, volume.ID, volume.UUID, volume.Owner, volume.InstanceID, volume.Status.String(), volume.Size)

	// 原逻辑保持：tenant UUID 从 OwnerInfo 取
	// 如果 OwnerInfo 没加载到，可能导致后面读取 UUID 时卡/空；这里加一行观测日志
	logger.Infof("[%s] extractVolumeInfo: tenant_uuid(from OwnerInfo)=%s", traceID, volume.OwnerInfo.UUID)

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeVolume,
		ResourceUUID: volume.UUID,
		TenantID:     volume.OwnerInfo.UUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"name":        volume.Name,
			"status":      volume.Status.String(),
			"size":        volume.Size,
			"instance_id": volume.InstanceID,
			"target":      volume.Target,
			"format":      volume.Format,
			"path":        volume.Path,
		},
	}, nil
}

// extractImageInfo 提取镜像信息
func extractImageInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	image := &model.Image{}
	if err := db.Where("id = ?", resourceID).Take(image).Error; err != nil {
		logger.Errorf("Failed to query image %d: %v", resourceID, err)
		return nil, err
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeImage,
		ResourceUUID: image.UUID,
		TenantID:     image.OwnerInfo.UUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"name":         image.Name,
			"status":       image.Status,
			"format":       image.Format,
			"os_code":      image.OSCode,
			"size":         image.Size,
			"architecture": image.Architecture,
		},
	}, nil
}

// extractInterfaceInfo 提取网络接口信息
func extractInterfaceInfo(db *gorm.DB, resourceID int64, args []string) (*ResourceChangeEvent, error) {
	// 参数检查, 不依赖上层保证
	if len(args) < 3 {
		err := fmt.Errorf("invalid args length: expected >=3, got %d, args: %v", len(args), args)
		logger.Error("Invalid args", "error", err)
		return nil, err
	}
	// 检查MAC地址格式
	macAddr := strings.TrimSpace(args[2])
	if macAddr == "" {
		err := fmt.Errorf("empty mac address")
		logger.Error("Invalid mac address", "error", err)
		return nil, err
	}
	// 查询实例
	instance := &model.Instance{}
	err := db.Where("id = ?", resourceID).Take(instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Instance not found", "resourceID", resourceID)
			return nil, fmt.Errorf("instance %d not found", resourceID)
		}
		logger.Error("Failed to query instance", "resourceID", resourceID, "error", err)
		return nil, err
	}
	// 查询接口信息
	iface := &model.Interface{}
	err = db.Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("Address").Preload("Address.Subnet").Where("instance = ? and mac_addr = ?", resourceID, macAddr).Take(iface).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Interface not found",
				"resourceID", resourceID,
				"macAddr", macAddr)
			return nil, fmt.Errorf("interface with mac %s not found", macAddr)
		}
		logger.Error("Failed to query interface",
			"resourceID", resourceID,
			"macAddr", macAddr,
			"error", err)
		return nil, err
	}
	// Interface 没有明确的 status 字段，使用 "active" 作为默认状态
	status := "active"
	if iface.Hyper == -1 {
		status = "unattached"
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInterface,
		ResourceUUID: iface.UUID,
		TenantID:     iface.OwnerInfo.UUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"name":        iface.Name,
			"status":      status,
			"mac_addr":    iface.MacAddr,
			"instance_id": iface.Instance,
			"hyper_id":    iface.Hyper,
			"type":        iface.Type,
		},
	}, nil
}
