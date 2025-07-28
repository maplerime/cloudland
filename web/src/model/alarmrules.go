package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log"
	"web/src/dbs"

	_ "github.com/lib/pq"
)

func init() {
	fmt.Printf("alarm rules dbstep1\n")
	dbs.AutoMigrate(
		&RuleGroupV2{},
		&CPURuleDetail{},
		&BWRuleDetail{},
		&VMRuleLink{},
		&NodeAlarmRule{},
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
	Email      string `gorm:"type:varchar(255);default:''"`
	Action     bool   `gorm:"default:false"`
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

type NodeAlarmRule struct {
	Model
	RuleType    string        `gorm:"type:varchar(32);index;not null" json:"rule_type"`
	Name        string        `gorm:"type:varchar(64);index;not null" json:"name"`
	Config      ConfigWrapper `gorm:"type:text;not null" json:"config" gorm:"column:config"`
	Description string        `gorm:"type:varchar(255)" json:"description"`
	Enabled     bool          `gorm:"default:true" json:"enabled"`
	Owner       string        `gorm:"column:owner;type:varchar(64);index;not null" json:"owner"`
}

type ConfigWrapper struct {
	json.RawMessage
}

func (c *ConfigWrapper) Scan(value interface{}) error {
	log.Printf("Scanning config with value: %v (type: %T)", value, value)
	if value == nil {
		c.RawMessage = nil
		return nil
	}
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("failed to scan config: expected string, got %T", value)
	}
	c.RawMessage = json.RawMessage([]byte(str))
	return nil
}

func (c ConfigWrapper) Value() (driver.Value, error) {
	if len(c.RawMessage) == 0 {
		return nil, nil
	}
	return string(c.RawMessage), nil
}
