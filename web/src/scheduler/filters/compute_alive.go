/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filters

import (
	"context"
	"time"

	rlog "web/src/utils/log"

	"web/src/scheduler"
)

var logger = rlog.MustGetLogger("scheduler.filters")

func init() {
	scheduler.RegisterFilter("compute_alive", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &ComputeAliveFilter{
			threshold: time.Duration(cfg.HostReportIntervalSec*2) * time.Second,
		}
	})
}

// ComputeAliveFilter removes hosts whose last heartbeat exceeds the alive threshold.
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
		} else {
			// Heartbeat timeout, host removed
			logger.Debugf("compute_alive: hyper %d last report %v exceeded threshold %v, removed",
				h.HyperID, h.LastReportAt.Format("15:04:05"), f.threshold)
		}
	}
	return result
}
