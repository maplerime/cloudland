package model

import (
	"fmt"
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
	fmt.Printf("adjust rules dbstep1\n")
	dbs.AutoMigrate(
		&AdjustRuleGroup{},
		&CPUAdjustRuleDetail{},
		&BWAdjustRuleDetail{},
		&AdjustmentHistory{},
	)
	fmt.Printf("Resource adjustment system tables migrated successfully\n")
}

func (AdjustRuleGroup) TableName() string {
	return "adjust_rule_group"
}

// AdjustRuleGroup 资源自动调整规则组
type AdjustRuleGroup struct {
	Model
	Name          string `gorm:"type:varchar(128)"`
	Type          string `gorm:"type:varchar(32)"`
	Owner         string `gorm:"type:varchar(128)"`
	Enabled       bool   `gorm:"default:true"`
	Email         string `gorm:"type:varchar(255)"`
	AdjustEnabled bool   `gorm:"default:true"`
}

// CPUAdjustRuleDetail CPU调整规则详情
type CPUAdjustRuleDetail struct {
	Model
	GroupUUID       string  `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"`
	Name            string  `gorm:"type:varchar(128)"`
	HighThreshold   float64 `gorm:"default:80;check:high_threshold > 0"`
	LowThreshold    float64 `gorm:"default:40;check:low_threshold > 0"`
	SmoothWindow    int     `gorm:"default:5;check:smooth_window > 0"`
	TriggerDuration int     `gorm:"default:30;check:trigger_duration > 0"`
	RestoreDuration int     `gorm:"default:300;check:restore_duration > 0"`
	LimitPercent    int     `gorm:"default:50;check:limit_percent > 0"` // CPU限制百分比
}

// BWAdjustRuleDetail 带宽调整规则详情
type BWAdjustRuleDetail struct {
	Model
	GroupUUID        string `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"`
	Name             string `gorm:"type:varchar(128)"`
	InHighThreshold  int64  `gorm:"default:10485760"` // 10MB/s
	InLowThreshold   int64  `gorm:"default:5242880"`  // 5MB/s
	OutHighThreshold int64  `gorm:"default:10485760"` // 10MB/s
	OutLowThreshold  int64  `gorm:"default:5242880"`  // 5MB/s
	SmoothWindow     int    `gorm:"default:5;check:smooth_window > 0"`
	TriggerDuration  int    `gorm:"default:30;check:trigger_duration > 0"`
	RestoreDuration  int    `gorm:"default:300;check:restore_duration > 0"`
	LimitValue       int    `gorm:"default:1048576"` // 带宽限制值，默认1MB/s
}

// AdjustmentHistory 调整历史记录
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
