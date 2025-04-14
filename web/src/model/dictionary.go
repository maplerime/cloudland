/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type IpGroup struct {
	Model
	Owner   int64       `gorm:"default:1"` /* The organization ID of the resource */
	Name    string      `gorm:"unique_index:idx_router_subnet;type:varchar(64)"`
	TypeID  int64       `gorm:"index"`
	Type    *Dictionary `gorm:"foreignkey:TypeID"`
	Subnets []*Subnet   `gorm:"foreignkey:GroupID;"`
}

type Dictionary struct {
	Model
	Owner int64  `gorm:"default:1"`              /* The organization ID of the resource */
	Type  string `gorm:"type:varchar(64);index"` // 用于区分服务对象
	Name  string `gorm:"type:varchar(64)"`
	Value string `gorm:"unique_index"`
}

func init() {
	dbs.AutoMigrate(&IpGroup{})
	dbs.AutoMigrate(&Dictionary{}) // 新增
}
