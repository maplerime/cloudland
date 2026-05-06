package model

import (
	"web/src/dbs"

	_ "github.com/lib/pq"
)

func init() {
	// Migrate N9E schema
	dbs.AutoMigrate(
		&N9EBusinessGroup{},
		&N9ECPURule{},
		&N9EMemoryRule{},
		&N9EBandwidthRule{},
		&N9EVMRuleLink{},
	)
}

// ============================================
// N9E Business Group (Level 1)
// Corresponds to Business Group in N9E system
// ============================================
func (N9EBusinessGroup) TableName() string {
	return "n9e_business_group"
}

type N9EBusinessGroup struct {
	Model
	Name               string `gorm:"type:varchar(128);column:name"` // Uniqueness enforced by application logic
	Owner              string `gorm:"type:varchar(255);index"`
	N9EBusinessGroupID int64  `gorm:"column:n9e_business_group_id;index"` // Business Group ID returned by N9E system
	RegionID           string `gorm:"type:varchar(64)"`
	Level              string `gorm:"type:varchar(32)"` // critical, warning, info
	Enabled            bool   `gorm:"default:true"`
}

// ============================================
// N9E CPU Rule (Level 2)
// Corresponds to Alert Rule in N9E system
// ============================================
func (N9ECPURule) TableName() string {
	return "n9e_cpu_rule"
}

type N9ECPURule struct {
	Model
	RuleID            string `gorm:"column:rule_id;type:varchar(36);index"` // User-specified business rule ID
	BusinessGroupUUID string `gorm:"column:business_group_uuid;type:varchar(36);index;not null;references:n9e_business_group(uuid)"`
	N9EAlertRuleID    int64  `gorm:"column:n9e_alert_rule_id;index"` // Alert Rule ID returned by N9E system
	Name              string `gorm:"type:varchar(128);column:name"`
	Owner             string `gorm:"type:varchar(255);index"`

	// CPU rule parameters
	Duration        int    `gorm:"check:duration >= 1"`         // Duration in seconds
	DurationMinutes int    `gorm:"column:duration_minutes"`     // N9E alert duration in minutes
	Operator        string `gorm:"type:varchar(4);default:'>'"` // Comparison operator: >, <, >=

	Enabled bool `gorm:"default:true"`
}

// ============================================
// N9E Memory Rule (Level 2)
// ============================================
func (N9EMemoryRule) TableName() string {
	return "n9e_memory_rule"
}

type N9EMemoryRule struct {
	Model
	RuleID            string `gorm:"column:rule_id;type:varchar(36);index"` // User-specified business rule ID
	BusinessGroupUUID string `gorm:"column:business_group_uuid;type:varchar(36);index;not null;references:n9e_business_group(uuid)"`
	N9EAlertRuleID    int64  `gorm:"column:n9e_alert_rule_id;index"` // Alert Rule ID returned by N9E system
	Name              string `gorm:"type:varchar(128);column:name"`
	Owner             string `gorm:"type:varchar(255);index"`

	// Memory rule parameters
	Duration        int    `gorm:"check:duration >= 1"`         // Duration in seconds
	DurationMinutes int    `gorm:"column:duration_minutes"`     // N9E alert duration in minutes
	Operator        string `gorm:"type:varchar(4);default:'>'"` // Comparison operator: >, <, >=

	Enabled bool `gorm:"default:true"`
}

// ============================================
// N9E Bandwidth Rule (Level 2) - Simplified to unidirectional
// ============================================
func (N9EBandwidthRule) TableName() string {
	return "n9e_bandwidth_rule"
}

type N9EBandwidthRule struct {
	Model
	RuleID            string `gorm:"column:rule_id;type:varchar(36);index"` // User-specified business rule ID
	BusinessGroupUUID string `gorm:"column:business_group_uuid;type:varchar(36);index;not null;references:n9e_business_group(uuid)"`
	N9EAlertRuleID    int64  `gorm:"column:n9e_alert_rule_id;index"` // Alert Rule ID returned by N9E system
	Name              string `gorm:"type:varchar(128);column:name"`
	Owner             string `gorm:"type:varchar(255);index"`

	// Bandwidth rule parameters (unidirectional simplified version)
	Direction       string `gorm:"type:varchar(8);check:direction IN ('in','out')"` // in or out
	Duration        int    `gorm:"check:duration >= 1"`                             // Duration in seconds
	DurationMinutes int    `gorm:"column:duration_minutes"`                         // N9E alert duration in minutes
	Operator        string `gorm:"type:varchar(4);default:'>'"`                     // Comparison operator: >, <, >=

	Enabled bool `gorm:"default:true"`
}

// ============================================
// N9E VM Rule Link
// Association relationship between VM and rules
// ============================================
func (N9EVMRuleLink) TableName() string {
	return "n9e_vm_rule_link"
}

type N9EVMRuleLink struct {
	Model
	RuleType          string  `gorm:"type:varchar(32);index"`                            // cpu, memory, bandwidth
	RuleUUID          string  `gorm:"column:rule_uuid;type:varchar(36);index;not null"`  // UUID of the associated specific Rule
	BusinessGroupUUID string  `gorm:"column:business_group_uuid;type:varchar(36);index"` // For convenient querying
	VMUUID            string  `gorm:"column:vm_uuid;type:varchar(36);index"`             // VM instance UUID
	Interface         string  `gorm:"type:varchar(32)"`                                  // eth0, eth1, etc
	Owner             string  `gorm:"type:varchar(255);index"`                           // Owner
	Threshold         float64 `gorm:"column:threshold;default:0"`                        // Anchor threshold value, used for DB→VM recovery
	TenantID          string  `gorm:"column:tenant_id;type:varchar(36)"`                 // Tenant UUID, cached for recovery without VM lookup
}
