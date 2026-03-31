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
	scheduler.RegisterFilter("cpu_load", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		threshold := cfg.Filters.CPULoad.IdleThresholdPct
		if threshold <= 0 {
			threshold = 15.0
		}
		return &CPULoadFilter{threshold: threshold}
	})
}

type CPULoadFilter struct {
	threshold float64
}

func (f *CPULoadFilter) Name() string { return "cpu_load" }

func (f *CPULoadFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	var result []*scheduler.HostState
	for _, h := range hosts {
		if h.CpuIdlePct >= f.threshold {
			result = append(result, h)
		}
	}
	return result
}
