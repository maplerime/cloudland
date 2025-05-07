package model

import (
	"fmt"
	"web/src/dbs" // 通过dbs包获取数据库实例

	_ "github.com/lib/pq"
)

func init() {
	fmt.Printf("alarm rules dbstep1\n")
	dbs.AutoMigrate(
		&RuleGroupV2{},
		&CPURuleDetail{},
		&BWRuleDetail{},
		&VMRuleLink{},
	)
	logger.Debugf("Alert system tables migrated successfully")
}
func (RuleGroupV2) TableName() string {
	return "rule_group_v2"
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
	GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index;not null"`
	Name      string `gorm:"type:varchar(128)"`

	// Inbound parameters - negative values indicate disabled rules
	InThreshold    int    `gorm:"default:-1"`
	InDuration     int    `gorm:"default:-1"`
	InOverType     string `gorm:"type:varchar(20);default:'percent';check:in_over_type IN ('percent','absolute')"`
	InDownTo       int    `gorm:"default:-1"`
	InDownDuration int    `gorm:"default:-1"`

	// Outbound parameters - negative values indicate disabled rules
	OutThreshold    int    `gorm:"default:-1"`
	OutDuration     int    `gorm:"default:-1"`
	OutOverType     string `gorm:"type:varchar(20);default:'percent';check:out_over_type IN ('percent','absolute')"`
	OutDownTo       int    `gorm:"default:-1"`
	OutDownDuration int    `gorm:"default:-1"`
}

type VMRuleLink struct {
	Model
	GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index;not null;references:rule_group_v2(uuid)"`
	VMUUID    string `gorm:"column:vm_uuid;type:varchar(36);index"`
	Interface string `gorm:"type:varchar(32)"`
}
