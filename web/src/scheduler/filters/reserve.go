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
	scheduler.RegisterFilter("reserve", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &ReserveFilter{count: cfg.Filters.Reserve.Count}
	})
}

// ReserveFilter keeps the top-N nodes (by free vCPU) available for large orders.
// It excludes those nodes from the candidate pool unless no other nodes pass.
// This prevents small orders from consuming the nodes best suited for large orders.
type ReserveFilter struct {
	count int
}

func (f *ReserveFilter) Name() string { return "reserve" }

func (f *ReserveFilter) Filter(_ context.Context, _ *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	if f.count <= 0 || len(hosts) <= f.count {
		return hosts
	}

	// Sort by VCPUFree descending to identify the top-N reserved nodes.
	sorted := make([]*scheduler.HostState, len(hosts))
	copy(sorted, hosts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].VCPUFree > sorted[j].VCPUFree
	})

	reserved := make(map[int32]struct{}, f.count)
	for i := 0; i < f.count; i++ {
		reserved[sorted[i].HyperID] = struct{}{}
	}

	var nonReserved []*scheduler.HostState
	for _, h := range hosts {
		if _, ok := reserved[h.HyperID]; !ok {
			nonReserved = append(nonReserved, h)
		}
	}
	if len(nonReserved) == 0 {
		// All candidates are reserved nodes — allow using them.
		logger.Debugf("reserve: all %d candidates are reserved nodes, falling through", len(hosts))
		return hosts
	}
	logger.Debugf("reserve: excluded %d reserved node(s), %d candidate(s) remain", f.count, len(nonReserved))
	return nonReserved
}
