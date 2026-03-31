/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"sort"
	"sync"
)

// FilterFactory creates a Filter instance from the current config snapshot.
type FilterFactory func(cfg *PlacementConfig) Filter

// WeigherFactory creates a Weigher instance from the current config snapshot.
type WeigherFactory func(cfg *PlacementConfig) Weigher

var (
	filterRegistry  = map[string]FilterFactory{}
	weigherRegistry = map[string]WeigherFactory{}
	registryMu      sync.RWMutex
)

// RegisterFilter registers a Filter factory (called from init()).
func RegisterFilter(name string, factory FilterFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	filterRegistry[name] = factory
}

// RegisterWeigher registers a Weigher factory (called from init()).
func RegisterWeigher(name string, factory WeigherFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	weigherRegistry[name] = factory
}

// BuildFilters constructs a Filter chain from config's name list.
func BuildFilters(cfg *PlacementConfig) []Filter {
	registryMu.RLock()
	defer registryMu.RUnlock()

	var chain []Filter
	for _, name := range cfg.FilterChain {
		if factory, ok := filterRegistry[name]; ok {
			chain = append(chain, factory(cfg))
		} else {
			logger.Warningf("placement: unknown filter %q in config, skipped", name)
		}
	}
	return chain
}

// BuildWeighers constructs a Weigher chain from config's name list.
func BuildWeighers(cfg *PlacementConfig) []Weigher {
	registryMu.RLock()
	defer registryMu.RUnlock()

	var chain []Weigher
	for _, name := range cfg.WeigherChain {
		if factory, ok := weigherRegistry[name]; ok {
			chain = append(chain, factory(cfg))
		} else {
			logger.Warningf("placement: unknown weigher %q in config, skipped", name)
		}
	}
	return chain
}

// GetRegisteredFilters returns sorted names of all registered filters.
func GetRegisteredFilters() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(filterRegistry))
	for name := range filterRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetRegisteredWeighers returns sorted names of all registered weighers.
func GetRegisteredWeighers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(weigherRegistry))
	for name := range weigherRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
