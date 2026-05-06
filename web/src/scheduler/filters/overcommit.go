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

// OvercommitFilter is a fallback filter that allows hosts with small CPU gaps.
// Only invoked when the standard filter chain eliminates all candidates.
//
// Only CPU is overcommittable (time-sliced). Memory/hugepage/disk are
// incompressible resources — overcommitting them risks OOM or startup failure,
// so this filter still enforces them as hard requirements.
type OvercommitFilter struct {
	cfg *scheduler.PlacementConfig
}

func (f *OvercommitFilter) Name() string { return "overcommit" }

func (f *OvercommitFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	if !f.cfg.Overcommit.Enabled {
		logger.Debug("overcommit: fallback disabled in config")
		return nil
	}
	logger.Debugf("overcommit: evaluating %d host(s) for CPU overcommit tolerance", len(hosts))

	var result []*scheduler.HostState
	for _, h := range hosts {
		if f.canOvercommit(req, h) {
			result = append(result, h)
			logger.Debugf("overcommit: hyper %d accepted (vcpu_gap=%.4f)", h.HyperID, f.vcpuGap(req, h))
		}
	}
	// Sort by CPU gap: smallest gap first.
	sort.Slice(result, func(i, j int) bool {
		return f.vcpuGap(req, result[i]) < f.vcpuGap(req, result[j])
	})
	logger.Debugf("overcommit: %d of %d host(s) passed CPU overcommit tolerance", len(result), len(hosts))
	return result
}

func (f *OvercommitFilter) canOvercommit(req *scheduler.PlacementRequest, h *scheduler.HostState) bool {
	oc := f.cfg.Overcommit

	// Memory/hugepage/disk are hard requirements — never overcommit (incompressible).
	// If the standard chain rejected this host for one of these, we must too.
	if h.HugepageSizeKB > 0 {
		if h.HugepageFreeMB() < req.MemMB {
			logger.Debugf("overcommit: hyper %d hugepage short (%dMB < %dMB), rejected",
				h.HyperID, h.HugepageFreeMB(), req.MemMB)
			return false
		}
	} else {
		if h.MemFreeKB/1024 < req.MemMB {
			logger.Debugf("overcommit: hyper %d memory short (%dMB < %dMB), rejected",
				h.HyperID, h.MemFreeKB/1024, req.MemMB)
			return false
		}
	}
	if h.DiskAvailGB() < req.DiskGB {
		logger.Debugf("overcommit: hyper %d disk avail %dGB < requested %dGB, rejected",
			h.HyperID, h.DiskAvailGB(), req.DiskGB)
		return false
	}

	// vCPU gap check — this is the only overcommit dimension.
	// Note: VCPUTotal already incorporates the hyper's CpuOverRate from report_rc.sh,
	// so this threshold is an additional gap on top of the already-overcommitted capacity.
	vcpuAvail := h.VCPUFree
	if vcpuAvail < int64(req.VCPUs) {
		vcpuDeltaRatio := float64(int64(req.VCPUs)-vcpuAvail) / float64(req.VCPUs) * 100
		if vcpuDeltaRatio > oc.VCPUDeltaRatioPct {
			logger.Debugf("overcommit: hyper %d vCPU gap %.1f%% > threshold %.1f%%, rejected",
				h.HyperID, vcpuDeltaRatio, oc.VCPUDeltaRatioPct)
			return false
		}
	}

	// CPU load relaxed threshold.
	if h.CpuIdlePct < oc.CPUIdleFallbackPct {
		logger.Debugf("overcommit: hyper %d cpu_idle %.1f%% < fallback threshold %.1f%%, rejected",
			h.HyperID, h.CpuIdlePct, oc.CPUIdleFallbackPct)
		return false
	}

	return true
}

// vcpuGap returns the vCPU shortfall ratio; lower = closer to meeting requirements.
func (f *OvercommitFilter) vcpuGap(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	if h.VCPUFree >= int64(req.VCPUs) {
		return 0
	}
	return float64(int64(req.VCPUs)-h.VCPUFree) / float64(req.VCPUs)
}
