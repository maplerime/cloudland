/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"sync"
	"time"
)

// DecisionLog records the full placement decision for one SelectHost call.
type DecisionLog struct {
	// Request context
	Timestamp time.Time        `json:"timestamp"`
	Request   *PlacementRequest `json:"request"`

	// Host loading
	TotalHosts int `json:"total_hosts"` // hosts loaded from DB

	// Filter chain results
	FilterSteps []FilterStep `json:"filter_steps"`

	// Fallback
	FallbackUsed    bool   `json:"fallback_used"`
	FallbackName    string `json:"fallback_name,omitempty"`
	FallbackResult  int    `json:"fallback_result"` // candidates after fallback

	// Weigher scores (only for successful placement)
	WeigherSteps []WeigherStep `json:"weigher_steps,omitempty"`

	// Final result
	Success      bool   `json:"success"`
	SelectedHost int32  `json:"selected_host"` // -1 if failed
	IsOvercommit bool   `json:"is_overcommit"`
	RejectReason string `json:"reject_reason,omitempty"` // structured rejection reason
	CandidateCount int  `json:"candidate_count"`

	// Timing
	DurationMs float64 `json:"duration_ms"`
}

// FilterStep records one filter's input/output in the chain.
type FilterStep struct {
	Name       string `json:"name"`
	InputCount int    `json:"input_count"`
	OutputCount int   `json:"output_count"`
	Eliminated int    `json:"eliminated"`
}

// WeigherStep records one weigher's scoring summary.
type WeigherStep struct {
	Name       string  `json:"name"`
	Multiplier float64 `json:"multiplier"`
	MinRaw     float64 `json:"min_raw"`
	MaxRaw     float64 `json:"max_raw"`
}

// decisionRingBuffer is a fixed-size ring buffer storing recent decision logs.
type decisionRingBuffer struct {
	mu      sync.RWMutex
	entries []*DecisionLog
	size    int
	pos     int // next write position
	count   int // total written (for knowing if buffer is full)
}

const defaultDecisionBufferSize = 100

var decisionBuffer = &decisionRingBuffer{
	entries: make([]*DecisionLog, defaultDecisionBufferSize),
	size:    defaultDecisionBufferSize,
}

// recordDecision appends a decision log to the ring buffer.
func recordDecision(log *DecisionLog) {
	decisionBuffer.mu.Lock()
	defer decisionBuffer.mu.Unlock()
	decisionBuffer.entries[decisionBuffer.pos] = log
	decisionBuffer.pos = (decisionBuffer.pos + 1) % decisionBuffer.size
	decisionBuffer.count++
}

// GetRecentDecisions returns the most recent N decision logs (newest first).
func GetRecentDecisions(n int) []*DecisionLog {
	decisionBuffer.mu.RLock()
	defer decisionBuffer.mu.RUnlock()

	total := decisionBuffer.count
	if total > decisionBuffer.size {
		total = decisionBuffer.size
	}
	if n > total {
		n = total
	}
	if n <= 0 {
		return nil
	}

	result := make([]*DecisionLog, 0, n)
	// Read backwards from the most recent entry
	pos := (decisionBuffer.pos - 1 + decisionBuffer.size) % decisionBuffer.size
	for i := 0; i < n; i++ {
		if decisionBuffer.entries[pos] != nil {
			result = append(result, decisionBuffer.entries[pos])
		}
		pos = (pos - 1 + decisionBuffer.size) % decisionBuffer.size
	}
	return result
}
