/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import "web/src/scheduler"

func init() {
	scheduler.RegisterWeigher("ram", func(cfg *scheduler.PlacementConfig) scheduler.Weigher {
		return &RAMWeigher{multiplier: cfg.Weighers.RAMMultiplier}
	})
}

type RAMWeigher struct {
	multiplier float64
}

func (w *RAMWeigher) Name() string       { return "ram" }
func (w *RAMWeigher) Multiplier() float64 { return w.multiplier }

func (w *RAMWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	return float64(h.MemFreeKB / 1024) // convert to MB for scoring
}
