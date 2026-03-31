/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math"

	rlog "web/src/utils/log"
)

var logger = rlog.MustGetLogger("scheduler")

var (
	ErrNoValidHost          = errors.New("no valid host found")
	ErrNoHyperNode          = errors.New("no hyper nodes available")
	ErrSchedulerNotReady    = errors.New("placement scheduler not initialized")
)

// SelectHost runs the Filter+Weigher chain and returns the best hyper's Hostid.
func SelectHost(ctx context.Context, req *PlacementRequest) (int32, error) {
	snap := GetSnapshot()
	if snap == nil {
		return -1, ErrSchedulerNotReady
	}

	hosts, err := loadHostStates(ctx, req.ZoneID)
	if err != nil {
		return -1, fmt.Errorf("failed to load host states: %w", err)
	}
	if len(hosts) == 0 {
		return -1, ErrNoHyperNode
	}

	// Phase 1: Filter chain
	candidates := hosts
	for _, f := range snap.filters {
		candidates = f.Filter(ctx, req, candidates)
		if len(candidates) == 0 {
			logger.Debugf("placement: filter %q eliminated all candidates", f.Name())
			break
		}
	}

	// Fallback: overcommit tolerance path
	if len(candidates) == 0 && snap.fallback != nil {
		logger.Info("placement: standard filters eliminated all hosts, trying fallback")
		candidates = snap.fallback.Filter(ctx, req, hosts)
		for _, h := range candidates {
			h.IsOvercommit = true
		}
	}

	if len(candidates) == 0 {
		return -1, fmt.Errorf("%w: zone_id=%d", ErrNoValidHost, req.ZoneID)
	}

	// Phase 2: Weigher chain
	best := weightAndPick(snap.weighers, req, candidates)
	logger.Debugf("placement: selected hyper %d (overcommit=%v) from %d candidates",
		best.HyperID, best.IsOvercommit, len(candidates))
	return best.HyperID, nil
}

// weightAndPick normalizes and scores all candidates, returns the highest scorer.
func weightAndPick(weighers []Weigher, req *PlacementRequest, hosts []*HostState) *HostState {
	if len(hosts) == 1 {
		return hosts[0]
	}
	if len(weighers) == 0 {
		return hosts[0]
	}

	scores := make([]float64, len(hosts))
	for _, w := range weighers {
		raw := make([]float64, len(hosts))
		minV, maxV := math.MaxFloat64, -math.MaxFloat64
		for i, h := range hosts {
			raw[i] = w.Score(req, h)
			if raw[i] < minV {
				minV = raw[i]
			}
			if raw[i] > maxV {
				maxV = raw[i]
			}
		}
		span := maxV - minV
		for i := range hosts {
			normalized := 0.0
			if span > 1e-9 {
				normalized = (raw[i] - minV) / span
			}
			scores[i] += w.Multiplier() * normalized
		}
	}

	best := 0
	for i := range hosts {
		if scores[i] > scores[best] {
			best = i
		}
	}
	return hosts[best]
}
