/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"context"
	"strconv"
	"time"

	. "web/src/common"
	"web/src/model"

	"github.com/jinzhu/gorm"
)

// ResourceMetadata 定义资源元数据
type ResourceMetadata struct {
	// ResourceType 资源类型
	ResourceType ResourceType
	// IDArgIndex 资源 ID 在 args 中的位置 (args[0] 是命令本身)
	IDArgIndex int
	// Extractor 自定义提取器 (可选，优先级高于 IDArgIndex)
	Extractor ResourceExtractor
}

// ResourceExtractor 自定义资源信息提取函数
type ResourceExtractor func(ctx context.Context, args []string) (*ResourceChangeEvent, error)

// commandMetadataRegistry Command 到资源的映射注册表
var commandMetadataRegistry = map[string]*ResourceMetadata{
	// ==================== 虚拟机相关 ====================
	"launch_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1, // args[1] 是 instance ID
	},
	"inst_status": {
		ResourceType: ResourceTypeInstance,
		Extractor:    extractInstanceStatusBatch, // 批量处理，需要自定义
	},
	"action_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
	},
	"clear_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
	},
	"migrate_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
	},

	// ==================== 存储卷相关 ====================
	"create_volume_local": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1, // args[1] 是 volume ID
	},
	"create_volume_wds_vhost": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
	},
	"attach_volume_local": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2, // args[2] 是 volume ID
	},
	"attach_volume_wds_vhost": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
	},
	"detach_volume": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
	},
	"detach_volume_wds_vhost": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   2,
	},
	"delete_volume": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
	},
	"resize_volume": {
		ResourceType: ResourceTypeVolume,
		IDArgIndex:   1,
	},

	// ==================== 镜像相关 ====================
	"create_image": {
		ResourceType: ResourceTypeImage,
		IDArgIndex:   1,
	},
	"capture_image": {
		ResourceType: ResourceTypeImage,
		IDArgIndex:   1,
	},

	// ==================== 网络接口相关 ====================
	"attach_vm_nic": {
		ResourceType: ResourceTypeInterface,
		IDArgIndex:   2, // 需要确认参数位置
	},
	"detach_vm_nic": {
		ResourceType: ResourceTypeInterface,
		IDArgIndex:   2, // 需要确认参数位置
	},
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
		// 该命令没有注册元数据，不处理
		logger.Debugf("Command %s not registered in metadata registry", cmd)
		return
	}

	var event *ResourceChangeEvent
	var err error

	// 使用自定义提取器或默认提取器
	if metadata.Extractor != nil {
		event, err = metadata.Extractor(ctx, args)
	} else {
		event, err = defaultExtractor(ctx, metadata, args)
	}

	if err != nil {
		logger.Errorf("Failed to extract resource info for command %s: %v", cmd, err)
		return
	}

	if event != nil {
		// 推送事件到队列
		success := PushEvent(event)
		if !success {
			logger.Warningf("Failed to push event for command %s: queue full", cmd)
		}
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
		logger.Errorf("Failed to parse resource ID '%s': %v", resourceIDStr, err)
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
		return extractInterfaceInfo(db, resourceID)

	default:
		logger.Warningf("Unknown resource type: %s", metadata.ResourceType)
		return nil, nil
	}
}

// extractInstanceInfo 提取虚拟机实例信息
func extractInstanceInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	instance := &model.Instance{}
	if err := db.Where("id = ?", resourceID).First(instance).Error; err != nil {
		logger.Errorf("Failed to query instance %d: %v", resourceID, err)
		return nil, err
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInstance,
		ResourceUUID: instance.UUID,
		ResourceID:   instance.ID,
		Status:       instance.Status.String(),
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"hostname": instance.Hostname,
			"hyper_id": instance.Hyper,
			"zone_id":  instance.ZoneID,
			"cpu":      instance.Cpu,
			"memory":   instance.Memory,
			"disk":     instance.Disk,
		},
	}, nil
}

// extractVolumeInfo 提取存储卷信息
func extractVolumeInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	volume := &model.Volume{}
	if err := db.Where("id = ?", resourceID).First(volume).Error; err != nil {
		logger.Errorf("Failed to query volume %d: %v", resourceID, err)
		return nil, err
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeVolume,
		ResourceUUID: volume.UUID,
		ResourceID:   volume.ID,
		Status:       volume.Status.String(),
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"name":        volume.Name,
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
	if err := db.Where("id = ?", resourceID).First(image).Error; err != nil {
		logger.Errorf("Failed to query image %d: %v", resourceID, err)
		return nil, err
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeImage,
		ResourceUUID: image.UUID,
		ResourceID:   image.ID,
		Status:       image.Status,
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"name":         image.Name,
			"format":       image.Format,
			"os_code":      image.OSCode,
			"size":         image.Size,
			"architecture": image.Architecture,
		},
	}, nil
}

// extractInterfaceInfo 提取网络接口信息
func extractInterfaceInfo(db *gorm.DB, resourceID int64) (*ResourceChangeEvent, error) {
	iface := &model.Interface{}
	if err := db.Where("id = ?", resourceID).First(iface).Error; err != nil {
		logger.Errorf("Failed to query interface %d: %v", resourceID, err)
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
		ResourceID:   iface.ID,
		Status:       status,
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"name":        iface.Name,
			"mac_addr":    iface.MacAddr,
			"instance_id": iface.Instance,
			"hyper_id":    iface.Hyper,
			"type":        iface.Type,
		},
	}, nil
}

// extractInstanceStatusBatch 处理 inst_status 的批量状态更新
// inst_status 格式: launch_vm.sh '3' '5 running 7 running 9 shut_off'
func extractInstanceStatusBatch(ctx context.Context, args []string) (*ResourceChangeEvent, error) {
	// inst_status 是批量更新多个实例的状态
	// 这里可以选择不处理，或者拆分成多个事件
	// 为了简化，这里返回 nil，不推送批量状态更新事件
	// 如果需要处理，可以解析 args[2] 并为每个实例生成事件

	logger.Debug("inst_status is batch operation, skipping event push")
	return nil, nil
}
