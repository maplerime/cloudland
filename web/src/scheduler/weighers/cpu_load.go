/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import "web/src/scheduler"

func init() {
	scheduler.RegisterWeigher("cpu_load", func(cfg *scheduler.PlacementConfig) scheduler.Weigher {
		return &CPULoadWeigher{multiplier: cfg.Weighers.CPULoadMultiplier}
	})
}

type CPULoadWeigher struct {
	multiplier float64
}

func (w *CPULoadWeigher) Name() string       { return "cpu_load" }
func (w *CPULoadWeigher) Multiplier() float64 { return w.multiplier }

func (w *CPULoadWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	return h.CpuIdlePct // 0~100, higher idle = more desirable
}
