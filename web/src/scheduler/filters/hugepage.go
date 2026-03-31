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
	var result []*scheduler.HostState
	for _, h := range hosts {
		// Node has no hugepages enabled
		if h.HugepageSizeKB == 0 {
			if req.HugepageSizeKB == 0 {
				// Neither side requires hugepages, pass through
				result = append(result, h)
			} else {
				logger.Debugf("hugepage: hyper %d has no hugepages, request requires %dKB pages, removed",
					h.HyperID, req.HugepageSizeKB)
			}
			continue
		}

		// Request requires a specific hugepage size; must match
		if req.HugepageSizeKB != 0 && req.HugepageSizeKB != h.HugepageSizeKB {
			logger.Debugf("hugepage: hyper %d page size %dKB != requested %dKB, removed",
				h.HyperID, h.HugepageSizeKB, req.HugepageSizeKB)
			continue
		}

		// Hugepage free MB must satisfy VM memory requirement
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
