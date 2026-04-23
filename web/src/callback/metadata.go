/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "web/src/common"

	"github.com/jinzhu/gorm"
)

const source = "Cloudland" // 事件来源系统名称

// ResourceMetadata 定义资源元数据
type ResourceMetadata struct {
	ResourceType ResourceType
	IDArgIndex   int
	ActionType   string
	Extractor    ResourceExtractor
}

// ResourceExtractor 自定义资源信息提取函数
type ResourceExtractor func(ctx context.Context, args []string) (*ResourceChangeEvent, error)

// commandMetadataRegistry Command 到资源的映射注册表
var commandMetadataRegistry = map[string]*ResourceMetadata{
	// ==================== 虚拟机相关 ====================
	"launch_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
		ActionType:   ActionCreated,
	},
	"action_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
		ActionType:   ActionStateChanged,
	},
	"migrate_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   3,
		ActionType:   ActionMigrated,
	},

	// ==================== 存储卷相关 ====================
	"create_volume_local": { // create volume local 测试不到
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
		ActionType:   ActionCreated,
	},
	"create_volume_wds_vhost": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
		ActionType:   ActionCreated,
	},
	"attach_volume_local": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionAttached,
	},
	"attach_volume_wds_vhost": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionAttached,
	},
	"detach_volume": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionDetached,
	},
	"detach_volume_wds_vhost": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
		ActionType:   ActionDetached,
	},
	"resize_volume": {
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
	"attach_vm_nic": {
		ResourceType: ResourceTypeInterface,
		IDArgIndex:   1,
		ActionType:   ActionAttached,
	},
}

// 这些命令是 debug mode 下的频繁上报命令，不做事件推送处理
var notTrackedCommands = map[string]bool{
	"report_rc":     true,
	"hyper_status":  true,
	"system_router": true,
	"inst_status":   true,
}

// ExtractAndPushEvent 提取资源信息并推送事件 (核心函数)
func ExtractAndPushEvent(ctx context.Context, cmd string, args []string, execError error) {
	// 如果没有callback的配置，则不做任何处理
	if !IsEnabled() {
		return
	}
	if execError != nil {
		logger.Debugf("Command %s failed with error, skipping event push: %v", cmd, execError)
		return
	}

	metadata, exists := commandMetadataRegistry[cmd]
	if !exists {
		// 比如report_rc 等命令，量大不需要打印日志，不做事件推送
		if !notTrackedCommands[cmd] {
			logger.Debugf("Command %s not registered in metadata registry", cmd)
		}
		return
	}

	var rcEvent *ResourceChangeEvent
	var err error

	if metadata.Extractor != nil {
		// 自定义提取器, 扩展使用
		rcEvent, err = metadata.Extractor(ctx, args)
	} else {
		// 默认提取器
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
	if metadata.IDArgIndex >= len(args) {
		logger.Debugf("IDArgIndex %d out of range for args length %d", metadata.IDArgIndex, len(args))
		return nil, nil
	}

	resourceIDStr := args[metadata.IDArgIndex]
	resourceID, err := strconv.ParseInt(resourceIDStr, 10, 64)
	if err != nil {
		logger.Errorf("Failed to parse resource ID '%s': %v from command %s", resourceIDStr, err, args)
		return nil, err
	}

	db := DB()

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

// --------------------- helpers ---------------------

func compactSQL(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// --------------------- 各种资源类型的提取器 (Raw + join org uuid) ---------------------

func extractInstanceInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	traceID := fmt.Sprintf("inst-%d-%d", resourceID, time.Now().UnixNano())
	start := time.Now()

	row := &InstanceRow{}
	logger.Debugf("[%s] extractInstanceInfo: sql=%s args=[%d]", traceID, compactSQL(sqlSelectInstanceByID), resourceID)

	if err := db.Raw(sqlSelectInstanceByID, resourceID).Scan(row).Error; err != nil {
		logger.Errorf("[%s] extractInstanceInfo: query failed id=%d err=%v elapsed=%s",
			traceID, resourceID, err, time.Since(start))
		return nil, err
	}

	// Scan 查不到时通常是零值，这里是“查不到就返回错误”的行为
	if row.ID == 0 || row.UUID == "" {
		logger.Errorf("[%s] extractInstanceInfo: not found id=%d elapsed=%s", traceID, resourceID, time.Since(start))
		return nil, gorm.ErrRecordNotFound
	}

	if row.TenantUUID == "" {
		// 兜底日志，防范特殊defect
		logger.Warningf("[%s] extractInstanceInfo: tenant uuid empty (owner_id=%d) inst_uuid=%s elapsed=%s",
			traceID, row.Owner, row.UUID, time.Since(start))
	} else {
		logger.Debugf("[%s] extractInstanceInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s status=%s hyper=%d zone_id=%d cpu=%d mem=%d disk=%d hostname=%s",
			traceID, time.Since(start),
			row.ID, row.UUID, row.Owner, row.TenantUUID,
			row.Status, row.Hyper, row.ZoneID, row.Cpu, row.Memory, row.Disk, row.Hostname)
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInstance,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"hostname": row.Hostname,
			"status":   row.Status,
			"hyper_id": row.Hyper,
			"zone_id":  row.ZoneID,
			"cpu":      row.Cpu,
			"memory":   row.Memory,
			"disk":     row.Disk,
		},
	}, nil
}

func extractVolumeInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	traceID := fmt.Sprintf("vol-%d-%d", resourceID, time.Now().UnixNano())
	start := time.Now()

	row := &VolumeRow{}
	logger.Debugf("[%s] extractVolumeInfo: sql=%s args=[%d]", traceID, compactSQL(sqlSelectVolumeByID), resourceID)

	if err := db.Raw(sqlSelectVolumeByID, resourceID).Scan(row).Error; err != nil {
		logger.Errorf("[%s] extractVolumeInfo: query failed id=%d err=%v elapsed=%s",
			traceID, resourceID, err, time.Since(start))
		return nil, err
	}

	if row.ID == 0 || row.UUID == "" {
		logger.Errorf("[%s] extractVolumeInfo: not found id=%d elapsed=%s", traceID, resourceID, time.Since(start))
		return nil, gorm.ErrRecordNotFound
	}

	if row.TenantUUID == "" {
		// 兜底日志，防范特殊defect
		logger.Warningf("[%s] extractVolumeInfo: tenant uuid empty (owner_id=%d) volume_uuid=%s elapsed=%s",
			traceID, row.Owner, row.UUID, time.Since(start))
	} else {
		logger.Debugf("[%s] extractVolumeInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s instance_id=%d status=%s size=%d",
			traceID, time.Since(start),
			row.ID, row.UUID, row.Owner, row.TenantUUID,
			row.InstanceID, row.Status, row.Size)
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeVolume,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"name":        row.Name,
			"status":      row.Status,
			"size":        row.Size,
			"instance_id": row.InstanceID,
			"target":      row.Target,
			"format":      row.Format,
			"path":        row.Path,
		},
	}, nil
}

func extractImageInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	traceID := fmt.Sprintf("img-%d-%d", resourceID, time.Now().UnixNano())
	start := time.Now()

	row := &ImageRow{}
	logger.Debugf("[%s] extractImageInfo: sql=%s args=[%d]", traceID, compactSQL(sqlSelectImageByID), resourceID)

	if err := db.Raw(sqlSelectImageByID, resourceID).Scan(row).Error; err != nil {
		logger.Errorf("[%s] extractImageInfo: query failed id=%d err=%v elapsed=%s",
			traceID, resourceID, err, time.Since(start))
		return nil, err
	}

	if row.ID == 0 || row.UUID == "" {
		logger.Errorf("[%s] extractImageInfo: not found id=%d elapsed=%s", traceID, resourceID, time.Since(start))
		return nil, gorm.ErrRecordNotFound
	}

	if row.TenantUUID == "" {
		// 兜底日志，防范特殊defect
		logger.Warningf("[%s] extractImageInfo: tenant uuid empty (owner_id=%d) img_uuid=%s elapsed=%s",
			traceID, row.Owner, row.UUID, time.Since(start))
	} else {
		logger.Infof("[%s] extractImageInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s status=%s size=%d name=%s",
			traceID, time.Since(start),
			row.ID, row.UUID, row.Owner, row.TenantUUID,
			row.Status, row.Size, row.Name)
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeImage,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"name":         row.Name,
			"status":       row.Status,
			"format":       row.Format,
			"os_code":      row.OSCode,
			"size":         row.Size,
			"architecture": row.Architecture,
		},
	}, nil
}

func extractInterfaceInfo(db *gorm.DB, resourceID int64, args []string) (*ResourceChangeEvent, error) {
	if len(args) < 3 {
		err := fmt.Errorf("invalid args length: expected >=3, got %d, args: %v", len(args), args)
		logger.Error("Invalid args", "error", err)
		return nil, err
	}

	macAddr := strings.TrimSpace(args[2])
	if macAddr == "" {
		err := fmt.Errorf("empty mac address")
		logger.Error("Invalid mac address", "error", err)
		return nil, err
	}

	traceID := fmt.Sprintf("nic-%d-%s-%d", resourceID, strings.ReplaceAll(macAddr, ":", ""), time.Now().UnixNano())
	start := time.Now()

	// 仍然先校验 instance 是否存在（instance not found 时返回明确错误）
	{
		type instExists struct {
			ID int64
		}
		ex := &instExists{}
		q := `SELECT id FROM instances WHERE id = ? LIMIT 1`
		logger.Debugf("[%s] extractInterfaceInfo: check instance sql=%s args=[%d]", traceID, compactSQL(q), resourceID)
		if err := db.Raw(q, resourceID).Scan(ex).Error; err != nil {
			logger.Errorf("[%s] extractInterfaceInfo: check instance failed id=%d err=%v elapsed=%s",
				traceID, resourceID, err, time.Since(start))
			return nil, err
		}
		if ex.ID == 0 {
			logger.Error("Instance not found", "resourceID", resourceID)
			return nil, fmt.Errorf("instance %d not found", resourceID)
		}
	}

	row := &InterfaceRow{}
	logger.Debugf("[%s] extractInterfaceInfo: sql=%s args=[%d,%s]", traceID, compactSQL(sqlSelectInterfaceByID), resourceID, macAddr)

	if err := db.Raw(sqlSelectInterfaceByID, resourceID, macAddr).Scan(row).Error; err != nil {
		logger.Errorf("[%s] extractInterfaceInfo: query failed instance_id=%d mac=%s err=%v elapsed=%s",
			traceID, resourceID, macAddr, err, time.Since(start))
		return nil, err
	}

	if row.ID == 0 || row.UUID == "" {
		logger.Error("Interface not found", "resourceID", resourceID, "macAddr", macAddr)
		return nil, fmt.Errorf("interface with mac %s not found", macAddr)
	}

	// Interface 没有明确 status 字段, 逻辑判断如下：
	status := "active"
	if row.Hyper == -1 {
		status = "unattached"
	}
	if row.TenantUUID == "" {
		// 兜底日志，防范特殊defect
		logger.Warningf("[%s] extractInterfaceInfo: tenant uuid empty (owner_id=%d) nic_uuid=%s elapsed=%s",
			traceID, row.Owner, row.UUID, time.Since(start))
	} else {
		logger.Debugf("[%s] extractInterfaceInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s instance_id=%d mac=%s hyper=%d type=%s",
			traceID, time.Since(start),
			row.ID, row.UUID, row.Owner, row.TenantUUID,
			row.Instance, row.MacAddr, row.Hyper, row.Type)
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInterface,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"name":        row.Name,
			"status":      status,
			"mac_addr":    row.MacAddr,
			"instance_id": row.Instance,
			"hyper_id":    row.Hyper,
			"type":        row.Type,
		},
	}, nil
}
