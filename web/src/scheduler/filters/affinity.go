/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filters

import (
	"context"

	. "web/src/common"
	"web/src/model"
	"web/src/scheduler"
)

func init() {
	scheduler.RegisterFilter("affinity", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &AffinityFilter{}
	})
}

// AffinityFilter applies owner-based affinity or anti-affinity within a zone.
// - affinity: VMs of the same owner should land on the same hyper(s)
// - anti-affinity: VMs of the same owner should spread across different hypers
type AffinityFilter struct{}

func (f *AffinityFilter) Name() string { return "affinity" }

func (f *AffinityFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	// No affinity policy or no owner context, pass all
	if req.Policy == "" || req.OwnerID <= 0 {
		return hosts
	}

	logger.Debugf("affinity: policy=%s, ownerID=%d, zoneID=%d", req.Policy, req.OwnerID, req.ZoneID)

	// Query hypers occupied by the same owner in the same zone
	occupiedHypers, err := getOwnerHypers(ctx, req.OwnerID, req.ZoneID)
	if err != nil {
		logger.Warningf("affinity: failed to query owner hypers, passing all: %v", err)
		return hosts
	}
	logger.Debugf("affinity: owner %d occupies hypers %v in zone %d", req.OwnerID, occupiedHypers, req.ZoneID)

	occupiedSet := make(map[int32]bool, len(occupiedHypers))
	for _, hid := range occupiedHypers {
		occupiedSet[hid] = true
	}

	var result []*scheduler.HostState
	switch req.Policy {
	case "affinity":
		// Keep only hypers where this owner already has VMs (or all if no existing VMs)
		if len(occupiedHypers) == 0 {
			logger.Debug("affinity: no existing VMs for this owner, passing all hosts")
			return hosts
		}
		for _, h := range hosts {
			if occupiedSet[h.HyperID] {
				result = append(result, h)
			}
		}
		logger.Debugf("affinity: affinity policy kept %d of %d hosts", len(result), len(hosts))

	case "anti-affinity":
		// Exclude hypers where this owner already has VMs
		for _, h := range hosts {
			if !occupiedSet[h.HyperID] {
				result = append(result, h)
			}
		}
		logger.Debugf("affinity: anti-affinity policy kept %d of %d hosts", len(result), len(hosts))
		// If anti-affinity eliminates all, fall back to all hosts (better than no placement)
		if len(result) == 0 && len(hosts) > 0 {
			logger.Info("affinity: anti-affinity eliminated all hosts, falling back to full list")
			return hosts
		}
	}

	return result
}

// getOwnerHypers returns distinct hyper IDs where the given owner has active VMs in the zone.
func getOwnerHypers(ctx context.Context, ownerID, zoneID int64) ([]int32, error) {
	_, db := GetContextDB(ctx)
	var hypers []int32
	query := db.Model(&model.Instance{}).
		Select("DISTINCT hyper").
		Where("owner = ? AND hyper > 0", ownerID)
	if zoneID > 0 {
		query = query.Where("zone_id = ?", zoneID)
	}
	if err := query.Pluck("hyper", &hypers).Error; err != nil {
		return nil, err
	}
	return hypers, nil
}
