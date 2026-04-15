/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

// ZonePlacementConfig is a per-zone override config.
// All fields are pointers; nil means "not set, fall back to global value".
type ZonePlacementConfig struct {
	// Overrides global filter_chain if non-nil (whole list replaced, not appended)
	FilterChain *[]string `mapstructure:"filter_chain"`

	// Overrides global weigher_chain if non-nil
	WeigherChain *[]string `mapstructure:"weigher_chain"`

	// Overrides global fallback_filter if non-nil
	FallbackFilter *string `mapstructure:"fallback_filter"`

	// Overrides global filters section if non-nil
	Filters *struct {
		Hugepage *struct {
			PageSizeKB *int64 `mapstructure:"page_size_kb"`
		} `mapstructure:"hugepage"`

		CPULoad *struct {
			IdleThresholdPct *float64 `mapstructure:"idle_threshold_pct"`
		} `mapstructure:"cpu_load"`
	} `mapstructure:"filters"`

	// Overrides global overcommit section (field-level merge)
	Overcommit *struct {
		Enabled               *bool    `mapstructure:"enabled"`
		MemDeltaRatioPct      *float64 `mapstructure:"mem_delta_ratio_pct"`
		VCPUDeltaRatioPct     *float64 `mapstructure:"vcpu_delta_ratio_pct"`
		CPUIdleFallbackPct    *float64 `mapstructure:"cpu_idle_fallback_pct"`
		HugepageDeltaRatioPct *float64 `mapstructure:"hugepage_delta_ratio_pct"`
	} `mapstructure:"overcommit"`

	// Overrides global weighers section (field-level merge)
	Weighers *struct {
		OvercommitPenaltyMultiplier *float64 `mapstructure:"overcommit_penalty_multiplier"`
		HugepageMultiplier          *float64 `mapstructure:"hugepage_multiplier"`
		RAMMultiplier               *float64 `mapstructure:"ram_multiplier"`
		CPULoadMultiplier           *float64 `mapstructure:"cpu_load_multiplier"`
		SpreadMultiplier            *float64 `mapstructure:"spread_multiplier"`
	} `mapstructure:"weighers"`
}

// PlacementConfig holds all scheduler configuration, loaded from placement.toml.
type PlacementConfig struct {
	// Filter chain: executed in order, names correspond to RegisterFilter keys.
	// Zone filtering is performed at DB query level (loadHostStates WHERE zone_id=?),
	// so no "zone" filter is needed here.
	FilterChain []string `mapstructure:"filter_chain"`

	// Weigher chain: scored in order
	WeigherChain []string `mapstructure:"weigher_chain"`

	// Fallback filter name (e.g. "overcommit"), empty string disables
	FallbackFilter string `mapstructure:"fallback_filter"`

	// Hyper heartbeat report interval in seconds; alive threshold = 2 * this.
	// Global only — not overridable per zone (heartbeat period is node-wide).
	HostReportIntervalSec int `mapstructure:"host_report_interval_sec"`

	// Filter-specific parameters
	Filters struct {
		Hugepage struct {
			PageSizeKB int64 `mapstructure:"page_size_kb"`
		} `mapstructure:"hugepage"`

		CPULoad struct {
			IdleThresholdPct float64 `mapstructure:"idle_threshold_pct"`
		} `mapstructure:"cpu_load"`
	} `mapstructure:"filters"`

	// Overcommit fallback parameters
	Overcommit struct {
		Enabled               bool    `mapstructure:"enabled"`
		MemDeltaRatioPct      float64 `mapstructure:"mem_delta_ratio_pct"`
		VCPUDeltaRatioPct     float64 `mapstructure:"vcpu_delta_ratio_pct"`
		CPUIdleFallbackPct    float64 `mapstructure:"cpu_idle_fallback_pct"`
		HugepageDeltaRatioPct float64 `mapstructure:"hugepage_delta_ratio_pct"`
	} `mapstructure:"overcommit"`

	// Weigher multiplier parameters
	Weighers struct {
		OvercommitPenaltyMultiplier float64 `mapstructure:"overcommit_penalty_multiplier"`
		HugepageMultiplier          float64 `mapstructure:"hugepage_multiplier"`
		RAMMultiplier               float64 `mapstructure:"ram_multiplier"`
		CPULoadMultiplier           float64 `mapstructure:"cpu_load_multiplier"`
		SpreadMultiplier            float64 `mapstructure:"spread_multiplier"`
	} `mapstructure:"weighers"`

	// Per-zone override configs: key = zone ID string (e.g. "1").
	// Corresponds to [placement.zone.1] sections in placement.toml.
	// Use ResolveZoneConfig(zoneID) to obtain the effective merged config.
	Zones map[string]*ZonePlacementConfig `mapstructure:"zone"`
}

// PlacementRequest describes the resources needed by a VM.
type PlacementRequest struct {
	VCPUs          int32
	MemMB          int64 // memory in MB
	DiskGB         int64
	HugepageSizeKB int64    // 0 = no hugepage requirement
	ZoneID         int64    // used for DB-level zone filtering and config lookup
	Traits         []string // required hyper tags, e.g. ["gpu", "nvme"]
	OwnerID        int64    // owner org ID (for affinity/anti-affinity)
	Policy         string   // "affinity" | "anti-affinity" | ""
	ExcludeHypers  []int32  // hypers to exclude from candidates (e.g. migration source)
}

func defaultConfig() *PlacementConfig {
	cfg := &PlacementConfig{
		// "zone" filter removed: DB query already scopes hosts to the requested zone.
		FilterChain:           []string{"compute_alive", "hugepage", "resource", "cpu_load", "affinity", "capability"},
		WeigherChain:          []string{"overcommit_penalty", "hugepage", "ram", "cpu_load", "spread"},
		FallbackFilter:        "overcommit",
		HostReportIntervalSec: 60,
	}
	cfg.Filters.CPULoad.IdleThresholdPct = 15.0
	cfg.Filters.Hugepage.PageSizeKB = 2048
	cfg.Overcommit.Enabled = true
	cfg.Overcommit.MemDeltaRatioPct = 10.0
	cfg.Overcommit.VCPUDeltaRatioPct = 10.0
	cfg.Overcommit.CPUIdleFallbackPct = 5.0
	cfg.Overcommit.HugepageDeltaRatioPct = 5.0
	cfg.Weighers.OvercommitPenaltyMultiplier = 3.0
	cfg.Weighers.HugepageMultiplier = 1.5
	cfg.Weighers.RAMMultiplier = 1.0
	cfg.Weighers.CPULoadMultiplier = 1.0
	cfg.Weighers.SpreadMultiplier = -1.0
	return cfg
}

func hasFilter(chain []string, name string) bool {
	for _, n := range chain {
		if n == name {
			return true
		}
	}
	return false
}

// ResolveRequestHugepageSizeKB returns the effective hugepage size to be carried in
// PlacementRequest for the target zone. Returning 0 means no hugepage requirement.
func ResolveRequestHugepageSizeKB(zoneID int64) int64 {
	cfg := ResolveZoneConfig(zoneID)
	if cfg == nil || !hasFilter(cfg.FilterChain, "hugepage") {
		return 0
	}
	if cfg.Filters.Hugepage.PageSizeKB <= 0 {
		return 2048
	}
	return cfg.Filters.Hugepage.PageSizeKB
}
