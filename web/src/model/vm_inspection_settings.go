package model

import "web/src/dbs"

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
func init()                                   { dbs.AutoMigrate(&VMInspectionSetting{}) }
