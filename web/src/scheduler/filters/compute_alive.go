/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filters

import (
	"context"
	"time"

	"web/src/scheduler"
)

func init() {
	scheduler.RegisterFilter("compute_alive", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &ComputeAliveFilter{
			threshold: time.Duration(cfg.HostReportIntervalSec*2) * time.Second,
		}
	})
}

type ComputeAliveFilter struct {
	threshold time.Duration
}

func (f *ComputeAliveFilter) Name() string { return "compute_alive" }

func (f *ComputeAliveFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	deadline := time.Now().Add(-f.threshold)
	var result []*scheduler.HostState
	for _, h := range hosts {
		if h.LastReportAt.After(deadline) {
			result = append(result, h)
		}
	}
	return result
}
