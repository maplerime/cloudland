/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package weighers

import (
	"strings"
	"testing"

	"web/src/scheduler"
)

func TestSpreadWeigher_Score_PackPicksLeastFreeVCPU(t *testing.T) {
	w := &SpreadWeigher{multiplier: 1.0, packThreshold: 4}
	req := &scheduler.PlacementRequest{VCPUs: 2} // <= threshold -> pack

	tight := &scheduler.HostState{HyperID: 1, VCPUFree: 6}
	roomy := &scheduler.HostState{HyperID: 2, VCPUFree: 60}

	if got := w.Score(req, tight); got <= w.Score(req, roomy) {
		t.Errorf("pack: tight host score %v should be > roomy host score %v (best-fit prefers least free vCPU)",
			got, w.Score(req, roomy))
	}
}

func TestSpreadWeigher_Score_SpreadPicksMostFreeVCPU(t *testing.T) {
	w := &SpreadWeigher{multiplier: 1.0, packThreshold: 4}
	req := &scheduler.PlacementRequest{VCPUs: 6} // > threshold -> spread

	tight := &scheduler.HostState{HyperID: 1, VCPUFree: 6}
	roomy := &scheduler.HostState{HyperID: 2, VCPUFree: 60}

	if got := w.Score(req, roomy); got <= w.Score(req, tight) {
		t.Errorf("spread: roomy host score %v should be > tight host score %v (worst-fit prefers most free vCPU)",
			got, w.Score(req, tight))
	}
}

func TestSpreadWeigher_Score_ThresholdZeroDisablesPack(t *testing.T) {
	w := &SpreadWeigher{multiplier: 1.0, packThreshold: 0}
	req := &scheduler.PlacementRequest{VCPUs: 1} // would be <= any positive threshold

	tight := &scheduler.HostState{HyperID: 1, VCPUFree: 6}
	roomy := &scheduler.HostState{HyperID: 2, VCPUFree: 60}

	if got := w.Score(req, roomy); got <= w.Score(req, tight) {
		t.Errorf("threshold=0: should always spread (roomy score %v should be > tight score %v)",
			got, w.Score(req, tight))
	}
}

func TestSpreadWeigher_Explain(t *testing.T) {
	w := &SpreadWeigher{multiplier: 1.0, packThreshold: 4}

	if got := w.Explain(&scheduler.PlacementRequest{VCPUs: 2}); !strings.Contains(got, "pack") {
		t.Errorf("Explain(2 vCPUs, threshold=4) = %q, want it to mention pack", got)
	}
	if got := w.Explain(&scheduler.PlacementRequest{VCPUs: 6}); !strings.Contains(got, "spread") {
		t.Errorf("Explain(6 vCPUs, threshold=4) = %q, want it to mention spread", got)
	}

	disabled := &SpreadWeigher{multiplier: 1.0, packThreshold: 0}
	if got := disabled.Explain(&scheduler.PlacementRequest{VCPUs: 1}); !strings.Contains(got, "disabled") {
		t.Errorf("Explain with threshold=0 = %q, want it to mention disabled", got)
	}
}
