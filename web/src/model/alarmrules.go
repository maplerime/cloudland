package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log"
	"web/src/dbs"

	"github.com/jinzhu/gorm"
	_ "github.com/lib/pq"
)

func init() {
	// 1. Migrate schema fields (RemoteNotifyConfig.Name no longer uses unique_index tag)
	dbs.AutoMigrate(
		&RuleGroupV2{},
		&CPURuleDetail{},
		&MemoryRuleDetail{},
		&BWRuleDetail{},
		&VMRuleLink{},
		&NodeAlarmRule{},
		&RemoteNotifyConfig{},
	)

	// 2. Create partial unique index for RemoteNotifyConfig (supports soft delete scenario)
	dbs.AutoUpgrade("create_remote_notify_name_partial_unique_index", func(db *gorm.DB) error {
		// 2.1 Clean up legacy global unique indexes that may have been created by unique_index tag
		//     GORM v1 tag-generated index names are typically idx_<table>_<column> or uix_<table>_<column>
		_ = db.Exec(`DROP INDEX IF EXISTS idx_remote_notify_config_name`).Error
		_ = db.Exec(`DROP INDEX IF EXISTS uix_remote_notify_config_name`).Error
		_ = db.Exec(`DROP INDEX IF EXISTS idx_remote_notify_name`).Error

		// 2.2 Create partial unique index for "only active records" (non-soft-deleted)
		//     Try CONCURRENTLY first (recommended for production to avoid blocking writes)
		err := db.Exec(`
			CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_remote_notify_name_active
			ON remote_notify_config (name)
			WHERE deleted_at IS NULL
		`).Error

		if err != nil {
			// If CONCURRENTLY fails (possibly due to running in transaction or other reasons),
			// fallback to non-concurrent version (briefly blocks writes, but guarantees success)
			log.Printf("CONCURRENTLY create index failed: %v, fallback to non-concurrent mode", err)
			err = db.Exec(`
				CREATE UNIQUE INDEX IF NOT EXISTS idx_remote_notify_name_active
				ON remote_notify_config (name)
				WHERE deleted_at IS NULL
			`).Error
			if err != nil {
				log.Printf("Failed to create partial unique index for remote_notify_config: %v", err)
				return err
			}
		}

		log.Printf("Successfully created partial unique index idx_remote_notify_name_active")
		return nil
	})
}
func (RuleGroupV2) TableName() string {
	return "rule_group_v2"
}

type RuleGroupV2 struct {
	Model
	RuleID          string `gorm:"type:varchar(128);unique_index:idx_rule_id;column:rule_id"`
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
	Limit        int    `gorm:"column:limit;check:limit >= 1"`
	Rule         string `gorm:"type:varchar(8);column:rule"`
	Duration     int    `gorm:"check:duration >= 1"`
	Over         int    `gorm:"column:over;check:over >= 1"`
	DownDuration int    `gorm:"column:down_duration;check:down_duration >= 1"`
	DownTo       int    `gorm:"column:down_to;check:down_to <= 100"`
}

type MemoryRuleDetail struct {
	Model
	GroupUUID    string `gorm:"column:group_uuid;type:varchar(36);index;not null;references:rule_group_v2(uuid)"`
	Name         string `gorm:"type:varchar(128);column:name"`
	Limit        int    `gorm:"column:limit;check:limit >= 1"`
	Rule         string `gorm:"type:varchar(8);column:rule"`
	Duration     int    `gorm:"check:duration >= 1"`
	Over         int    `gorm:"column:over;check:over >= 1"`
	DownDuration int    `gorm:"column:down_duration;check:down_duration >= 1"`
	DownTo       int    `gorm:"column:down_to;check:down_to <= 100"`
}

type BWRuleDetail struct {
	Model
	GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index;not null"`
	Name      string `gorm:"type:varchar(128)"`

	// New single-direction fields for API v2
	Direction string `gorm:"type:varchar(8);check:direction IN ('in','out')"`
	Limit     int    `gorm:"check:limit >= 1"`
	Duration  int    `gorm:"check:duration >= 1"`

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

// Remote configuration type constants
const (
	RemoteConfigTypeNotify  = "NOTIFY"
	RemoteConfigTypeWebhook = "WEBHOOK"
	RemoteConfigTypeSync    = "SYNC"
	RemoteConfigTypeMetrics = "METRICS"
	RemoteConfigTypeLog     = "LOG"
)

// RemoteNotifyConfig Remote notification configuration
type RemoteNotifyConfig struct {
	Model
	Name      string `gorm:"type:varchar(128);column:name" json:"name"` // Uniqueness is enforced by partial unique index in database (idx_remote_notify_name_active)
	Type      string `gorm:"type:varchar(50);not null;column:type;index" json:"type"`
	NotifyURL string `gorm:"type:varchar(500);not null;column:notify_url" json:"notify_url"`
	Username  string `gorm:"type:varchar(255);column:username" json:"username"`
	Password  string `gorm:"type:varchar(255);column:password" json:"password"`
	TokenURL  string `gorm:"type:varchar(500);column:token_url" json:"token_url"`
}

func (RemoteNotifyConfig) TableName() string {
	return "remote_notify_config"
}
