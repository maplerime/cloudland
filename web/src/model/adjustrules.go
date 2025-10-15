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
	RegionID      string `gorm:"type:varchar(64);index"`
	RuleID        string `gorm:"type:varchar(128);unique_index:idx_adjust_rule_id;column:rule_id"`
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
	GroupUUID       string `gorm:"column:group_uuid;type:varchar(64);index;not null;references:adjust_rule_group(uuid)"` // 规则组UUID，关联adjust_rule_group表
	Name            string `gorm:"type:varchar(128)"`                                                                    // 规则名称
	Direction       string `gorm:"type:varchar(8);check:direction IN ('in','out')"`                                      // 带宽方向：'in'(入站)或'out'(出站)
	HighThreshold   int64  `gorm:"check:high_threshold > 0"`                                                             // 高阈值，触发限制的带宽使用率 (单位: kB/s)
	LowThreshold    int64  `gorm:"check:low_threshold > 0"`                                                              // 低阈值，恢复正常的带宽使用率 (单位: kB/s)
	SmoothWindow    int    `gorm:"default:5;check:smooth_window > 0"`                                                    // 平滑窗口，监控数据平滑处理的时间窗口 (单位: 分钟)
	TriggerDuration int    `gorm:"default:30;check:trigger_duration > 0"`                                                // 触发持续时间，超过阈值多长时间后触发调整 (单位: 秒)
	RestoreDuration int    `gorm:"default:300;check:restore_duration > 0"`                                               // 恢复持续时间，低于阈值多长时间后恢复正常 (单位: 秒)
	LimitValue      int    `gorm:"default:1024;check:limit_value > 0"`                                                   // 带宽限制值，触发调整时的目标带宽限制 (单位: kB/s，默认1MB/s)
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
