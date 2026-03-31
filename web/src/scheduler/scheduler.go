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
	ErrNoValidHost       = errors.New("no valid host found")
	ErrNoHyperNode       = errors.New("no hyper nodes available")
	ErrSchedulerNotReady = errors.New("placement scheduler not initialized")
)

// SelectHost runs the Filter+Weigher chain and returns the best hyper's Hostid.
func SelectHost(ctx context.Context, req *PlacementRequest) (int32, error) {
	logger.Debugf("SelectHost entry: vcpus=%d, memMB=%d, diskGB=%d, zoneID=%d, hugepageSizeKB=%d",
		req.VCPUs, req.MemMB, req.DiskGB, req.ZoneID, req.HugepageSizeKB)

	snap := GetSnapshot()
	if snap == nil {
		logger.Error("Scheduler not initialized, no active config snapshot")
		return -1, ErrSchedulerNotReady
	}

	// Load host states from database
	logger.Debug("Loading host states from database")
	hosts, err := loadHostStates(ctx, req.ZoneID)
	if err != nil {
		logger.Errorf("Failed to load host states: %v", err)
		return -1, fmt.Errorf("failed to load host states: %w", err)
	}
	if len(hosts) == 0 {
		logger.Warningf("No active hyper nodes found for zone_id=%d", req.ZoneID)
		return -1, ErrNoHyperNode
	}
	logger.Debugf("Loaded %d active host(s) for zone_id=%d", len(hosts), req.ZoneID)

	// Phase 1: Filter chain
	logger.Debugf("Starting filter chain with %d filter(s)", len(snap.filters))
	candidates := hosts
	for _, f := range snap.filters {
		before := len(candidates)
		candidates = f.Filter(ctx, req, candidates)
		after := len(candidates)
		// Log state change when filter eliminates hosts
		if after < before {
			logger.Debugf("Filter %q: %d -> %d candidates (eliminated %d)", f.Name(), before, after, before-after)
		}
		if len(candidates) == 0 {
			logger.Infof("Filter %q eliminated all remaining candidates", f.Name())
			break
		}
	}

	// Fallback: overcommit tolerance path
	if len(candidates) == 0 && snap.fallback != nil {
		logger.Infof("Standard filters eliminated all %d hosts, trying fallback filter %q", len(hosts), snap.fallback.Name())
		candidates = snap.fallback.Filter(ctx, req, hosts)
		for _, h := range candidates {
			h.IsOvercommit = true
		}
		if len(candidates) > 0 {
			logger.Infof("Fallback filter recovered %d candidate(s) via overcommit tolerance", len(candidates))
		} else {
			logger.Warning("Fallback filter also found no candidates")
		}
	}

	if len(candidates) == 0 {
		logger.Warningf("SelectHost failed: no valid host for zone_id=%d, vcpus=%d, memMB=%d", req.ZoneID, req.VCPUs, req.MemMB)
		return -1, fmt.Errorf("%w: zone_id=%d", ErrNoValidHost, req.ZoneID)
	}

	// Phase 2: Weigher chain
	logger.Debugf("Starting weigher chain with %d weigher(s) on %d candidate(s)", len(snap.weighers), len(candidates))
	best := weightAndPick(snap.weighers, req, candidates)
	logger.Infof("SelectHost result: selected hyper %d (zone=%d, overcommit=%v) from %d candidates",
		best.HyperID, best.ZoneID, best.IsOvercommit, len(candidates))
	return best.HyperID, nil
}

// weightAndPick normalizes and scores all candidates, returns the highest scorer.
func weightAndPick(weighers []Weigher, req *PlacementRequest, hosts []*HostState) *HostState {
	// Short-circuit: single candidate or no weighers
	if len(hosts) == 1 {
		logger.Debugf("Single candidate hyper %d, skipping weigher scoring", hosts[0].HyperID)
		return hosts[0]
	}
	if len(weighers) == 0 {
		logger.Debug("No weighers configured, returning first candidate")
		return hosts[0]
	}

	scores := make([]float64, len(hosts))
	// Evaluate each weigher
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
		// Normalize and apply multiplier
		for i := range hosts {
			normalized := 0.0
			if span > 1e-9 {
				normalized = (raw[i] - minV) / span
			}
			scores[i] += w.Multiplier() * normalized
		}
		logger.Debugf("Weigher %q: multiplier=%.1f, raw range=[%.2f, %.2f]", w.Name(), w.Multiplier(), minV, maxV)
	}

	// Find highest scorer
	best := 0
	for i := range hosts {
		if scores[i] > scores[best] {
			best = i
		}
	}
	// Log final scores for top candidates
	logger.Debugf("Weigher scores: best=hyper %d (score=%.4f), total candidates=%d",
		hosts[best].HyperID, scores[best], len(hosts))
	return hosts[best]
}
