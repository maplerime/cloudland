/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filters

import (
	"context"

	"web/src/scheduler"
)

func init() {
	scheduler.RegisterFilter("capability", func(cfg *scheduler.PlacementConfig) scheduler.Filter {
		return &CapabilityFilter{}
	})
}

// CapabilityFilter keeps only hosts that have all requested traits (tag names).
// Tag value is not matched — only presence of the tag key is checked.
type CapabilityFilter struct{}

func (f *CapabilityFilter) Name() string { return "capability" }

func (f *CapabilityFilter) Filter(ctx context.Context, req *scheduler.PlacementRequest, hosts []*scheduler.HostState) []*scheduler.HostState {
	// No traits required, pass all
	if len(req.Traits) == 0 {
		logger.Debug("capability: no traits required, passing all hosts")
		return hosts
	}

	var result []*scheduler.HostState
	for _, h := range hosts {
		if hasAllTraits(h.Tags, req.Traits) {
			result = append(result, h)
		} else {
			logger.Debugf("capability: hyper %d missing required traits %v (has %v), removed",
				h.HyperID, req.Traits, h.Tags)
		}
	}
	logger.Debugf("capability: %d of %d hosts have all required traits %v", len(result), len(hosts), req.Traits)
	return result
}

// hasAllTraits checks if the tag map contains all required trait names.
func hasAllTraits(tags map[string]string, required []string) bool {
	if tags == nil {
		return false
	}
	for _, trait := range required {
		if _, ok := tags[trait]; !ok {
			return false
		}
	}
	return true
}
