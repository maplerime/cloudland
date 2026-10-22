package model

import (
	"github.com/jinzhu/gorm"
	"time"
	"web/src/dbs"
)

// Structured observations and optional feature judgments share the VM record.
// Completed reports are collected and removed by report_rc on the compute node.
type VMInfectionRecord struct {
	Model
	InstanceUUID     string     `gorm:"type:varchar(64);not null;index"`
	HyperID          int32      `gorm:"not null"`
	HyperHostname    string     `gorm:"-" json:"-"`
	InspectionResult string     `gorm:"type:text"`
	CollectedAt      *time.Time `gorm:"index"`
	RulesetVersion   string     `gorm:"type:varchar(128)"`
	MatchSummary     string     `gorm:"type:varchar(256)"`
	Verdict          string     `gorm:"type:varchar(32)"`
	MatchRuleIDs     string     `gorm:"type:varchar(1024)"`
	IsolationStatus  string     `gorm:"type:varchar(32);index"`
	IsolationCPUs    string     `gorm:"type:varchar(255)"`
	IsolationSource  string     `gorm:"type:varchar(16)"`
	OperationAt      time.Time  `gorm:"not null;index"`
	ScanID           string     `gorm:"type:varchar(128)"`
	Remark           string     `gorm:"type:varchar(255)"`
	ErrorMessage     string     `gorm:"type:varchar(512)"`
}

func (VMInfectionRecord) TableName() string { return "vm_infection_records" }
func init() {
	dbs.AutoMigrate(&VMInfectionRecord{})
	dbs.AutoUpgrade("vm_infection_records_uuid_unique", func(db *gorm.DB) error {
		return db.Model(&VMInfectionRecord{}).AddUniqueIndex("idx_vm_infection_records_uuid_unique", "uuid").Error
	})
}
