/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"web/src/dbs"
)

// PlacementDecision persists a placement scheduling decision so that both
// clbase and clapi can share the same decision history.
type PlacementDecision struct {
	ID        int64     `gorm:"primary_key" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	// Request context
	ZoneID int64 `json:"zone_id"`
	VCPUs  int32 `json:"vcpus"`
	MemMB  int64 `json:"mem_mb"`
	DiskGB int64 `json:"disk_gb"`

	// Result
	Success      bool   `json:"success"`
	SelectedHost int32  `json:"selected_host"`
	IsOvercommit bool   `json:"is_overcommit"`
	RejectReason string `gorm:"type:text" json:"reject_reason,omitempty"`

	// Detail (full DecisionLog JSON for drill-down)
	Detail string `gorm:"type:text" json:"detail"`

	// Timing
	DurationMs float64 `json:"duration_ms"`
}

func init() {
	dbs.AutoMigrate(&PlacementDecision{})
}
