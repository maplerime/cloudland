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

type ResourceFilter struct{}

func (f *ResourceFilter) Name() string { return "resource" }

func (f *ResourceFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	var result []*scheduler.HostState
	for _, h := range hosts {
		// vCPU check
		if h.VCPUAvail() < int64(req.VCPUs) {
			continue
		}
		// Disk check (req in GB, host in bytes)
		if h.DiskAvailGB() < req.DiskGB {
			continue
		}
		// Memory check for non-hugepage hosts (hugepage hosts checked by HugepageFilter)
		if h.HugepageSizeKB == 0 {
			memFreeMB := h.MemFreeKB / 1024
			if memFreeMB < req.MemMB {
				continue
			}
		}
		result = append(result, h)
	}
	return result
}
