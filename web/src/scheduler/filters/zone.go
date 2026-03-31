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

type ZoneFilter struct{}

func (f *ZoneFilter) Name() string { return "zone" }

func (f *ZoneFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	if req.ZoneID <= 0 {
		return hosts
	}
	var result []*scheduler.HostState
	for _, h := range hosts {
		if h.ZoneID == req.ZoneID {
			result = append(result, h)
		}
	}
	return result
}
