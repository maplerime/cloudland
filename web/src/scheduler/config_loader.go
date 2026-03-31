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
	configFilePath = path
	configViper = viper.New()
	configViper.SetConfigFile(path)
	configViper.SetConfigType("toml")

	if err := configViper.ReadInConfig(); err != nil {
		logger.Warningf("placement config not found at %s, using defaults", path)
		snapshot := buildSnapshot(defaultConfig(), path)
		activeSnapshot.Store(snapshot)
		return nil
	}

	return doReload()
}

// ReloadConfig re-reads the config file and atomically replaces the active snapshot.
// Called by the admin API handler.
func ReloadConfig() (*ReloadResult, error) {
	if configViper == nil {
		return nil, fmt.Errorf("placement config not initialized")
	}
	if err := configViper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configFilePath, err)
	}
	if err := doReload(); err != nil {
		return nil, err
	}
	snap := GetSnapshot()
	return &ReloadResult{
		LoadedAt:     snap.loadedAt,
		ConfigPath:   snap.configPath,
		FilterChain:  snap.cfg.FilterChain,
		WeigherChain: snap.cfg.WeigherChain,
	}, nil
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
	if err := configViper.UnmarshalKey("placement", cfg); err != nil {
		return fmt.Errorf("config unmarshal failed: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	snapshot := buildSnapshot(cfg, configFilePath)
	activeSnapshot.Store(snapshot)
	logger.Info("placement config loaded successfully")
	return nil
}

func buildSnapshot(cfg *PlacementConfig, path string) *configSnapshot {
	snap := &configSnapshot{
		cfg:        cfg,
		loadedAt:   time.Now(),
		configPath: path,
	}
	snap.filters = BuildFilters(cfg)
	snap.weighers = BuildWeighers(cfg)
	if cfg.FallbackFilter != "" {
		registryMu.RLock()
		if factory, ok := filterRegistry[cfg.FallbackFilter]; ok {
			snap.fallback = factory(cfg)
		}
		registryMu.RUnlock()
	}
	return snap
}

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
