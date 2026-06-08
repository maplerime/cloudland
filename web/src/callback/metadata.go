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
	"web/src/model"

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
		return extractInstanceInfo(db, resourceID, metadata.ActionType == ActionDeleted, args, metadata.ActionType)
	case ResourceTypeVolume:
		return extractVolumeInfo(db, resourceID, args, metadata.IDArgIndex)
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

func enrichMigrationMetadata(data map[string]interface{}, migration *model.Migration) {
	if data == nil || migration == nil {
		return
	}
	data["force"] = migration.Force
	if migration.Type != "" {
		data["migration_type"] = migration.Type
	}
}

// --------------------- Extractors for different resource types (Raw + join org uuid) ---------------------

func extractInstanceInfo(db *gorm.DB, resourceID int64, unscoped bool, args []string, actionType string) (*ResourceChangeEvent, error) {
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

	// Skip intermediate migrating state — only the final migrated event matters.
	if row.Status == "migrating" {
		logger.Debugf("[%s] extractInstanceInfo: skipping migrating state id=%d uuid=%s", traceID, resourceID, row.UUID)
		return nil, nil
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

	data := map[string]interface{}{
		"id":       row.UUID,
		"name":     row.Hostname,
		"status":   row.Status,
		"hyper_id": row.Hyper,
		"zone_id":  row.ZoneID,
		"cpu":      row.Cpu,
		"memory":   row.Memory,
		"disk":     row.Disk,
	}

	// Migration event: enrich with source / target hyper hostnames and migration flags.
	if actionType == ActionMigrated && len(args) > 1 {
		if migrationID, err := strconv.ParseInt(args[1], 10, 64); err == nil {
			type migRow struct {
				SourceHyper int32
				TargetHyper int32
			}
			mr := &migRow{}
			qMig := `SELECT source_hyper, target_hyper FROM migrations WHERE id = ? LIMIT 1`
			if err := db.Raw(qMig, migrationID).Scan(mr).Error; err != nil {
				logger.Warningf("[%s] extractInstanceInfo: migration query failed id=%d: %v", traceID, migrationID, err)
			} else {
				if mr.TargetHyper <= 0 && len(args) > 4 {
					if fallbackHyperID, parseErr := strconv.ParseInt(args[4], 10, 64); parseErr == nil {
						mr.TargetHyper = int32(fallbackHyperID)
					}
				}

				type hyperName struct {
					Hostname string
				}
				// source hyper hostname
				if mr.SourceHyper > 0 {
					sn := &hyperName{}
					if err := db.Raw(`SELECT hostname FROM hypers WHERE hostid = ? LIMIT 1`, mr.SourceHyper).Scan(sn).Error; err != nil {
						logger.Warningf("[%s] extractInstanceInfo: source hyper query failed hostid=%d: %v", traceID, mr.SourceHyper, err)
					} else if sn.Hostname != "" {
						data["source_node"] = sn.Hostname
					}
				}
				// target hyper hostname
				if mr.TargetHyper > 0 {
					tn := &hyperName{}
					if err := db.Raw(`SELECT hostname FROM hypers WHERE hostid = ? LIMIT 1`, mr.TargetHyper).Scan(tn).Error; err != nil {
						logger.Warningf("[%s] extractInstanceInfo: target hyper query failed hostid=%d: %v", traceID, mr.TargetHyper, err)
					} else if tn.Hostname != "" {
						data["target_node"] = tn.Hostname
					}
				}
				logger.Debugf("[%s] extractInstanceInfo: migration nodes source=%v target=%v", traceID, data["source_node"], data["target_node"])
			}

			// Enrich migration metadata (force / type) — non-blocking, best-effort.
			type migMeta struct {
				Force bool
				Type  string
			}
			mm := &migMeta{}
			if err := db.Raw(`SELECT force, type FROM migrations WHERE id = ? LIMIT 1`, migrationID).Scan(mm).Error; err != nil {
				logger.Warningf("[%s] extractInstanceInfo: migration metadata query failed id=%d: %v", traceID, migrationID, err)
			} else {
				enrichMigrationMetadata(data, &model.Migration{Force: mm.Force, Type: mm.Type})
			}
		}
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInstance,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data:         data,
	}, nil
}

func extractVolumeInfo(db *gorm.DB, resourceID int64, args []string, idArgIndex int) (*ResourceChangeEvent, error) {
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

	// Fallback: after detach, instance_id is cleared to 0 in the DB (detach_volume RPC),
	// but the previous instance ID is still available in args[1].
	// Only trigger when IDArgIndex==2 (volume ID at args[2], instance ID at args[1]).
	if row.InstanceUUID == "" && idArgIndex == 2 && len(args) > 1 {
		if instID, err := strconv.ParseInt(args[1], 10, 64); err == nil {
			type instUUID struct {
				UUID string
			}
			u := &instUUID{}
			if err := db.Raw(`SELECT uuid FROM instances WHERE id = ? LIMIT 1`, instID).Scan(u).Error; err != nil {
				logger.Warningf("[%s] extractVolumeInfo: fallback instance query failed inst_id=%d: %v", traceID, instID, err)
			} else if u.UUID != "" {
				row.InstanceUUID = u.UUID
				logger.Debugf("[%s] extractVolumeInfo: fallback found instance_uuid=%s from args[1]=%d", traceID, u.UUID, instID)
			}
		}
	}

	if row.TenantUUID == "" {
		// Fallback log for unusual defects.
		logger.Warningf("[%s] extractVolumeInfo: tenant uuid empty (owner_id=%d) volume_uuid=%s elapsed=%s",
			traceID, row.Owner, row.UUID, time.Since(start))
	} else {
		logger.Debugf("[%s] extractVolumeInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s instance_uuid=%s status=%s size=%d",
			traceID, time.Since(start),
			row.ID, row.UUID, row.Owner, row.TenantUUID,
			row.InstanceUUID, row.Status, row.Size)
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeVolume,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"id":            row.UUID,
			"name":          row.Name,
			"status":        row.Status,
			"size":          row.Size,
			"instance_uuid": row.InstanceUUID,
			"target":        row.Target,
			"format":        row.Format,
			"path":          row.Path,
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
		logger.Debugf("[%s] extractImageInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s status=%s size=%d name=%s",
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
			"id":           row.UUID,
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

	data := map[string]interface{}{
		"name":          row.Name,
		"id":            row.UUID,
		"status":        status,
		"mac_address":   row.MacAddr,
		"is_primary":    row.PrimaryIf,
		"inbound":       row.Inbound,
		"outbound":      row.Outbound,
		"instance_uuid": row.InstanceUUID,
		"hyper_id":      row.Hyper,
		"type":          row.Type,
	}

	// Primary address + subnet
	if row.IpAddress != "" {
		data["ip_address"] = row.IpAddress
	}
	if row.SubnetUUID != "" {
		data["subnet"] = map[string]string{
			"id":   row.SubnetUUID,
			"name": row.SubnetName,
		}
	}

	// Primary-interface-only data (secondary addresses, site subnets, floating IPs)
	if row.PrimaryIf {
		// Secondary addresses (only for primary interface, matching API getInterfaceResponse)
		{
			type secAddrRow struct {
				Address    string
				SubnetUUID string `gorm:"column:subnet_uuid"`
				SubnetName string `gorm:"column:subnet_name"`
			}
			secRows := []*secAddrRow{}
			qSec := `SELECT a.address, s.uuid AS subnet_uuid, s.name AS subnet_name
				FROM addresses a
				LEFT JOIN subnets s ON s.id = a.subnet_id
				WHERE a.second_interface = ? AND a.allocated = true`
			if err := db.Raw(qSec, row.ID).Scan(&secRows).Error; err != nil {
				logger.Warningf("[%s] extractInterfaceInfo: secondary addresses query failed: %v", traceID, err)
			} else if len(secRows) > 0 {
				secAddrs := make([]map[string]interface{}, len(secRows))
				for i, sr := range secRows {
					secAddrs[i] = map[string]interface{}{
						"ip_address": sr.Address,
						"subnet": map[string]string{
							"id":   sr.SubnetUUID,
							"name": sr.SubnetName,
						},
					}
				}
				data["secondary_addresses"] = secAddrs
			}
		}

		// Site subnets
		type siteRow struct {
			UUID    string
			Name    string
			Network string
			Gateway string
			Netmask string
			Start   string
			End     string
			Vlan    int64
		}
		siteRows := []*siteRow{}
		qSite := `SELECT uuid, name, network, gateway, netmask, start, "end", vlan
			FROM subnets WHERE interface = ?`
		if err := db.Raw(qSite, row.ID).Scan(&siteRows).Error; err != nil {
			logger.Warningf("[%s] extractInterfaceInfo: site subnets query failed: %v", traceID, err)
		} else if len(siteRows) > 0 {
			sites := make([]map[string]interface{}, len(siteRows))
			for i, sr := range siteRows {
				sites[i] = map[string]interface{}{
					"id":      sr.UUID,
					"name":    sr.Name,
					"network": sr.Network,
					"gateway": sr.Gateway,
					"netmask": sr.Netmask,
					"start":   sr.Start,
					"end":     sr.End,
					"vlan":    sr.Vlan,
				}
			}
			data["site_subnets"] = sites
		}

		// Floating IPs (only for primary interface)
		{
			type fipRow struct {
				UUID       string
				Name       string
				IpAddress  string `gorm:"column:ip_address"`
				FipAddress string `gorm:"column:fip_address"`
				Type       string
			}
			fipRows := []*fipRow{}
			qFip := `SELECT uuid, name, ip_address, fip_address, type
				FROM floating_ips WHERE instance_id = ?`
			if err := db.Raw(qFip, resourceID).Scan(&fipRows).Error; err != nil {
				logger.Warningf("[%s] extractInterfaceInfo: floating ips query failed: %v", traceID, err)
			} else if len(fipRows) > 0 {
				fips := make([]map[string]interface{}, len(fipRows))
				for i, fr := range fipRows {
					fips[i] = map[string]interface{}{
						"id":          fr.UUID,
						"name":        fr.Name,
						"ip_address":  fr.IpAddress,
						"fip_address": fr.FipAddress,
						"type":        fr.Type,
					}
				}
				data["floating_ips"] = fips
			}
		}
	}

	// Security groups
	{
		type sgRow struct {
			UUID string
			Name string
		}
		sgRows := []*sgRow{}
		qSG := `SELECT sg.uuid, sg.name
			FROM security_groups sg
			INNER JOIN secgroup_ifaces si ON si.security_group_id = sg.id
			WHERE si.interface_id = ?`
		if err := db.Raw(qSG, row.ID).Scan(&sgRows).Error; err != nil {
			logger.Warningf("[%s] extractInterfaceInfo: security groups query failed: %v", traceID, err)
		} else if len(sgRows) > 0 {
			sgs := make([]map[string]string, len(sgRows))
			for i, sr := range sgRows {
				sgs[i] = map[string]string{
					"id":   sr.UUID,
					"name": sr.Name,
				}
			}
			data["security_groups"] = sgs
		}
	}

	if row.TenantUUID == "" {
		logger.Warningf("[%s] extractInterfaceInfo: tenant uuid empty (owner_id=%d) nic_uuid=%s elapsed=%s",
			traceID, row.Owner, row.UUID, time.Since(start))
	} else {
		logger.Debugf("[%s] extractInterfaceInfo: query OK elapsed=%s id=%d uuid=%s owner_id=%d tenant_uuid=%s instance_uuid=%s mac=%s primary_if=%t type=%s",
			traceID, time.Since(start),
			row.ID, row.UUID, row.Owner, row.TenantUUID,
			row.InstanceUUID, row.MacAddr, row.PrimaryIf, row.Type)
	}

	return &ResourceChangeEvent{
		ResourceType: ResourceTypeInterface,
		ResourceUUID: row.UUID,
		TenantID:     row.TenantUUID,
		Timestamp:    time.Now(),
		Data:         data,
	}, nil
}
