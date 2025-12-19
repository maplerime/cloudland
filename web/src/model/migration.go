/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type Migration struct {
	Model
	Name        string `gorm:"type:varchar(64)"`
	InstanceID  int64
	Instance    *Instance `gorm:"foreignkey:InstanceID"`
	Force       bool      `gorm:"default:false"`
	Type        string    `gorm:"type:varchar(32)"`
	SourceHyper int32
	TargetHyper int32
	Phases      []*Task `gorm:"foreignkey:Mission"`
	Status      string  `gorm:"type:varchar(32)"`
}

func init() {
	dbs.AutoMigrate(&Migration{})
}
