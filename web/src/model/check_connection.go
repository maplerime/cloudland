package model

import "web/src/dbs"

// CheckConnection records a request, not the result of a connectivity probe.
type CheckConnection struct {
	Model
	Owner         int64  `gorm:"not null;index"`
	IP            string `gorm:"type:varchar(45);not null;index"`
	Status        string `gorm:"type:varchar(16);not null"`
	DispatchError string `gorm:"type:text"`
}

func init() {
	dbs.AutoMigrate(&CheckConnection{})
}
