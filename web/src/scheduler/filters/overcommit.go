/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filters

import (
	"context"
	"sort"

	"web/src/scheduler"
)

func init() {
	scheduler.RegisterFilter("overcommit", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &OvercommitFilter{cfg: cfg}
	})
}

type OvercommitFilter struct {
	cfg *scheduler.PlacementConfig
}

func (f *OvercommitFilter) Name() string { return "overcommit" }

func (f *OvercommitFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	if !f.cfg.Overcommit.Enabled {
		return nil
	}
	var result []*scheduler.HostState
	for _, h := range hosts {
		if f.canOvercommit(req, h) {
			result = append(result, h)
		}
	}
	// Sort by gap: smallest gap first
	sort.Slice(result, func(i, j int) bool {
		return f.delta(req, result[i]) < f.delta(req, result[j])
	})
	return result
}

func (f *OvercommitFilter) canOvercommit(req *scheduler.PlacementRequest, h *scheduler.HostState) bool {
	oc := f.cfg.Overcommit

	// Memory / hugepage gap check
	if h.HugepageSizeKB > 0 {
		availHP := h.HugepageFreeMB()
		if availHP < req.MemMB {
			hpDeltaRatio := float64(req.MemMB-availHP) / float64(req.MemMB) * 100
			if hpDeltaRatio > oc.HugepageDeltaRatioPct {
				return false
			}
		}
	} else {
		availMem := h.MemFreeKB / 1024
		if availMem < req.MemMB {
			memDeltaRatio := float64(req.MemMB-availMem) / float64(req.MemMB) * 100
			if memDeltaRatio > oc.MemDeltaRatioPct {
				return false
			}
		}
	}

	// vCPU gap check (delta-based, same approach as memory).
	// Note: VCPUTotal already incorporates the hyper's CpuOverRate from report_rc.sh,
	// so we do NOT apply an additional overcommit ratio here.
	// We only check if the deficit is within a tolerable percentage of what's requested.
	vcpuAvail := h.VCPUFree
	if vcpuAvail < int64(req.VCPUs) {
		vcpuDeltaRatio := float64(int64(req.VCPUs)-vcpuAvail) / float64(req.VCPUs) * 100
		if vcpuDeltaRatio > oc.VCPUDeltaRatioPct {
			return false
		}
	}

	// CPU load relaxed threshold
	if h.CpuIdlePct < oc.CPUIdleFallbackPct {
		return false
	}

	// Disk never overcommit
	if h.DiskAvailGB() < req.DiskGB {
		return false
	}

	return true
}

// delta calculates a combined gap score; lower = closer to meeting requirements.
// Memory/hugepage gap weighted 70%, vCPU gap weighted 30%.
func (f *OvercommitFilter) delta(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	var memRatio float64
	if h.HugepageSizeKB > 0 {
		avail := h.HugepageFreeMB()
		if avail < req.MemMB {
			memRatio = float64(req.MemMB-avail) / float64(req.MemMB)
		}
	} else {
		avail := h.MemFreeKB / 1024
		if avail < req.MemMB {
			memRatio = float64(req.MemMB-avail) / float64(req.MemMB)
		}
	}

	vcpuRatio := 0.0
	if h.VCPUFree < int64(req.VCPUs) {
		vcpuRatio = float64(int64(req.VCPUs)-h.VCPUFree) / float64(req.VCPUs)
	}

	return memRatio*0.7 + vcpuRatio*0.3
}
