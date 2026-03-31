/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

// PlacementConfig holds all scheduler configuration, loaded from placement.toml.
type PlacementConfig struct {
	// Filter chain: executed in order, names correspond to RegisterFilter keys
	FilterChain []string `mapstructure:"filter_chain"`

	// Weigher chain: scored in order
	WeigherChain []string `mapstructure:"weigher_chain"`

	// Fallback filter name (e.g. "overcommit"), empty string disables
	FallbackFilter string `mapstructure:"fallback_filter"`

	// Hyper heartbeat report interval in seconds; alive threshold = 2 * this
	HostReportIntervalSec int `mapstructure:"host_report_interval_sec"`

	// Filter-specific parameters
	Filters struct {
		CPULoad struct {
			IdleThresholdPct float64 `mapstructure:"idle_threshold_pct"`
		} `mapstructure:"cpu_load"`
	} `mapstructure:"filters"`

	// Overcommit fallback parameters
	Overcommit struct {
		Enabled               bool    `mapstructure:"enabled"`
		MemDeltaRatioPct      float64 `mapstructure:"mem_delta_ratio_pct"`
		VCPUOvercommitRatio   float64 `mapstructure:"vcpu_overcommit_ratio"`
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
}

// PlacementRequest describes the resources needed by a VM.
type PlacementRequest struct {
	VCPUs          int32
	MemMB          int64 // memory in MB
	DiskGB         int64
	HugepageSizeKB int64 // 0 = no hugepage requirement
	ZoneID         int64
}

func defaultConfig() *PlacementConfig {
	cfg := &PlacementConfig{
		FilterChain:           []string{"compute_alive", "zone", "hugepage", "resource", "cpu_load"},
		WeigherChain:          []string{"hugepage", "ram", "cpu_load"},
		FallbackFilter:        "",
		HostReportIntervalSec: 60,
	}
	cfg.Filters.CPULoad.IdleThresholdPct = 15.0
	cfg.Overcommit.Enabled = false
	cfg.Overcommit.MemDeltaRatioPct = 10.0
	cfg.Overcommit.VCPUOvercommitRatio = 1.5
	cfg.Overcommit.CPUIdleFallbackPct = 5.0
	cfg.Overcommit.HugepageDeltaRatioPct = 5.0
	cfg.Weighers.OvercommitPenaltyMultiplier = 3.0
	cfg.Weighers.HugepageMultiplier = 1.5
	cfg.Weighers.RAMMultiplier = 1.0
	cfg.Weighers.CPULoadMultiplier = 1.0
	cfg.Weighers.SpreadMultiplier = 1.0
	return cfg
}
