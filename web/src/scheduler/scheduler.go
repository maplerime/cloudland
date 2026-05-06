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
	"sort"
	"time"

	. "web/src/common"
	"web/src/model"
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

	// 1. Resolve effective config for this zone (per-zone override or global fallback).
	//    This is done per-call so that per-zone overrides take effect immediately after
	//    a reload without needing to re-start the service.
	cfg := ResolveZoneConfig(req.ZoneID)

	// Initialize decision log
	dlog := &DecisionLog{
		Timestamp:    startTime,
		Request:      req,
		SelectedHost: -1,
	}
	defer func() {
		dlog.DurationMs = float64(time.Since(startTime).Microseconds()) / 1000.0
		recordDecision(cfg, dlog)
		logger.Debugf("SelectHost completed in %.2fms, success=%v", dlog.DurationMs, dlog.Success)
	}()

	// Ensure the scheduler is initialized
	if activeSnapshot.Load() == nil {
		logger.Error("Scheduler not initialized, no active config snapshot")
		dlog.RejectReason = "scheduler not initialized"
		return -1, NewCLError(ErrPlacementNotReady, "Scheduler not initialized, no active config snapshot", ErrSchedulerNotReady)
	}

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

	// Filter out excluded hypers (e.g. migration source)
	hosts = excludeHypers(hosts, req.ExcludeHypers)

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

	scores := weightAndScore(weighers, req, hosts, dlog)

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

// excludeHypers removes hosts whose HyperID is in the exclude list.
func excludeHypers(hosts []*HostState, exclude []int32) []*HostState {
	if len(exclude) == 0 {
		return hosts
	}
	excSet := make(map[int32]struct{}, len(exclude))
	for _, id := range exclude {
		excSet[id] = struct{}{}
	}
	var filtered []*HostState
	for _, h := range hosts {
		if _, skip := excSet[h.HyperID]; !skip {
			filtered = append(filtered, h)
		}
	}
	if len(filtered) < len(hosts) {
		logger.Debugf("excludeHypers: removed %d host(s) from candidates", len(hosts)-len(filtered))
	}
	return filtered
}

// weightAndScore normalizes and scores all candidates, returns scores array.
// Shared by weightAndPick and QueryAvailableHosts.
func weightAndScore(weighers []Weigher, req *PlacementRequest, hosts []*HostState, dlog *DecisionLog) []float64 {
	scores := make([]float64, len(hosts))
	if len(hosts) == 0 || len(weighers) == 0 {
		return scores
	}
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
		if dlog != nil {
			step := WeigherStep{
				Name:       w.Name(),
				Multiplier: w.Multiplier(),
				MinRaw:     minV,
				MaxRaw:     maxV,
			}
			dlog.WeigherSteps = append(dlog.WeigherSteps, step)
		}
		logger.Debugf("Weigher %q: multiplier=%.1f, raw range=[%.2f, %.2f]", w.Name(), w.Multiplier(), minV, maxV)
	}
	return scores
}

// HostCandidate represents a hyper that passed the filter chain with its weigher score.
type HostCandidate struct {
	HyperID       int32   `json:"hyper_id"`
	ZoneName      string  `json:"zone_name"`
	VCPUFree      int64   `json:"vcpu_free"`
	VCPUTotal     int64   `json:"vcpu_total"`
	MemFreeMB     int64   `json:"mem_free_mb"`
	MemTotalMB    int64   `json:"mem_total_mb"`
	DiskFreeGB    int64   `json:"disk_free_gb"`
	DiskTotalGB   int64   `json:"disk_total_gb"`
	CpuIdlePct    float64 `json:"cpu_idle_pct"`
	InstanceCount int     `json:"instance_count"`
	Score         float64 `json:"score"`
	IsOvercommit  bool    `json:"is_overcommit"`
}

// QueryAvailableHosts runs the filter+weigher chain and returns ALL passing
// candidates sorted by weigher score (highest first).
func QueryAvailableHosts(ctx context.Context, req *PlacementRequest) ([]*HostCandidate, error) {
	if activeSnapshot.Load() == nil {
		return nil, NewCLError(ErrPlacementNotReady, "Scheduler not initialized", ErrSchedulerNotReady)
	}
	cfg := ResolveZoneConfig(req.ZoneID)
	filters := BuildFilters(cfg)
	weighers := BuildWeighers(cfg)

	var fallback Filter
	if cfg.FallbackFilter != "" {
		registryMu.RLock()
		if factory, ok := filterRegistry[cfg.FallbackFilter]; ok {
			fallback = factory(cfg)
		}
		registryMu.RUnlock()
	}

	hosts, err := loadHostStatesWithCache(ctx, req.ZoneID)
	if err != nil {
		return nil, NewCLError(ErrPlacementHostStateLoadFailed, "Failed to load host states", err)
	}
	hosts = excludeHypers(hosts, req.ExcludeHypers)
	if len(hosts) == 0 {
		return nil, NewCLError(ErrPlacementNoHyperNodes, fmt.Sprintf("No active hyper nodes in zone_id=%d", req.ZoneID), ErrNoHyperNode)
	}

	// Filter chain
	candidates := hosts
	for _, f := range filters {
		candidates = f.Filter(ctx, req, candidates)
		if len(candidates) == 0 {
			break
		}
	}

	// Fallback
	if len(candidates) == 0 && fallback != nil {
		candidates = fallback.Filter(ctx, req, hosts)
		for _, h := range candidates {
			h.IsOvercommit = true
		}
	}

	if len(candidates) == 0 {
		return []*HostCandidate{}, nil
	}

	// Score and sort
	scores := weightAndScore(weighers, req, candidates, nil)
	result := make([]*HostCandidate, len(candidates))
	for i, h := range candidates {
		result[i] = &HostCandidate{
			HyperID:       h.HyperID,
			ZoneName:      h.ZoneName,
			VCPUFree:      h.VCPUFree,
			VCPUTotal:     h.VCPUTotal,
			MemFreeMB:     h.MemFreeKB / 1024,
			MemTotalMB:    h.MemTotalKB / 1024,
			DiskFreeGB:    h.DiskFreeBytes / (1024 * 1024 * 1024),
			DiskTotalGB:   h.DiskTotalBytes / (1024 * 1024 * 1024),
			CpuIdlePct:    h.CpuIdlePct,
			InstanceCount: h.InstanceCount,
			Score:         scores[i],
			IsOvercommit:  h.IsOvercommit,
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	return result, nil
}

// ValidateHostForVM checks whether a specific hyper can host a VM with the given requirements.
// Returns nil if the hyper passes filter chain (or overcommit fallback), otherwise a descriptive error.
func ValidateHostForVM(ctx context.Context, hyperID int32, req *PlacementRequest) error {
	if activeSnapshot.Load() == nil {
		return NewCLError(ErrPlacementNotReady, "Scheduler not initialized", ErrSchedulerNotReady)
	}

	// Load fresh host state for this specific hyper (skip cache for accuracy)
	_, db := GetContextDB(ctx)
	hyper := &model.Hyper{}
	if err := db.Where("hostid = ? AND status = 1", hyperID).Preload("Zone").Take(hyper).Error; err != nil {
		return NewCLError(ErrPlacementNoHyperNodes, fmt.Sprintf("Hyper %d not found or not active", hyperID), err)
	}

	// Check zone match if specified
	if req.ZoneID > 0 && hyper.ZoneID != req.ZoneID {
		return NewCLError(ErrPlacementInsufficientResource,
			fmt.Sprintf("Hyper %d is in zone %d, not in requested zone %d", hyperID, hyper.ZoneID, req.ZoneID), nil)
	}

	// Load resource
	resource := &model.Resource{}
	if err := db.Where("hostid = ?", hyperID).Take(resource).Error; err != nil {
		return NewCLError(ErrPlacementHostStateLoadFailed, fmt.Sprintf("No resource data for hyper %d", hyperID), err)
	}

	hs := &HostState{
		HyperID:         hyper.Hostid,
		ZoneID:          hyper.ZoneID,
		VCPUFree:        resource.Cpu,
		VCPUTotal:       resource.CpuTotal,
		MemFreeKB:       resource.Memory,
		MemTotalKB:      resource.MemoryTotal,
		DiskFreeBytes:   resource.Disk,
		DiskTotalBytes:  resource.DiskTotal,
		CpuOverRate:     hyper.CpuOverRate,
		MemOverRate:     hyper.MemOverRate,
		DiskOverRate:    hyper.DiskOverRate,
		Hugepages2MFree: resource.Hugepages2MFree,
		Hugepages1GFree: resource.Hugepages1GFree,
		HugepageSizeKB:  resource.HugepageSizeKB,
		LoadAvg5m:       resource.LoadAvg5m,
		CpuIdlePct:      resource.CpuIdlePct,
		LastReportAt:    resource.UpdatedAt,
	}

	// Resolve zone config and build filter chain
	zoneID := req.ZoneID
	if zoneID == 0 {
		zoneID = hyper.ZoneID
	}
	cfg := ResolveZoneConfig(zoneID)
	filters := BuildFilters(cfg)

	// Run filter chain on this single host
	candidates := []*HostState{hs}
	for _, f := range filters {
		candidates = f.Filter(ctx, req, candidates)
		if len(candidates) == 0 {
			// Try fallback
			if cfg.FallbackFilter != "" {
				registryMu.RLock()
				factory, ok := filterRegistry[cfg.FallbackFilter]
				registryMu.RUnlock()
				if ok {
					fb := factory(cfg)
					candidates = fb.Filter(ctx, req, []*HostState{hs})
					if len(candidates) > 0 {
						return nil // passed via fallback
					}
				}
			}
			return NewCLError(ErrPlacementInsufficientResource,
				fmt.Sprintf("Hyper %d rejected by filter %q", hyperID, f.Name()), nil)
		}
	}
	return nil
}
