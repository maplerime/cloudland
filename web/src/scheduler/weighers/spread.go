/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import "web/src/scheduler"

func init() {
	scheduler.RegisterWeigher("spread", func(cfg *scheduler.PlacementConfig) scheduler.Weigher {
		return &SpreadWeigher{multiplier: cfg.Weighers.SpreadMultiplier}
	})
}

// SpreadWeigher scores hosts by their current instance count.
// With negative multiplier (default -1.0): fewer VMs = higher final score (spread/打散).
// With positive multiplier: more VMs = higher final score (pack/堆叠).
type SpreadWeigher struct {
	multiplier float64
}

func (w *SpreadWeigher) Name() string       { return "spread" }
func (w *SpreadWeigher) Multiplier() float64 { return w.multiplier }

func (w *SpreadWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	return float64(h.InstanceCount)
}
