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
	"time"

	. "web/src/common"
	rlog "web/src/utils/log"
)

var logger = rlog.MustGetLogger("scheduler")

var (
	ErrNoValidHost       = errors.New("no valid host found")
	ErrNoHyperNode       = errors.New("no hyper nodes available")
	ErrSchedulerNotReady = errors.New("placement scheduler not initialized")
)

// SelectHost runs the Filter+Weigher chain and returns the best hyper's Hostid.
// The effective config is resolved per-zone: if req.ZoneID has a [placement.zone.<id>]
// override in placement.toml, those overrides are merged with the global config for this
// scheduling call only.  Host candidates are pre-scoped to req.ZoneID at DB query level.
// A structured DecisionLog is recorded for every call (success or failure).
func SelectHost(ctx context.Context, req *PlacementRequest) (int32, error) {
	startTime := time.Now()
	logger.Debugf("SelectHost entry: vcpus=%d, memMB=%d, diskGB=%d, zoneID=%d",
		req.VCPUs, req.MemMB, req.DiskGB, req.ZoneID)

	// Initialize decision log
	dlog := &DecisionLog{
		Timestamp:    startTime,
		Request:      req,
		SelectedHost: -1,
	}
	defer func() {
		dlog.DurationMs = float64(time.Since(startTime).Microseconds()) / 1000.0
		recordDecision(dlog)
		logger.Debugf("SelectHost completed in %.2fms, success=%v", dlog.DurationMs, dlog.Success)
	}()

	// Ensure the scheduler is initialized
	if activeSnapshot.Load() == nil {
		logger.Error("Scheduler not initialized, no active config snapshot")
		dlog.RejectReason = "scheduler not initialized"
		return -1, NewCLError(ErrPlacementNotReady, "Scheduler not initialized, no active config snapshot", ErrSchedulerNotReady)
	}

	// 1. Resolve effective config for this zone (per-zone override or global fallback).
	//    This is done per-call so that per-zone overrides take effect immediately after
	//    a reload without needing to re-start the service.
	cfg := ResolveZoneConfig(req.ZoneID)

	// 2. Build Filter / Weigher chains from the resolved config.
	//    Each scheduling call gets its own chain instances; this is cheap since chains
	//    are just slices of lightweight structs.
	filters := BuildFilters(cfg)
	weighers := BuildWeighers(cfg)

	// Build fallback filter from resolved config
	var fallback Filter
	if cfg.FallbackFilter != "" {
		registryMu.RLock()
		if factory, ok := filterRegistry[cfg.FallbackFilter]; ok {
			fallback = factory(cfg)
		} else {
			logger.Warningf("SelectHost: fallback filter %q not found in registry", cfg.FallbackFilter)
		}
		registryMu.RUnlock()
	}

	// 3. Load host states for the requested zone.
	//    loadHostStatesWithCache queries DB with WHERE zone_id=?, so the returned list
	//    is already scoped to this zone — no ZoneFilter needed in the chain.
	logger.Debugf("Loading host states for zoneID=%d", req.ZoneID)
	hosts, err := loadHostStatesWithCache(ctx, req.ZoneID)
	if err != nil {
		logger.Errorf("Failed to load host states: %v", err)
		dlog.RejectReason = fmt.Sprintf("failed to load host states: %v", err)
		return -1, NewCLError(ErrPlacementHostStateLoadFailed, fmt.Sprintf("Failed to load host states for zone_id=%d", req.ZoneID), err)
	}
	dlog.TotalHosts = len(hosts)
	if len(hosts) == 0 {
		logger.Warningf("No active hyper nodes found for zone_id=%d", req.ZoneID)
		dlog.RejectReason = fmt.Sprintf("no active hyper nodes in zone_id=%d", req.ZoneID)
		return -1, NewCLError(ErrPlacementNoHyperNodes, fmt.Sprintf("No active hyper nodes in zone_id=%d", req.ZoneID), ErrNoHyperNode)
	}
	logger.Debugf("Loaded %d active host(s) for zone_id=%d", len(hosts), req.ZoneID)

	// 4. Phase 1: Filter chain (order and members from resolved zone/global config)
	logger.Debugf("Starting filter chain with %d filter(s) for zoneID=%d", len(filters), req.ZoneID)
	candidates := hosts
	lastFilterName := ""
	for _, f := range filters {
		before := len(candidates)
		candidates = f.Filter(ctx, req, candidates)
		after := len(candidates)

		// Record filter step
		step := FilterStep{
			Name:        f.Name(),
			InputCount:  before,
			OutputCount: after,
			Eliminated:  before - after,
		}
		dlog.FilterSteps = append(dlog.FilterSteps, step)

		if after < before {
			logger.Debugf("Filter %q: %d -> %d candidates (eliminated %d)", f.Name(), before, after, before-after)
		}
		if len(candidates) == 0 {
			lastFilterName = f.Name()
			logger.Infof("Filter %q eliminated all remaining candidates", f.Name())
			break
		}
	}

	// 5. Fallback: overcommit tolerance path (uses zone-resolved config)
	if len(candidates) == 0 && fallback != nil {
		dlog.FallbackUsed = true
		dlog.FallbackName = fallback.Name()
		logger.Infof("Standard filters eliminated all %d hosts, trying fallback filter %q (zoneID=%d)",
			len(hosts), fallback.Name(), req.ZoneID)
		candidates = fallback.Filter(ctx, req, hosts)
		for _, h := range candidates {
			h.IsOvercommit = true
		}
		dlog.FallbackResult = len(candidates)
		if len(candidates) > 0 {
			logger.Infof("Fallback filter recovered %d candidate(s) via overcommit tolerance", len(candidates))
		} else {
			logger.Warning("Fallback filter also found no candidates")
		}
	}

	if len(candidates) == 0 {
		// Build structured rejection reason
		reason := fmt.Sprintf("all %d hosts eliminated", len(hosts))
		if lastFilterName != "" {
			reason = fmt.Sprintf("filter %q eliminated all candidates (started with %d hosts)", lastFilterName, len(hosts))
		}
		if dlog.FallbackUsed {
			reason += fmt.Sprintf("; fallback %q also found no candidates", dlog.FallbackName)
		}
		dlog.RejectReason = reason
		logger.Warningf("SelectHost failed: %s, zone_id=%d", reason, req.ZoneID)
		return -1, NewCLError(ErrPlacementNoValidHost, reason, ErrNoValidHost)
	}

	dlog.CandidateCount = len(candidates)

	// 6. Phase 2: Weigher chain scoring (from resolved zone/global config)
	logger.Debugf("Starting weigher chain with %d weigher(s) on %d candidate(s)", len(weighers), len(candidates))
	best := weightAndPick(weighers, req, candidates, dlog)

	dlog.Success = true
	dlog.SelectedHost = best.HyperID
	dlog.IsOvercommit = best.IsOvercommit
	logger.Infof("SelectHost result: selected hyper %d (zoneID=%d, overcommit=%v) from %d candidates",
		best.HyperID, best.ZoneID, best.IsOvercommit, len(candidates))
	return best.HyperID, nil
}

// weightAndPick normalizes and scores all candidates, returns the highest scorer.
// Records weigher steps into the decision log.
func weightAndPick(weighers []Weigher, req *PlacementRequest, hosts []*HostState, dlog *DecisionLog) *HostState {
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

		// Record weigher step
		step := WeigherStep{
			Name:       w.Name(),
			Multiplier: w.Multiplier(),
			MinRaw:     minV,
			MaxRaw:     maxV,
		}
		dlog.WeigherSteps = append(dlog.WeigherSteps, step)
		logger.Debugf("Weigher %q: multiplier=%.1f, raw range=[%.2f, %.2f]", w.Name(), w.Multiplier(), minV, maxV)
	}

	best := 0
	for i := range hosts {
		if scores[i] > scores[best] {
			best = i
		}
	}
	logger.Debugf("Weigher scores: best=hyper %d (score=%.4f), total candidates=%d",
		hosts[best].HyperID, scores[best], len(hosts))
	return hosts[best]
}
