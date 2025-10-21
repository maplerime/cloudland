package model

import (
	"time"
	"web/src/dbs"

	_ "github.com/lib/pq"
)

const (
	RuleTypeAdjustCPU   = "adjust_cpu"
	RuleTypeAdjustInBW  = "adjust_in_bw"
	RuleTypeAdjustOutBW = "adjust_out_bw"
)

func init() {
	dbs.AutoMigrate(
		&AdjustRuleGroup{},
		&CPUAdjustRuleDetail{},
		&BWAdjustRuleDetail{},
		&AdjustmentHistory{},
	)
}

func (AdjustRuleGroup) TableName() string {
	return "adjust_rule_group"
}

// AdjustRuleGroup Resource auto-adjustment rule group
type AdjustRuleGroup struct {
	Model
	Name          string `gorm:"type:varchar(128)"`
	Type          string `gorm:"type:varchar(32)"`
	Owner         string `gorm:"type:varchar(128)"`
	Enabled       bool   `gorm:"default:true"`
	Email         string `gorm:"type:varchar(255)"`
	AdjustEnabled bool   `gorm:"default:true"`
	RegionID      string `gorm:"type:varchar(64);index"`
	RuleID        string `gorm:"type:varchar(128);unique_index:idx_adjust_rule_id;column:rule_id"`
}

// CPUAdjustRuleDetail CPU adjustment rule detail
type CPUAdjustRuleDetail struct {
	Model
	GroupUUID       string  `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"`
	Name            string  `gorm:"type:varchar(128)"`
	HighThreshold   float64 `gorm:"default:80;check:high_threshold > 0"`
	LowThreshold    float64 `gorm:"default:40;check:low_threshold > 0"`
	SmoothWindow    int     `gorm:"default:5;check:smooth_window > 0"`
	TriggerDuration int     `gorm:"default:30;check:trigger_duration > 0"`
	RestoreDuration int     `gorm:"default:300;check:restore_duration > 0"`
	LimitPercent    int     `gorm:"default:50;check:limit_percent > 0"`
}

// BWAdjustRuleDetail Bandwidth adjustment rule detail
type BWAdjustRuleDetail struct {
	Model
	GroupUUID       string `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"`
	Name            string `gorm:"type:varchar(128)"`
	Direction       string `gorm:"type:varchar(8);check:direction IN ('in','out')"`
	HighThreshold   int64  `gorm:"check:high_threshold > 0"`
	LowThreshold    int64  `gorm:"check:low_threshold > 0"`
	SmoothWindow    int    `gorm:"default:5;check:smooth_window > 0"`
	TriggerDuration int    `gorm:"default:30;check:trigger_duration > 0"`
	RestoreDuration int    `gorm:"default:300;check:restore_duration > 0"`
	LimitValue      int    `gorm:"default:1024;check:limit_value > 0"`
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
