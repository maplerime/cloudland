/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type IPWhitelist struct {
	Model
	Owner        int64  `gorm:"default:1"`
	InstanceUUID string `gorm:"type:varchar(64);not null;index;column:instance_uuid"`
	IP           string `gorm:"type:varchar(64);not null"`
	Reason       string `gorm:"type:text"`
}

func init() {
	dbs.AutoMigrate(&IPWhitelist{})
}
