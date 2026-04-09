/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"
)

// configSnapshot is an immutable snapshot: config + pre-built Filter/Weigher chains.
// The chains in the snapshot are built from the GLOBAL config.
// Per-zone scheduling uses ResolveZoneConfig + BuildFilters/BuildWeighers on the fly.
type configSnapshot struct {
	cfg        *PlacementConfig
	filters    []Filter // built from global config (kept for reference / fallback)
	weighers   []Weigher
	fallback   Filter // may be nil
	loadedAt   time.Time
	configPath string
}

var (
	activeSnapshot atomic.Pointer[configSnapshot]
	configViper    *viper.Viper
	configFilePath string
)

// InitPlacementConfig loads the config file and builds the first snapshot.
// Called once at startup. Does not start file watching.
func InitPlacementConfig(path string) error {
	logger.Infof("InitPlacementConfig entry: path=%s", path)
	configFilePath = path
	configViper = viper.New()
	configViper.SetConfigFile(path)
	configViper.SetConfigType("toml")

	if err := configViper.ReadInConfig(); err != nil {
		// Config file not found, use defaults
		logger.Warningf("Placement config not found at %s, using defaults: %v", path, err)
		snapshot := buildSnapshot(defaultConfig(), path)
		activeSnapshot.Store(snapshot)
		logger.Info("InitPlacementConfig completed with default config")
		return nil
	}

	if err := doReload(); err != nil {
		logger.Errorf("InitPlacementConfig failed to load config: %v", err)
		return err
	}
	logger.Info("InitPlacementConfig completed successfully")
	return nil
}

// ReloadConfig re-reads the config file and atomically replaces the active snapshot.
// Called by the admin API handler.
func ReloadConfig() (*ReloadResult, error) {
	logger.Info("ReloadConfig entry: re-reading config file")
	if configViper == nil {
		logger.Error("ReloadConfig failed: config not initialized")
		return nil, fmt.Errorf("placement config not initialized")
	}
	// Re-read config file from disk
	if err := configViper.ReadInConfig(); err != nil {
		logger.Errorf("ReloadConfig failed to read config file %s: %v", configFilePath, err)
		return nil, fmt.Errorf("failed to read config file %s: %w", configFilePath, err)
	}
	if err := doReload(); err != nil {
		logger.Errorf("ReloadConfig failed: %v (keeping previous config)", err)
		return nil, err
	}
	snap := GetSnapshot()
	result := &ReloadResult{
		LoadedAt:     snap.loadedAt,
		ConfigPath:   snap.configPath,
		FilterChain:  snap.cfg.FilterChain,
		WeigherChain: snap.cfg.WeigherChain,
	}
	logger.Infof("ReloadConfig completed: filters=%v, weighers=%v, zone_overrides=%d",
		result.FilterChain, result.WeigherChain, len(snap.cfg.Zones))
	return result, nil
}

// ReloadResult is returned to the API caller after a successful reload.
type ReloadResult struct {
	LoadedAt     time.Time `json:"loaded_at"`
	ConfigPath   string    `json:"config_path"`
	FilterChain  []string  `json:"filter_chain"`
	WeigherChain []string  `json:"weigher_chain"`
}

func doReload() error {
	cfg := defaultConfig()
	// Unmarshal config from file
	if err := configViper.UnmarshalKey("placement", cfg); err != nil {
		return fmt.Errorf("config unmarshal failed: %w", err)
	}
	// Validate before applying
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	// Build and atomically store new snapshot
	snapshot := buildSnapshot(cfg, configFilePath)
	activeSnapshot.Store(snapshot)
	logger.Infof("Placement config loaded: filters=%v, weighers=%v, fallback=%q, zone_overrides=%d",
		cfg.FilterChain, cfg.WeigherChain, cfg.FallbackFilter, len(cfg.Zones))
	return nil
}

func buildSnapshot(cfg *PlacementConfig, path string) *configSnapshot {
	logger.Debug("Building config snapshot: constructing filter and weigher chains")
	snap := &configSnapshot{
		cfg:        cfg,
		loadedAt:   time.Now(),
		configPath: path,
	}
	snap.filters = BuildFilters(cfg)
	snap.weighers = BuildWeighers(cfg)
	// Build fallback filter if configured
	if cfg.FallbackFilter != "" {
		registryMu.RLock()
		if factory, ok := filterRegistry[cfg.FallbackFilter]; ok {
			snap.fallback = factory(cfg)
			logger.Debugf("Fallback filter %q loaded", cfg.FallbackFilter)
		} else {
			logger.Warningf("Fallback filter %q not found in registry, fallback disabled", cfg.FallbackFilter)
		}
		registryMu.RUnlock()
	}
	logger.Debugf("Config snapshot built: %d filters, %d weighers, fallback=%v, zone_overrides=%d",
		len(snap.filters), len(snap.weighers), snap.fallback != nil, len(cfg.Zones))
	return snap
}

// validateConfig checks config values for sanity before applying.
func validateConfig(cfg *PlacementConfig) error {
	if len(cfg.FilterChain) == 0 {
		return fmt.Errorf("filter_chain must not be empty")
	}
	if len(cfg.WeigherChain) == 0 {
		return fmt.Errorf("weigher_chain must not be empty")
	}
	if err := validateFilterChain(cfg.FilterChain, "placement.filter_chain"); err != nil {
		return err
	}
	if err := validateWeigherChain(cfg.WeigherChain, "placement.weigher_chain"); err != nil {
		return err
	}
	if cfg.FallbackFilter != "" {
		if err := validateFilterName(cfg.FallbackFilter, "placement.fallback_filter"); err != nil {
			return err
		}
	}
	if cfg.Overcommit.Enabled {
		if cfg.Overcommit.MemDeltaRatioPct < 0 || cfg.Overcommit.MemDeltaRatioPct > 100 {
			return fmt.Errorf("overcommit.mem_delta_ratio_pct must be in [0, 100]")
		}
		if cfg.Overcommit.VCPUDeltaRatioPct < 0 || cfg.Overcommit.VCPUDeltaRatioPct > 100 {
			return fmt.Errorf("overcommit.vcpu_delta_ratio_pct must be in [0, 100]")
		}
	}
	if hasFilter(cfg.FilterChain, "hugepage") && cfg.Filters.Hugepage.PageSizeKB <= 0 {
		return fmt.Errorf("filters.hugepage.page_size_kb must be > 0 when hugepage filter is enabled")
	}
	for zoneID, zone := range cfg.Zones {
		if zone == nil {
			continue
		}
		if zone.FilterChain != nil {
			if len(*zone.FilterChain) == 0 {
				return fmt.Errorf("placement.zone.%s.filter_chain must not be empty when set", zoneID)
			}
			if err := validateFilterChain(*zone.FilterChain, fmt.Sprintf("placement.zone.%s.filter_chain", zoneID)); err != nil {
				return err
			}
		}
		if zone.WeigherChain != nil {
			if len(*zone.WeigherChain) == 0 {
				return fmt.Errorf("placement.zone.%s.weigher_chain must not be empty when set", zoneID)
			}
			if err := validateWeigherChain(*zone.WeigherChain, fmt.Sprintf("placement.zone.%s.weigher_chain", zoneID)); err != nil {
				return err
			}
		}
		if zone.FallbackFilter != nil && *zone.FallbackFilter != "" {
			if err := validateFilterName(*zone.FallbackFilter, fmt.Sprintf("placement.zone.%s.fallback_filter", zoneID)); err != nil {
				return err
			}
		}
		if zone.Filters != nil && zone.Filters.Hugepage != nil && zone.Filters.Hugepage.PageSizeKB != nil {
			if *zone.Filters.Hugepage.PageSizeKB <= 0 {
				return fmt.Errorf("placement.zone.%s.filters.hugepage.page_size_kb must be > 0", zoneID)
			}
		}
	}
	return nil
}

func validateFilterName(name, field string) error {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if _, ok := filterRegistry[name]; !ok {
		return fmt.Errorf("%s contains unknown filter %q", field, name)
	}
	return nil
}

func validateFilterChain(chain []string, field string) error {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, name := range chain {
		if _, ok := filterRegistry[name]; !ok {
			return fmt.Errorf("%s contains unknown filter %q", field, name)
		}
	}
	return nil
}

func validateWeigherChain(chain []string, field string) error {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, name := range chain {
		if _, ok := weigherRegistry[name]; !ok {
			return fmt.Errorf("%s contains unknown weigher %q", field, name)
		}
	}
	return nil
}

// GetSnapshot returns the current active config snapshot (atomic, lock-free).
func GetSnapshot() *configSnapshot {
	return activeSnapshot.Load()
}

// GetCurrentConfig returns the current config and its load time for display.
func GetCurrentConfig() (*PlacementConfig, time.Time) {
	snap := activeSnapshot.Load()
	if snap == nil {
		return defaultConfig(), time.Time{}
	}
	return snap.cfg, snap.loadedAt
}

// ResolveZoneConfig returns the effective PlacementConfig for the given zone.
// Lookup priority: per-zone config → global config (field-level fallback, not whole replacement).
// The returned *PlacementConfig is a merged copy safe to use directly by the caller.
func ResolveZoneConfig(zoneID int64) *PlacementConfig {
	snap := activeSnapshot.Load()
	if snap == nil {
		logger.Warning("ResolveZoneConfig: no active snapshot, returning default config")
		return defaultConfig()
	}
	global := snap.cfg
	if zoneID <= 0 {
		return global
	}
	zoneKey := strconv.FormatInt(zoneID, 10)
	zoneCfg, ok := global.Zones[zoneKey]
	if !ok || zoneCfg == nil {
		// No per-zone config, use global
		logger.Debugf("ResolveZoneConfig: no override for zone ID %d, using global config", zoneID)
		return global
	}
	logger.Debugf("ResolveZoneConfig: merging per-zone override for zone ID %d", zoneID)
	return mergeZoneConfig(global, zoneCfg)
}

// mergeZoneConfig merges per-zone overrides onto a copy of the global config.
// Only fields explicitly set (non-nil) in zone are overridden; others inherit from global.
func mergeZoneConfig(global *PlacementConfig, zone *ZonePlacementConfig) *PlacementConfig {
	// Shallow copy global as the base
	merged := *global
	// Merged config does not carry the zones table (prevents recursive resolution)
	merged.Zones = nil

	if zone.FilterChain != nil {
		merged.FilterChain = *zone.FilterChain
		logger.Debugf("mergeZoneConfig: override filter_chain=%v", merged.FilterChain)
	}
	if zone.WeigherChain != nil {
		merged.WeigherChain = *zone.WeigherChain
		logger.Debugf("mergeZoneConfig: override weigher_chain=%v", merged.WeigherChain)
	}
	if zone.FallbackFilter != nil {
		merged.FallbackFilter = *zone.FallbackFilter
		logger.Debugf("mergeZoneConfig: override fallback_filter=%q", merged.FallbackFilter)
	}
	if zone.Filters != nil {
		z := zone.Filters
		if z.Hugepage != nil {
			if z.Hugepage.PageSizeKB != nil {
				merged.Filters.Hugepage.PageSizeKB = *z.Hugepage.PageSizeKB
			}
		}
		if z.CPULoad != nil {
			if z.CPULoad.IdleThresholdPct != nil {
				merged.Filters.CPULoad.IdleThresholdPct = *z.CPULoad.IdleThresholdPct
				logger.Debugf("mergeZoneConfig: override cpu_load.idle_threshold_pct=%.1f", merged.Filters.CPULoad.IdleThresholdPct)
			}
		}
	}
	if zone.Overcommit != nil {
		z := zone.Overcommit
		if z.Enabled != nil {
			merged.Overcommit.Enabled = *z.Enabled
		}
		if z.MemDeltaRatioPct != nil {
			merged.Overcommit.MemDeltaRatioPct = *z.MemDeltaRatioPct
		}
		if z.VCPUDeltaRatioPct != nil {
			merged.Overcommit.VCPUDeltaRatioPct = *z.VCPUDeltaRatioPct
		}
		if z.CPUIdleFallbackPct != nil {
			merged.Overcommit.CPUIdleFallbackPct = *z.CPUIdleFallbackPct
		}
		if z.HugepageDeltaRatioPct != nil {
			merged.Overcommit.HugepageDeltaRatioPct = *z.HugepageDeltaRatioPct
		}
	}
	if zone.Weighers != nil {
		z := zone.Weighers
		if z.OvercommitPenaltyMultiplier != nil {
			merged.Weighers.OvercommitPenaltyMultiplier = *z.OvercommitPenaltyMultiplier
		}
		if z.HugepageMultiplier != nil {
			merged.Weighers.HugepageMultiplier = *z.HugepageMultiplier
		}
		if z.RAMMultiplier != nil {
			merged.Weighers.RAMMultiplier = *z.RAMMultiplier
		}
		if z.CPULoadMultiplier != nil {
			merged.Weighers.CPULoadMultiplier = *z.CPULoadMultiplier
		}
		if z.SpreadMultiplier != nil {
			merged.Weighers.SpreadMultiplier = *z.SpreadMultiplier
		}
	}
	return &merged
}
