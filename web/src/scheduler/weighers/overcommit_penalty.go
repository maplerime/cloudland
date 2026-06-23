/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import "web/src/scheduler"

func init() {
	scheduler.RegisterWeigher("overcommit_penalty", func(cfg *scheduler.PlacementConfig) scheduler.Weigher {
		return &OvercommitPenaltyWeigher{multiplier: cfg.Weighers.OvercommitPenaltyMultiplier}
	})
}

type OvercommitPenaltyWeigher struct {
	multiplier float64
}

func (w *OvercommitPenaltyWeigher) Name() string       { return "overcommit_penalty" }
func (w *OvercommitPenaltyWeigher) Multiplier() float64 { return w.multiplier }

func (w *OvercommitPenaltyWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	if h.IsOvercommit {
		return 0
	}
	return 1
}
