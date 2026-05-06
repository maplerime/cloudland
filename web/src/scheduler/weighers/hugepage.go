/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import "web/src/scheduler"

func init() {
	scheduler.RegisterWeigher("hugepage", func(cfg *scheduler.PlacementConfig) scheduler.Weigher {
		return &HugepageWeigher{multiplier: cfg.Weighers.HugepageMultiplier}
	})
}

type HugepageWeigher struct {
	multiplier float64
}

func (w *HugepageWeigher) Name() string       { return "hugepage" }
func (w *HugepageWeigher) Multiplier() float64 { return w.multiplier }

func (w *HugepageWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	return float64(h.HugepageFreeMB())
}
