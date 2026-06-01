/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"context"
	"sync"
	"time"
)

// hostStateCache provides a TTL-based in-memory cache for loadHostStates results.
// This avoids querying the database on every scheduling call at large scale.
type hostStateCache struct {
	mu       sync.RWMutex
	entries  map[int64]*cacheEntry // keyed by zoneID (0 = all zones)
	ttl      time.Duration
}

type cacheEntry struct {
	hosts     []*HostState
	loadedAt  time.Time
}

const defaultCacheTTL = 10 * time.Second

var stateCache = &hostStateCache{
	entries: make(map[int64]*cacheEntry),
	ttl:     defaultCacheTTL,
}

// loadHostStatesWithCache returns cached host states if fresh, otherwise queries DB.
func loadHostStatesWithCache(ctx context.Context, zoneID int64) ([]*HostState, error) {
	// Check cache first
	stateCache.mu.RLock()
	if entry, ok := stateCache.entries[zoneID]; ok {
		if time.Since(entry.loadedAt) < stateCache.ttl {
			hosts := entry.hosts
			stateCache.mu.RUnlock()
			logger.Debugf("hostStateCache hit: zoneID=%d, %d hosts, age=%.1fs",
				zoneID, len(hosts), time.Since(entry.loadedAt).Seconds())
			return hosts, nil
		}
	}
	stateCache.mu.RUnlock()

	// Cache miss or expired, query DB
	logger.Debugf("hostStateCache miss: zoneID=%d, querying database", zoneID)
	hosts, err := loadHostStates(ctx, zoneID)
	if err != nil {
		return nil, err
	}

	// Store in cache
	stateCache.mu.Lock()
	stateCache.entries[zoneID] = &cacheEntry{
		hosts:    hosts,
		loadedAt: time.Now(),
	}
	stateCache.mu.Unlock()
	logger.Debugf("hostStateCache stored: zoneID=%d, %d hosts", zoneID, len(hosts))

	return hosts, nil
}

// InvalidateHostStateCache clears all cached host states.
// Called when config changes or explicitly by admin.
func InvalidateHostStateCache() {
	stateCache.mu.Lock()
	stateCache.entries = make(map[int64]*cacheEntry)
	stateCache.mu.Unlock()
	logger.Info("hostStateCache invalidated")
}
