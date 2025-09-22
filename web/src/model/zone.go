/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"web/src/dbs"
)

type Zone struct {
	ID        int64  `gorm:"primary_key"`
	Name      string `gorm:"unique_index"`
	Remark    string `gorm:"type:varchar(512);default:''"`
	Default   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func init() {
	dbs.AutoMigrate(&Zone{})
}
