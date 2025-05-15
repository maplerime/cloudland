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
	Owner          int64       `gorm:"default:1"` /* The organization ID of the resource */
	Name           string      `gorm:"unique_index;type:varchar(64)"`
	TypeID         int64       `gorm:"index"`
	DictionaryType *Dictionary `gorm:"foreignKey:TypeID;references:ID"`
	Subnets        []*Subnet   `gorm:"foreignkey:GroupID;"`
	SubnetNames    string      `gorm:"-"`
}

type Dictionary struct {
	Model
	Owner    int64  `gorm:"default:1"` /* The organization ID of the resource */
	Category string `gorm:"column:category;type:varchar(64);index"`
	Name     string `gorm:"type:varchar(64)"`
	Value    string `gorm:"unique_index"`
}

func init() {
	dbs.AutoMigrate(&IpGroup{})
	dbs.AutoMigrate(&Dictionary{})
}
