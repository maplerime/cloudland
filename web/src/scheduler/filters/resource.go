/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filters

import (
	"context"

	"web/src/scheduler"
)

func init() {
	scheduler.RegisterFilter("resource", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &ResourceFilter{}
	})
}

// ResourceFilter checks vCPU, disk, and memory (non-hugepage) sufficiency.
type ResourceFilter struct{}

func (f *ResourceFilter) Name() string { return "resource" }

func (f *ResourceFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	var result []*scheduler.HostState
	for _, h := range hosts {
		// vCPU check
		if h.VCPUAvail() < int64(req.VCPUs) {
			logger.Debugf("resource: hyper %d vCPU avail %d < requested %d, removed",
				h.HyperID, h.VCPUAvail(), req.VCPUs)
			continue
		}
		// Disk check (req in GB, host in bytes)
		if h.DiskAvailGB() < req.DiskGB {
			logger.Debugf("resource: hyper %d disk avail %dGB < requested %dGB, removed",
				h.HyperID, h.DiskAvailGB(), req.DiskGB)
			continue
		}
		// Memory check for non-hugepage hosts (hugepage hosts checked by HugepageFilter)
		if h.HugepageSizeKB == 0 {
			memFreeMB := h.MemFreeKB / 1024
			if memFreeMB < req.MemMB {
				logger.Debugf("resource: hyper %d mem avail %dMB < requested %dMB, removed",
					h.HyperID, memFreeMB, req.MemMB)
				continue
			}
		}
		result = append(result, h)
	}
	return result
}
