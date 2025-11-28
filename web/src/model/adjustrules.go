package model

import (
	"log"
	"time"
	"web/src/dbs"

	"github.com/jinzhu/gorm"
	_ "github.com/lib/pq"
)

const (
	RuleTypeAdjustCPU   = "adjust_cpu"
	RuleTypeAdjustInBW  = "adjust_in_bw"
	RuleTypeAdjustOutBW = "adjust_out_bw"
)

func init() {
	// 1. Migrate schema fields (AdjustRuleGroup.RuleID no longer uses unique_index tag)
	dbs.AutoMigrate(
		&AdjustRuleGroup{},
		&CPUAdjustRuleDetail{},
		&BWAdjustRuleDetail{},
		&AdjustmentHistory{},
	)

	// 2. Create partial unique index for AdjustRuleGroup.RuleID (supports soft delete scenario)
	dbs.AutoUpgrade("create_adjust_rule_group_rule_id_partial_unique_index", func(db *gorm.DB) error {
		// 2.1 Clean up legacy global unique indexes that may have been created by unique_index tag
		_ = db.Exec(`DROP INDEX IF EXISTS idx_adjust_rule_id`).Error
		_ = db.Exec(`DROP INDEX IF EXISTS uix_adjust_rule_id`).Error
		_ = db.Exec(`DROP INDEX IF EXISTS idx_adjust_rule_group_rule_id`).Error
		_ = db.Exec(`DROP INDEX IF EXISTS uix_adjust_rule_group_rule_id`).Error

		// 2.2 Create partial unique index for "only active records" (non-soft-deleted)
		err := db.Exec(`
			CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_adjust_rule_id_active
			ON adjust_rule_group (rule_id)
			WHERE deleted_at IS NULL
		`).Error

		if err != nil {
			log.Printf("CONCURRENTLY create index failed: %v, fallback to non-concurrent mode", err)
			err = db.Exec(`
				CREATE UNIQUE INDEX IF NOT EXISTS idx_adjust_rule_id_active
				ON adjust_rule_group (rule_id)
				WHERE deleted_at IS NULL
			`).Error
			if err != nil {
				log.Printf("Failed to create partial unique index for adjust_rule_group.rule_id: %v", err)
				return err
			}
		}

		log.Printf("Successfully created partial unique index idx_adjust_rule_id_active")
		return nil
	})
}

func (AdjustRuleGroup) TableName() string {
	return "adjust_rule_group"
}

// AdjustRuleGroup Resource auto-adjustment rule group
type AdjustRuleGroup struct {
	Model
	Name      string `gorm:"type:varchar(128)"`
	Type      string `gorm:"type:varchar(32)"`
	Owner     string `gorm:"type:varchar(128)"`
	Enabled   bool   `gorm:"default:true"`
	RegionID  string `gorm:"type:varchar(64);index"`
	RuleID    string `gorm:"type:varchar(128);unique_index:idx_adjust_rule_id;column:rule_id"`
	NotifyURL string `gorm:"type:varchar(500);not null"`
}

// CPUAdjustRuleDetail CPU adjustment rule detail
type CPUAdjustRuleDetail struct {
	Model
	GroupUUID       string  `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"`
	Name            string  `gorm:"type:varchar(128)"`
	HighThreshold   float64 `gorm:"default:80;check:high_threshold > 0"`
	SmoothWindow    int     `gorm:"default:5;check:smooth_window > 0"`
	TriggerDuration int     `gorm:"default:30;check:trigger_duration > 0"`
	LimitDuration   int     `gorm:"default:300;check:limit_duration > 0"`
	LimitPercent    int     `gorm:"default:50;check:limit_percent > 0"`
}

// BWAdjustRuleDetail Bandwidth adjustment rule detail
type BWAdjustRuleDetail struct {
	Model
	GroupUUID        string `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"`
	Name             string `gorm:"type:varchar(128)"`
	Direction        string `gorm:"type:varchar(8);check:direction IN ('in','out')"`
	HighThresholdPct int    `gorm:"check:high_threshold_pct >= 1 AND high_threshold_pct <= 100"`
	SmoothWindow     int    `gorm:"default:5;check:smooth_window > 0"`
	TriggerDuration  int    `gorm:"default:30;check:trigger_duration > 0"`
	LimitDuration    int    `gorm:"default:300;check:limit_duration > 0"`
	LimitValuePct    int    `gorm:"check:limit_value_pct >= 1 AND limit_value_pct <= 100"`
}

// AdjustmentHistory Adjustment history record
type AdjustmentHistory struct {
	Model
	DomainName string    `gorm:"type:varchar(128);index"`
	RuleID     string    `gorm:"type:varchar(128);index"`
	GroupUUID  string    `gorm:"type:varchar(64);index"`
	ActionType string    `gorm:"type:varchar(32)"`
	Status     string    `gorm:"type:varchar(32)"`
	Details    string    `gorm:"type:text"`
	AdjustTime time.Time `gorm:"index"`
}
