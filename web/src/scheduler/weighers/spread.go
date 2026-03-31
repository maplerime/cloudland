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

// SpreadWeigher is a Phase 2 stub. Currently returns 0 for all hosts.
// In Phase 2, it will use instance count per host and support spread/pack modes.
type SpreadWeigher struct {
	multiplier float64
}

func (w *SpreadWeigher) Name() string       { return "spread" }
func (w *SpreadWeigher) Multiplier() float64 { return w.multiplier }

func (w *SpreadWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	return 0 // no-op until Phase 2
}
