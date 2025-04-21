package model

import (
	"fmt"
	"web/src/dbs" // 通过dbs包获取数据库实例
)

func init() {
	fmt.Printf("alarm rules dbstep1")
	dbs.AutoMigrate(
        &RuleGroupV2{},
        &CPURuleDetail{},
        &BWRuleDetail{},
        &VMRuleLink{},
    )
	logger.Debugf("Alert system tables migrated successfully")
}
func (RuleGroupV2) TableName() string {
	return "rule_group_v2" // 明确指定表名
}

type RuleGroupV2 struct {
	Model
	Name       string `gorm:"type:varchar(128);uniqueIndex;column:name"`
	Type       string `gorm:"type:varchar(32)"`
	Owner      string `gorm:"type:varchar(255);index"`
	Enabled    bool   `gorm:"default:true"`
	TriggerCnt int    `gorm:"default:0"`
}

type CPURuleDetail struct {
	Model
	GroupUUID    string `gorm:"column:group_uuid;type:varchar(36);index;not null;references:rule_group_v2(uuid)"`
	Name         string `gorm:"type:varchar(128);column:name"`
	Over         int    `gorm:"column:over;check:over >= 1"`
	Duration     int    `gorm:"check:duration >= 1"`
	DownDuration int    `gorm:"column:down_duration;check:down_duration >= 1"`
	DownTo       int    `gorm:"column:down_to;check:down_to <= 100"`
}

type BWRuleDetail struct {
	Model
    GroupUUID   string `gorm:"column:group_uuid;type:varchar(36);index;not null"`
	Name         string `gorm:"type:varchar(128)"`
	InThreshold  int    `gorm:"check:in_threshold >= 1"`
	InDuration   int    `gorm:"check:in_duration >= 1"`
	OutThreshold int    `gorm:"check:out_threshold >= 1"`
	OutDuration  int    `gorm:"check:out_duration >= 1"`
}

type VMRuleLink struct {
	Model
	GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index;not null;references:rule_group_v2(uuid)"`
	VMUUID    string `gorm:"column:vm_uuid;type:varchar(36);index"`
	Interface string `gorm:"type:varchar(32)"`
}
