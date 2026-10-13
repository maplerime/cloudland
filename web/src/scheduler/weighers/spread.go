/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import (
	"fmt"
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

// SpreadWeigher scores hosts by free vCPU with a tiered best-fit/worst-fit strategy.
//
// When req.VCPUs <= packThreshold (small orders): pack (best-fit) — prefer the
// host with the LEAST free vCPU that can still take the request (ResourceFilter
// upstream already guarantees every candidate fits it), so small orders top off
// nodes that are already tight instead of eating into roomy ones.
//
// Otherwise: spread (worst-fit) — prefer the host with the MOST free vCPU
// (current default), keeping large orders spread across the roomiest nodes.
//
// Direction is encoded in Score (pack: -VCPUFree, spread: +VCPUFree) so that
// Multiplier always returns a positive weight and the normalizer works uniformly.
type SpreadWeigher struct {
	multiplier    float64
	packThreshold int32 // VCPUs <= this → pack; 0 disables tiered logic
}

func (w *SpreadWeigher) Name() string { return "spread" }

// Multiplier returns the weight magnitude. Direction is in Score, so this is always positive.
func (w *SpreadWeigher) Multiplier() float64 { return math.Abs(w.multiplier) }

func (w *SpreadWeigher) isPack(req *scheduler.PlacementRequest) bool {
	return w.packThreshold > 0 && req.VCPUs <= w.packThreshold
}

func (w *SpreadWeigher) Score(req *scheduler.PlacementRequest, h *scheduler.HostState) float64 {
	if w.isPack(req) {
		// pack (best-fit): less free vCPU left over = higher score = preferred
		return -float64(h.VCPUFree)
	}
	// spread (worst-fit): more free vCPU = higher score = preferred
	return float64(h.VCPUFree)
}

// Explain returns a one-line, human-readable reason for the mode this request
// triggered — used only to make logs and decision records readable, never for scoring.
func (w *SpreadWeigher) Explain(req *scheduler.PlacementRequest) string {
	if w.packThreshold <= 0 {
		return "pack disabled (pack_vcpu_threshold=0), always spread: prefer the host with the most free vCPU"
	}
	if w.isPack(req) {
		return fmt.Sprintf("pack (best-fit): request %d vCPUs <= threshold %d, prefer the host with the least free vCPU that still fits", req.VCPUs, w.packThreshold)
	}
	return fmt.Sprintf("spread (worst-fit): request %d vCPUs > threshold %d, prefer the host with the most free vCPU", req.VCPUs, w.packThreshold)
}
