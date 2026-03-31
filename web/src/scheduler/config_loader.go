/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"
)

// configSnapshot is an immutable snapshot: config + pre-built Filter/Weigher chains.
type configSnapshot struct {
	cfg        *PlacementConfig
	filters    []Filter
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
	logger.Infof("ReloadConfig completed: filters=%v, weighers=%v", result.FilterChain, result.WeigherChain)
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
	logger.Infof("Placement config loaded: filters=%v, weighers=%v, fallback=%q",
		cfg.FilterChain, cfg.WeigherChain, cfg.FallbackFilter)
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
	logger.Debugf("Config snapshot built: %d filters, %d weighers, fallback=%v",
		len(snap.filters), len(snap.weighers), snap.fallback != nil)
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
	if cfg.Overcommit.Enabled {
		if cfg.Overcommit.MemDeltaRatioPct < 0 || cfg.Overcommit.MemDeltaRatioPct > 100 {
			return fmt.Errorf("overcommit.mem_delta_ratio_pct must be in [0, 100]")
		}
		if cfg.Overcommit.VCPUDeltaRatioPct < 0 || cfg.Overcommit.VCPUDeltaRatioPct > 100 {
			return fmt.Errorf("overcommit.vcpu_delta_ratio_pct must be in [0, 100]")
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
