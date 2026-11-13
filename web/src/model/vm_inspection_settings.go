package model

import (
	"github.com/jinzhu/gorm"
	"web/src/dbs"
)

// HyperID -1 is the regional default; other rows override one Hyper.Hostid.
type VMInspectionSetting struct {
	Model
	HyperID             int32 `gorm:"not null;unique_index"`
	IsolationEnabled    bool  `gorm:"not null;default:false"`
	RecordRetentionDays *int
	FeatureLibraryURL   string `gorm:"type:varchar(512)"`
	FeatureLibraryToken string `gorm:"type:text" json:"-"`
	MatchPolicy         string `gorm:"type:text"`
	HiveStatus          string `gorm:"type:text"`
}

func (VMInspectionSetting) TableName() string { return "vm_inspection_settings" }

// VMInspectionPinWhitelist exempts one VM from automatic inspection-triggered pinning.
type VMInspectionPinWhitelist struct {
	Model
	InstanceUUID string `gorm:"type:varchar(64);not null"`
	Reason       string `gorm:"type:varchar(255)"`
}

func (VMInspectionPinWhitelist) TableName() string { return "vm_inspection_pin_whitelists" }

func init() {
	dbs.AutoMigrate(&VMInspectionSetting{}, &VMInspectionPinWhitelist{})
	dbs.AutoUpgrade("vm_inspection_pin_whitelists_instance_active", func(db *gorm.DB) error {
		return db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_vm_inspection_pin_whitelists_instance_active
			ON vm_inspection_pin_whitelists (instance_uuid) WHERE deleted_at IS NULL`).Error
	})
}
