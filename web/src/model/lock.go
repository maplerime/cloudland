/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package model

import (
	"time"
	"web/src/dbs"
)

type Lock struct {
	ID        int64 `gorm:"primary_key"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Name string `gorm:"type:varchar(128);unique_index"`
}

func init() {
	dbs.AutoMigrate(&Lock{})
}
