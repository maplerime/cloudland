/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import (
	"math"

	"web/src/scheduler"
)

func init() {
	scheduler.RegisterWeigher("spread", func(cfg *scheduler.PlacementConfig) scheduler.Weigher {
		return &SpreadWeigher{
			multiplier:    cfg.Weighers.SpreadMultiplier,
			packThreshold: cfg.Weighers.PackVCPUThreshold,
		}
	})
}

// SpreadWeigher scores hosts by instance count with tiered pack/spread strategy.
//
// When req.VCPUs <= packThreshold (small orders): pack strategy — prefer nodes
// already hosting more VMs, leaving low-utilization nodes free for large orders.
//
// Otherwise: spread strategy — prefer nodes with fewer VMs (current default).
//
// Direction is encoded in Score (positive = pack, negative = spread) so that
// Multiplier always returns a positive weight and the normalizer works uniformly.
type SpreadWeigher struct {
	multiplier    float64
	packThreshold int32 // VCPUs <= this → pack; 0 disables tiered logic
}

func (w *SpreadWeigher) Name() string { return "spread" }

// Multiplier returns the weight magnitude. Direction is in Score, so this is always positive.
func (w *SpreadWeigher) Multiplier() float64 { return math.Abs(w.multiplier) }

func (w *SpreadWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	if w.packThreshold > 0 && req.VCPUs <= w.packThreshold {
		// pack: more VMs on this node = higher score = preferred
		return float64(h.InstanceCount)
	}
	// spread: fewer VMs on this node = higher score = preferred (via negative raw)
	return -float64(h.InstanceCount)
}
