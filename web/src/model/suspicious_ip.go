package model

import (
	"time"
	"web/src/dbs"
)

const (
	SuspiciousIPPending     = "suspicious"
	SuspiciousIPBlocked     = "blocked"
	SuspiciousIPBlockFailed = "block_failed"
)

type SuspiciousIP struct {
	Model
	IP           string     `gorm:"type:varchar(45);not null;unique_index" json:"ip"`
	Status       string     `gorm:"type:varchar(24);not null;index" json:"status"`
	Note         string     `gorm:"type:text" json:"note"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	LastSeenNode *int32     `json:"last_seen_node"`
	BlockedAt    *time.Time `json:"blocked_at"`
	BlockError   string     `gorm:"type:text" json:"block_error"`
}

func init() { dbs.AutoMigrate(&SuspiciousIP{}) }
