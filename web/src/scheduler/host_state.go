/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"context"
	"time"

	. "web/src/common"
	"web/src/model"
)

// HostState is the in-memory representation of a hyper node's current state.
type HostState struct {
	HyperID    int32
	ZoneID     int64
	ZoneName   string

	// Compute resources (Resource table values are "free", not "used")
	// Note: VCPUTotal/MemTotalKB/DiskTotalBytes already incorporate hyper's overcommit ratio.
	// report_rc.sh calculates: total_cpu = physical_cpu * cpu_over_ratio
	VCPUFree       int64
	VCPUTotal      int64 // already multiplied by CpuOverRate
	MemFreeKB      int64 // free memory in KB
	MemTotalKB     int64 // already multiplied by MemOverRate
	DiskFreeBytes  int64
	DiskTotalBytes int64 // already multiplied by DiskOverRate

	// Hyper overcommit rates (from Hyper model, managed via admin UI)
	CpuOverRate  float32
	MemOverRate  float32
	DiskOverRate float32

	// Hugepage
	Hugepages2MFree int64
	Hugepages1GFree int64
	HugepageSizeKB  int64

	// CPU Load
	LoadAvg5m  float64
	CpuIdlePct float64

	// Spread/affinity data
	InstanceCount int               // number of VMs on this hyper (for SpreadWeigher)
	Tags          map[string]string // hyper tags (for CapabilityFilter)

	// Metadata
	LastReportAt time.Time

	// Scheduler-internal flag
	IsOvercommit bool
}

// HugepageFreeMB returns free hugepage memory in MB.
func (h *HostState) HugepageFreeMB() int64 {
	switch h.HugepageSizeKB {
	case 1048576: // 1GB pages
		return h.Hugepages1GFree * 1024
	case 2048: // 2MB pages
		return h.Hugepages2MFree * 2
	default:
		return 0
	}
}

// AvailMemMB returns available memory in MB (hugepage path preferred).
func (h *HostState) AvailMemMB() int64 {
	if h.HugepageSizeKB > 0 {
		return h.HugepageFreeMB()
	}
	return h.MemFreeKB / 1024
}

// VCPUAvail returns available vCPU count.
func (h *HostState) VCPUAvail() int64 {
	return h.VCPUFree
}

// DiskAvailGB returns available disk in GB.
func (h *HostState) DiskAvailGB() int64 {
	return h.DiskFreeBytes / (1024 * 1024 * 1024)
}

// instanceCountRow is used to scan the instance count per hyper query result.
type instanceCountRow struct {
	Hyper int32
	Count int
}

// loadHostStates queries active hypers with their resources from the database.
func loadHostStates(ctx context.Context, zoneID int64) ([]*HostState, error) {
	logger.Debugf("loadHostStates entry: zoneID=%d", zoneID)
	_, db := GetContextDB(ctx)
	hypers := []*model.Hyper{}

	query := db.Where("status = 1 AND hostid >= 0")
	if zoneID > 0 {
		query = query.Where("zone_id = ?", zoneID)
	}
	if err := query.Preload("Resource").Preload("Zone").Find(&hypers).Error; err != nil {
		logger.Errorf("loadHostStates DB query failed: %v", err)
		return nil, err
	}
	logger.Debugf("loadHostStates: queried %d hyper(s) from database", len(hypers))

	// Query instance count per hyper (for SpreadWeigher)
	instCountMap := make(map[int32]int)
	var countRows []instanceCountRow
	if err := db.Model(&model.Instance{}).
		Select("hyper, count(*) as count").
		Where("hyper > 0").
		Group("hyper").
		Scan(&countRows).Error; err != nil {
		logger.Warningf("loadHostStates: failed to query instance counts: %v", err)
		// Non-fatal: SpreadWeigher will treat all hosts as having 0 instances
	} else {
		for _, row := range countRows {
			instCountMap[row.Hyper] = row.Count
		}
		logger.Debugf("loadHostStates: loaded instance counts for %d hyper(s)", len(countRows))
	}

	// Query hyper tags (for CapabilityFilter)
	tagMap := make(map[int32]map[string]string)
	var tags []model.HyperTag
	if err := db.Find(&tags).Error; err != nil {
		logger.Warningf("loadHostStates: failed to query hyper tags: %v", err)
		// Non-fatal: CapabilityFilter will see empty tags
	} else {
		for _, t := range tags {
			if tagMap[t.Hostid] == nil {
				tagMap[t.Hostid] = make(map[string]string)
			}
			tagMap[t.Hostid][t.TagName] = t.TagValue
		}
		logger.Debugf("loadHostStates: loaded tags for %d hyper(s)", len(tagMap))
	}

	var hosts []*HostState
	for _, h := range hypers {
		if h.Resource == nil {
			// Skip hypers without resource data
			logger.Debugf("loadHostStates: hyper %d has no resource data, skipped", h.Hostid)
			continue
		}
		hs := &HostState{
			HyperID:         h.Hostid,
			ZoneID:          h.ZoneID,
			VCPUFree:        h.Resource.Cpu,
			VCPUTotal:       h.Resource.CpuTotal,
			MemFreeKB:       h.Resource.Memory,
			MemTotalKB:      h.Resource.MemoryTotal,
			DiskFreeBytes:   h.Resource.Disk,
			DiskTotalBytes:  h.Resource.DiskTotal,
			CpuOverRate:     h.CpuOverRate,
			MemOverRate:     h.MemOverRate,
			DiskOverRate:    h.DiskOverRate,
			Hugepages2MFree: h.Resource.Hugepages2MFree,
			Hugepages1GFree: h.Resource.Hugepages1GFree,
			HugepageSizeKB:  h.Resource.HugepageSizeKB,
			LoadAvg5m:       h.Resource.LoadAvg5m,
			CpuIdlePct:      h.Resource.CpuIdlePct,
			InstanceCount:   instCountMap[h.Hostid],
			Tags:            tagMap[h.Hostid],
			LastReportAt:    h.Resource.UpdatedAt,
		}
		if h.Zone != nil {
			hs.ZoneName = h.Zone.Name
		}
		hosts = append(hosts, hs)
	}
	logger.Debugf("loadHostStates exit: %d host(s) with valid resource data", len(hosts))
	return hosts, nil
}
