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
	VCPUFree       int64
	VCPUTotal      int64
	MemFreeKB      int64 // free memory in KB (as reported by report_rc)
	MemTotalKB     int64
	DiskFreeBytes  int64 // free disk in bytes
	DiskTotalBytes int64

	// Hugepage
	Hugepages2MFree int64
	Hugepages1GFree int64
	HugepageSizeKB  int64

	// CPU Load
	LoadAvg5m  float64
	CpuIdlePct float64

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

// loadHostStates queries active hypers with their resources from the database.
func loadHostStates(ctx context.Context, zoneID int64) ([]*HostState, error) {
	_, db := GetContextDB(ctx)
	hypers := []*model.Hyper{}

	query := db.Where("status = 1 AND hostid >= 0")
	if zoneID > 0 {
		query = query.Where("zone_id = ?", zoneID)
	}
	if err := query.Preload("Resource").Preload("Zone").Find(&hypers).Error; err != nil {
		return nil, err
	}

	var hosts []*HostState
	for _, h := range hypers {
		if h.Resource == nil {
			continue // no resource data yet
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
			Hugepages2MFree: h.Resource.Hugepages2MFree,
			Hugepages1GFree: h.Resource.Hugepages1GFree,
			HugepageSizeKB:  h.Resource.HugepageSizeKB,
			LoadAvg5m:       h.Resource.LoadAvg5m,
			CpuIdlePct:      h.Resource.CpuIdlePct,
			LastReportAt:    h.Resource.UpdatedAt,
		}
		if h.Zone != nil {
			hs.ZoneName = h.Zone.Name
		}
		hosts = append(hosts, hs)
	}
	return hosts, nil
}
