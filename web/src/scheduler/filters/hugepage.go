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
	scheduler.RegisterFilter("hugepage", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &HugepageFilter{}
	})
}

// HugepageFilter ensures hosts have sufficient hugepage blocks for the requested VM memory.
type HugepageFilter struct{}

func (f *HugepageFilter) Name() string { return "hugepage" }

func (f *HugepageFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	if req.HugepageSizeKB == 0 {
		// Standard VM with no explicit hugepage page-size requirement.
		// WDS hypervisors always use <memoryBacking><hugepages> in their domain XML,
		// so every VM placed on a WDS host consumes hugepage memory regardless.
		// ResourceFilter already skips the regular memory check for WDS hosts
		// (h.HugepageSizeKB > 0), so we must enforce the hugepage memory check here.
		var result []*scheduler.HostState
		for _, h := range hosts {
			if h.HugepageSizeKB > 0 {
				freeMB := h.HugepageFreeMB()
				if freeMB < req.MemMB {
					logger.Debugf("hugepage: hyper %d (WDS, pageSize=%dKB) hugepage free %dMB < requested %dMB, removed",
						h.HyperID, h.HugepageSizeKB, freeMB, req.MemMB)
					continue
				}
			}
			result = append(result, h)
		}
		return result
	}

	// VM explicitly requests a specific hugepage page size.
	var result []*scheduler.HostState
	for _, h := range hosts {
		// Host has no hugepages; VM requires them — skip.
		if h.HugepageSizeKB == 0 {
			logger.Debugf("hugepage: hyper %d has no hugepages, request requires %dKB pages, removed",
				h.HyperID, req.HugepageSizeKB)
			continue
		}

		// Page size must match.
		if req.HugepageSizeKB != h.HugepageSizeKB {
			logger.Debugf("hugepage: hyper %d page size %dKB != requested %dKB, removed",
				h.HyperID, h.HugepageSizeKB, req.HugepageSizeKB)
			continue
		}

		// Hugepage free MB must satisfy VM memory requirement.
		freeMB := h.HugepageFreeMB()
		if freeMB >= req.MemMB {
			result = append(result, h)
		} else {
			logger.Debugf("hugepage: hyper %d hugepage free %dMB < requested %dMB, removed",
				h.HyperID, freeMB, req.MemMB)
		}
	}
	return result
}
