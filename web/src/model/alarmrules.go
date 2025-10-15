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
		&MemoryRuleDetail{},
		&BWRuleDetail{},
		&VMRuleLink{},
		&NodeAlarmRule{},
		&RemoteNotifyConfig{},
	)

	logger.Debugf("Alert system tables migrated successfully")
}
func (RuleGroupV2) TableName() string {
	return "rule_group_v2"
}

type RuleGroupV2 struct {
	Model
	RuleID          string `gorm:"type:varchar(128);unique_index:idx_rule_id;column:rule_id"` // 用户指定的rule_id，用于API查询和删除
	Name            string `gorm:"type:varchar(128);unique_index:idx_rule_group_name;column:name"`
	Type            string `gorm:"type:varchar(32)"`
	Owner           string `gorm:"type:varchar(255);index"`
	Enabled         bool   `gorm:"default:true"`
	TriggerCnt      int    `gorm:"default:0"`
	RegionID        string `gorm:"type:varchar(64)"`
	Level           string `gorm:"type:varchar(32)"`
	DurationMinutes int
}

type CPURuleDetail struct {
	Model
	GroupUUID    string `gorm:"column:group_uuid;type:varchar(36);index;not null;references:rule_group_v2(uuid)"`
	Name         string `gorm:"type:varchar(128);column:name"`
	Limit        int    `gorm:"column:limit;check:limit >= 1"` // 阈值，对应输入的limit
	Rule         string `gorm:"type:varchar(8);column:rule"`   // 比较操作符: gt/lt
	Duration     int    `gorm:"check:duration >= 1"`           // 持续时间(分钟)，对应输入的duration
	Over         int    `gorm:"column:over;check:over >= 1"`
	DownDuration int    `gorm:"column:down_duration;check:down_duration >= 1"`
	DownTo       int    `gorm:"column:down_to;check:down_to <= 100"`
}

type MemoryRuleDetail struct {
	Model
	GroupUUID    string `gorm:"column:group_uuid;type:varchar(36);index;not null;references:rule_group_v2(uuid)"`
	Name         string `gorm:"type:varchar(128);column:name"`
	Limit        int    `gorm:"column:limit;check:limit >= 1"` // 阈值，对应输入的limit
	Rule         string `gorm:"type:varchar(8);column:rule"`   // 比较操作符: gt/lt
	Duration     int    `gorm:"check:duration >= 1"`           // 持续时间(分钟)，对应输入的duration
	Over         int    `gorm:"column:over;check:over >= 1"`
	DownDuration int    `gorm:"column:down_duration;check:down_duration >= 1"`
	DownTo       int    `gorm:"column:down_to;check:down_to <= 100"`
}

type BWRuleDetail struct {
	Model
	GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index;not null"`
	Name      string `gorm:"type:varchar(128)"`

	// New single-direction fields for API v2
	Direction string `gorm:"type:varchar(8);check:direction IN ('in','out')"` // 方向: in/out
	Limit     int    `gorm:"check:limit >= 1"`                                // 阈值 (Mbps)
	Duration  int    `gorm:"check:duration >= 1"`                             // 持续时间(分钟)

	// Legacy dual-direction fields - kept for backward compatibility
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
	Config      ConfigWrapper `gorm:"type:text;not null;column:config" json:"config"`
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

// 远程配置类型常量
const (
	RemoteConfigTypeNotify  = "NOTIFY"  // 告警通知类型
	RemoteConfigTypeWebhook = "WEBHOOK" // Webhook回调类型
	RemoteConfigTypeSync    = "SYNC"    // 数据同步类型
	RemoteConfigTypeMetrics = "METRICS" // 指标上报类型
	RemoteConfigTypeLog     = "LOG"     // 日志转发类型
)

// RemoteNotifyConfig 远程通知配置
type RemoteNotifyConfig struct {
	Model
	Name      string `gorm:"type:varchar(128);unique_index:idx_remote_notify_name;column:name" json:"name"` // 服务名称
	Type      string `gorm:"type:varchar(50);not null;column:type;index" json:"type"`                       // 配置类型：NOTIFY, WEBHOOK, SYNC等
	NotifyURL string `gorm:"type:varchar(500);not null;column:notify_url" json:"notify_url"`                // 通知URL
	Username  string `gorm:"type:varchar(255);column:username" json:"username"`                             // 用户名（基础认证或Token认证都用这个）
	Password  string `gorm:"type:varchar(255);column:password" json:"password"`                             // 密码（基础认证或Token认证都用这个）
	TokenURL  string `gorm:"type:varchar(500);column:token_url" json:"token_url"`                           // Token获取URL（为空=基础认证，不为空=Token认证）
}

func (RemoteNotifyConfig) TableName() string {
	return "remote_notify_config"
}
