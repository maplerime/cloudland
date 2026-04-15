/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"encoding/json"
	"time"

	"web/src/dbs"
	"web/src/model"
)

// DecisionLog records the full placement decision for one SelectHost call.
type DecisionLog struct {
	// Request context
	Timestamp time.Time         `json:"timestamp"`
	Request   *PlacementRequest `json:"request"`

	// Host loading
	TotalHosts int `json:"total_hosts"` // hosts loaded from DB

	// Filter chain results
	FilterSteps []FilterStep `json:"filter_steps"`

	// Fallback
	FallbackUsed   bool   `json:"fallback_used"`
	FallbackName   string `json:"fallback_name,omitempty"`
	FallbackResult int    `json:"fallback_result"` // candidates after fallback

	// Weigher scores (only for successful placement)
	WeigherSteps []WeigherStep `json:"weigher_steps,omitempty"`

	// Final result
	Success        bool   `json:"success"`
	SelectedHost   int32  `json:"selected_host"` // -1 if failed
	IsOvercommit   bool   `json:"is_overcommit"`
	RejectReason   string `json:"reject_reason,omitempty"` // structured rejection reason
	CandidateCount int    `json:"candidate_count"`

	// Timing
	DurationMs float64 `json:"duration_ms"`
}

// FilterStep records one filter's input/output in the chain.
type FilterStep struct {
	Name        string `json:"name"`
	InputCount  int    `json:"input_count"`
	OutputCount int    `json:"output_count"`
	Eliminated  int    `json:"eliminated"`
}

// WeigherStep records one weigher's scoring summary.
type WeigherStep struct {
	Name       string  `json:"name"`
	Multiplier float64 `json:"multiplier"`
	MinRaw     float64 `json:"min_raw"`
	MaxRaw     float64 `json:"max_raw"`
}

// recordDecision persists a decision log to the database.
func recordDecision(log *DecisionLog) {
	detailJSON, err := json.Marshal(log)
	if err != nil {
		logger.Errorf("recordDecision: failed to marshal decision log: %v", err)
		return
	}

	var zoneID int64
	var vcpus int32
	var memMB, diskGB int64
	if log.Request != nil {
		zoneID = log.Request.ZoneID
		vcpus = log.Request.VCPUs
		memMB = log.Request.MemMB
		diskGB = log.Request.DiskGB
	}

	record := &model.PlacementDecision{
		ZoneID:       zoneID,
		VCPUs:        vcpus,
		MemMB:        memMB,
		DiskGB:       diskGB,
		Success:      log.Success,
		SelectedHost: log.SelectedHost,
		IsOvercommit: log.IsOvercommit,
		RejectReason: log.RejectReason,
		Detail:       string(detailJSON),
		DurationMs:   log.DurationMs,
	}

	db := dbs.DB()
	if err := db.Create(record).Error; err != nil {
		logger.Errorf("recordDecision: failed to persist decision to DB: %v", err)
	}
}

// GetRecentDecisions returns the most recent N decision logs from the database (newest first).
func GetRecentDecisions(n int) []*DecisionLog {
	if n <= 0 {
		n = 20
	}
	if n > 100 {
		n = 100
	}

	db := dbs.DB()
	var records []model.PlacementDecision
	if err := db.Order("id desc").Limit(n).Find(&records).Error; err != nil {
		logger.Errorf("GetRecentDecisions: failed to query decisions: %v", err)
		return nil
	}

	result := make([]*DecisionLog, 0, len(records))
	for _, r := range records {
		dl := &DecisionLog{}
		if err := json.Unmarshal([]byte(r.Detail), dl); err != nil {
			logger.Errorf("GetRecentDecisions: failed to unmarshal decision %d: %v", r.ID, err)
			continue
		}
		result = append(result, dl)
	}
	return result
}
