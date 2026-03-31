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

type HugepageFilter struct{}

func (f *HugepageFilter) Name() string { return "hugepage" }

func (f *HugepageFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	var result []*scheduler.HostState
	for _, h := range hosts {
		// Node has no hugepages enabled
		if h.HugepageSizeKB == 0 {
			if req.HugepageSizeKB == 0 {
				result = append(result, h) // neither side requires hugepages
			}
			continue
		}

		// Request requires a specific hugepage size; must match
		if req.HugepageSizeKB != 0 && req.HugepageSizeKB != h.HugepageSizeKB {
			continue
		}

		// Hugepage free MB must satisfy VM memory requirement
		if h.HugepageFreeMB() >= req.MemMB {
			result = append(result, h)
		}
	}
	return result
}
