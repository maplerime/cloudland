/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package filters provides built-in placement filter implementations.
package filters

import (
	"context"

	"web/src/scheduler"
)

// ZoneFilter keeps only hosts matching the requested zone.
//
// Deprecated (v2.1): This filter is no longer needed and should be removed from
// placement.toml filter_chain.  Since v2.1, host candidates are pre-scoped to the
// requested zone at the DB query level (WHERE zone_id = ?) by loadHostStates, so by
// the time the filter chain runs the list already contains only zone-local hosts.
// Keeping this registration prevents a startup warning if old config files still list
// "zone" in filter_chain, but it has no functional effect: it simply passes all hosts
// through when the DB query has already filtered by zone_id.
func init() {
	scheduler.RegisterFilter("zone", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &ZoneFilter{}
	})
}

// ZoneFilter keeps only hosts matching the requested zone.
type ZoneFilter struct{}

func (f *ZoneFilter) Name() string { return "zone" }

func (f *ZoneFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	// Since v2.1 the host list is already zone-scoped by the DB query.
	// This filter is a no-op: pass all hosts through unchanged.
	if req.ZoneID <= 0 {
		logger.Debug("zone(deprecated): no zone constraint, passing all hosts")
		return hosts
	}
	var result []*scheduler.HostState
	for _, h := range hosts {
		if h.ZoneID == req.ZoneID {
			result = append(result, h)
		}
	}
	logger.Debugf("zone(deprecated): zone_id=%d, matched %d of %d hosts (should equal input — DB already filtered)",
		req.ZoneID, len(result), len(hosts))
	return result
}
