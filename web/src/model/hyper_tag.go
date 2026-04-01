/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"web/src/dbs"
)

// HyperTag stores key-value tags on hypervisors for capability filtering.
// Examples: gpu=nvidia-a100, nvme=true, nested=true
type HyperTag struct {
	ID        int64     `gorm:"primary_key"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Hostid    int32  `gorm:"index"`
	TagName   string `gorm:"type:varchar(64);index"`
	TagValue  string `gorm:"type:varchar(256)"`
}

func init() {
	dbs.AutoMigrate(&HyperTag{})
}
