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

const source = "Cloudland" // Event source system name

// ResourceMetadata defines resource metadata.
type ResourceMetadata struct {
	ResourceType ResourceType
	IDArgIndex   int
	ActionType   string
	Extractor    ResourceExtractor
}

// ResourceExtractor is a custom extractor for resource information.
type ResourceExtractor func(ctx context.Context, args []string) (*ResourceChangeEvent, error)

// commandMetadataRegistry maps command to resource metadata.
var commandMetadataRegistry = map[string]*ResourceMetadata{
	// ==================== Instance ====================
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
	"clear_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   1,
		ActionType:   ActionDeleted,
	},
	"migrate_vm": {
		ResourceType: ResourceTypeInstance,
		IDArgIndex:   3,
		ActionType:   ActionMigrated,
	},

	// ==================== Volume ====================
	"create_volume_local": { // create volume local is not covered by current tests
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

	// ==================== Image ====================
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

	// ==================== Interface ====================
	"attach_vm_nic": {
		ResourceType: ResourceTypeInterface,
		IDArgIndex:   1,
		ActionType:   ActionAttached,
	},
}

// These are high-frequency debug-mode report commands and are not pushed as events.
var notTrackedCommands = map[string]bool{
	"report_rc":     true,
	"hyper_status":  true,
	"system_router": true,
	"inst_status":   true,
}

// ExtractAndPushEvent extracts resource info and pushes event (core function).
func ExtractAndPushEvent(ctx context.Context, cmd string, args []string, execError error) {
	// Skip processing when callback is not enabled.
	if !IsEnabled() {
		return
	}
	if execError != nil {
		logger.Debugf("Command %s failed with error, skipping event push: %v", cmd, execError)
		return
	}

	metadata, exists := commandMetadataRegistry[cmd]
	if !exists {
		// Commands like report_rc are high volume and should not be logged or pushed.
		if !notTrackedCommands[cmd] {
			logger.Debugf("Command %s not registered in metadata registry", cmd)
		}
		return
	}

	var rcEvent *ResourceChangeEvent
	var err error

	if metadata.Extractor != nil {
		// Custom extractor for extension use.
		rcEvent, err = metadata.Extractor(ctx, args)
	} else {
		// Default extractor.
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

// defaultExtractor is the default resource info extractor.
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
		return extractInstanceInfo(db, resourceID, metadata.ActionType == ActionDeleted)
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

// --------------------- Extractors for different resource types (Raw + join org uuid) ---------------------

func extractInstanceInfo(db *gorm.DB, resourceID int64, unscoped bool) (*ResourceChangeEvent, error) {
	traceID := fmt.Sprintf("inst-%d-%d", resourceID, time.Now().UnixNano())
	start := time.Now()

	row := &InstanceRow{}
	logger.Debugf("[%s] extractInstanceInfo: unscoped=%v sql=%s args=[%d]", traceID, unscoped, compactSQL(sqlSelectInstanceByID), resourceID)

	query := db
	if unscoped {
		query = db.Unscoped()
	}
	if err := query.Raw(sqlSelectInstanceByID, resourceID).Scan(row).Error; err != nil {
		logger.Errorf("[%s] extractInstanceInfo: query failed id=%d err=%v elapsed=%s",
			traceID, resourceID, err, time.Since(start))
		return nil, err
	}

	// When Scan finds nothing, fields are usually zero values; treat as not found error.
	if row.ID == 0 || row.UUID == "" {
		logger.Errorf("[%s] extractInstanceInfo: not found id=%d elapsed=%s", traceID, resourceID, time.Since(start))
		return nil, gorm.ErrRecordNotFound
	}

	if row.TenantUUID == "" {
		// Fallback log for unusual defects.
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
		// Fallback log for unusual defects.
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
		// Fallback log for unusual defects.
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

	// Still validate instance existence first (return explicit error if not found).
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

	// Interface has no explicit status field; derive status using this logic:
	status := "active"
	if row.Hyper == -1 {
		status = "unattached"
	}
	if row.TenantUUID == "" {
		// Fallback log for unusual defects.
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
