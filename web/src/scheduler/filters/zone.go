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
	scheduler.RegisterFilter("zone", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &ZoneFilter{}
	})
}

// ZoneFilter keeps only hosts matching the requested zone.
type ZoneFilter struct{}

func (f *ZoneFilter) Name() string { return "zone" }

func (f *ZoneFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	// No zone constraint, pass all
	if req.ZoneID <= 0 {
		logger.Debug("zone: no zone constraint, passing all hosts")
		return hosts
	}
	var result []*scheduler.HostState
	for _, h := range hosts {
		if h.ZoneID == req.ZoneID {
			result = append(result, h)
		}
	}
	logger.Debugf("zone: zone_id=%d, matched %d of %d hosts", req.ZoneID, len(result), len(hosts))
	return result
}
