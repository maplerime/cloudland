/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"testing"
)

// ptr helpers to create pointer values inline
func ptrString(s string) *string       { return &s }
func ptrBool(b bool) *bool             { return &b }
func ptrFloat(f float64) *float64      { return &f }
func ptrStrSlice(s []string) *[]string { return &s }

// makeTestSnapshot builds and stores a test config snapshot with per-zone overrides.
func makeTestSnapshot(zones map[string]*ZonePlacementConfig) *PlacementConfig {
	cfg := defaultConfig()
	cfg.Zones = zones
	snap := buildSnapshot(cfg, "test")
	activeSnapshot.Store(snap)
	return cfg
}

// ---------------------------------------------------------------------------
// ResolveZoneConfig tests
// ---------------------------------------------------------------------------

func TestResolveZoneConfig_NoSnapshot(t *testing.T) {
	// Reset snapshot
	activeSnapshot.Store(nil)

	cfg := ResolveZoneConfig(1)
	if cfg == nil {
		t.Fatal("expected non-nil config when no snapshot (should return defaultConfig)")
	}
	if len(cfg.FilterChain) == 0 {
		t.Error("expected non-empty FilterChain in default config")
	}
}

func TestResolveZoneConfig_ZeroZoneID(t *testing.T) {
	global := makeTestSnapshot(map[string]*ZonePlacementConfig{
		"1": {FilterChain: ptrStrSlice([]string{"compute_alive"})},
	})

	cfg := ResolveZoneConfig(0)
	if cfg != global {
		t.Error("expected global config returned for zero zone ID")
	}
}

func TestResolveZoneConfig_UnknownZoneID(t *testing.T) {
	global := makeTestSnapshot(map[string]*ZonePlacementConfig{
		"1": {FilterChain: ptrStrSlice([]string{"compute_alive"})},
	})

	cfg := ResolveZoneConfig(999)
	if cfg != global {
		t.Error("expected global config returned for unknown zone ID")
	}
}

func TestResolveZoneConfig_KnownZone_FilterChainOverride(t *testing.T) {
	makeTestSnapshot(map[string]*ZonePlacementConfig{
		"1": {
			FilterChain: ptrStrSlice([]string{"compute_alive", "hugepage", "resource"}),
		},
	})

	cfg := ResolveZoneConfig(1)
	if cfg == nil {
		t.Fatal("expected non-nil config for zone override")
	}
	if len(cfg.FilterChain) != 3 {
		t.Errorf("expected 3 filters for zone override, got %d: %v", len(cfg.FilterChain), cfg.FilterChain)
	}
	if cfg.FilterChain[2] != "resource" {
		t.Errorf("expected last filter to be 'resource', got %q", cfg.FilterChain[2])
	}
	// Zones map should be stripped from merged config
	if cfg.Zones != nil {
		t.Error("merged config should not carry Zones map")
	}
}

func TestResolveZoneConfig_GlobalFieldsInherited(t *testing.T) {
	makeTestSnapshot(map[string]*ZonePlacementConfig{
		"2": {
			// Only override overcommit.vcpu_delta_ratio_pct
			Overcommit: &struct {
				Enabled            *bool    `mapstructure:"enabled"`
				VCPUDeltaRatioPct  *float64 `mapstructure:"vcpu_delta_ratio_pct"`
				CPUIdleFallbackPct *float64 `mapstructure:"cpu_idle_fallback_pct"`
			}{
				VCPUDeltaRatioPct: ptrFloat(20.0),
			},
		},
	})

	cfg := ResolveZoneConfig(2)
	// VCPUDeltaRatioPct overridden
	if cfg.Overcommit.VCPUDeltaRatioPct != 20.0 {
		t.Errorf("expected VCPUDeltaRatioPct=20.0, got %.1f", cfg.Overcommit.VCPUDeltaRatioPct)
	}
	// CPUIdleFallbackPct inherited from global default (5.0)
	if cfg.Overcommit.CPUIdleFallbackPct != 5.0 {
		t.Errorf("expected CPUIdleFallbackPct=5.0 (inherited), got %.1f", cfg.Overcommit.CPUIdleFallbackPct)
	}
	// FilterChain inherited from global default
	defaultFC := defaultConfig().FilterChain
	if len(cfg.FilterChain) != len(defaultFC) {
		t.Errorf("expected FilterChain to be inherited (len=%d), got len=%d", len(defaultFC), len(cfg.FilterChain))
	}
}

// ---------------------------------------------------------------------------
// mergeZoneConfig tests
// ---------------------------------------------------------------------------

func TestMergeZoneConfig_NilZone(t *testing.T) {
	global := defaultConfig()
	// Passing an empty ZonePlacementConfig should return global values unchanged
	merged := mergeZoneConfig(global, &ZonePlacementConfig{})
	if merged.FallbackFilter != global.FallbackFilter {
		t.Errorf("expected FallbackFilter=%q, got %q", global.FallbackFilter, merged.FallbackFilter)
	}
}

func TestMergeZoneConfig_FallbackFilterOverride(t *testing.T) {
	global := defaultConfig()
	zone := &ZonePlacementConfig{
		FallbackFilter: ptrString(""),
	}
	merged := mergeZoneConfig(global, zone)
	if merged.FallbackFilter != "" {
		t.Errorf("expected FallbackFilter disabled (empty), got %q", merged.FallbackFilter)
	}
}

func TestMergeZoneConfig_OvercommitDisabled(t *testing.T) {
	global := defaultConfig()
	zone := &ZonePlacementConfig{
		Overcommit: &struct {
			Enabled            *bool    `mapstructure:"enabled"`
			VCPUDeltaRatioPct  *float64 `mapstructure:"vcpu_delta_ratio_pct"`
			CPUIdleFallbackPct *float64 `mapstructure:"cpu_idle_fallback_pct"`
		}{
			Enabled: ptrBool(false),
		},
	}
	merged := mergeZoneConfig(global, zone)
	if merged.Overcommit.Enabled {
		t.Error("expected Overcommit.Enabled=false for zone override")
	}
	// VCPUDeltaRatioPct should still be inherited from global
	if merged.Overcommit.VCPUDeltaRatioPct != global.Overcommit.VCPUDeltaRatioPct {
		t.Errorf("expected VCPUDeltaRatioPct inherited from global (%.1f), got %.1f",
			global.Overcommit.VCPUDeltaRatioPct, merged.Overcommit.VCPUDeltaRatioPct)
	}
}

func TestMergeZoneConfig_WeigherMultiplierOverride(t *testing.T) {
	global := defaultConfig()
	zone := &ZonePlacementConfig{
		Weighers: &struct {
			OvercommitPenaltyMultiplier *float64 `mapstructure:"overcommit_penalty_multiplier"`
			HugepageMultiplier          *float64 `mapstructure:"hugepage_multiplier"`
			RAMMultiplier               *float64 `mapstructure:"ram_multiplier"`
			CPULoadMultiplier           *float64 `mapstructure:"cpu_load_multiplier"`
			SpreadMultiplier            *float64 `mapstructure:"spread_multiplier"`
		}{
			SpreadMultiplier: ptrFloat(2.0), // positive = stack (edge zone)
		},
	}
	merged := mergeZoneConfig(global, zone)
	if merged.Weighers.SpreadMultiplier != 2.0 {
		t.Errorf("expected SpreadMultiplier=2.0, got %.1f", merged.Weighers.SpreadMultiplier)
	}
	// Other weighers inherited
	if merged.Weighers.RAMMultiplier != global.Weighers.RAMMultiplier {
		t.Errorf("expected RAMMultiplier inherited (%.1f), got %.1f",
			global.Weighers.RAMMultiplier, merged.Weighers.RAMMultiplier)
	}
}

func TestMergeZoneConfig_CPULoadThresholdOverride(t *testing.T) {
	global := defaultConfig()
	zone := &ZonePlacementConfig{
		Filters: &struct {
			Hugepage *struct {
				PageSizeKB *int64 `mapstructure:"page_size_kb"`
			} `mapstructure:"hugepage"`
			CPULoad *struct {
				IdleThresholdPct *float64 `mapstructure:"idle_threshold_pct"`
			} `mapstructure:"cpu_load"`
		}{
			CPULoad: &struct {
				IdleThresholdPct *float64 `mapstructure:"idle_threshold_pct"`
			}{
				IdleThresholdPct: ptrFloat(10.0),
			},
		},
	}
	merged := mergeZoneConfig(global, zone)
	if merged.Filters.CPULoad.IdleThresholdPct != 10.0 {
		t.Errorf("expected IdleThresholdPct=10.0, got %.1f", merged.Filters.CPULoad.IdleThresholdPct)
	}
}

func TestMergeZoneConfig_GlobalNotMutated(t *testing.T) {
	global := defaultConfig()
	originalFC := make([]string, len(global.FilterChain))
	copy(originalFC, global.FilterChain)

	zone := &ZonePlacementConfig{
		FilterChain: ptrStrSlice([]string{"compute_alive"}),
	}
	mergeZoneConfig(global, zone)

	// global must not be modified
	if len(global.FilterChain) != len(originalFC) {
		t.Errorf("global FilterChain mutated after merge: expected len=%d, got len=%d",
			len(originalFC), len(global.FilterChain))
	}
}

// ---------------------------------------------------------------------------
// defaultConfig sanity check (regression: "zone" must not be in FilterChain)
// ---------------------------------------------------------------------------

func TestDefaultConfig_NoZoneFilter(t *testing.T) {
	cfg := defaultConfig()
	for _, name := range cfg.FilterChain {
		if name == "zone" {
			t.Errorf("defaultConfig FilterChain must not contain 'zone' (v2.1 design); got: %v", cfg.FilterChain)
		}
	}
}
