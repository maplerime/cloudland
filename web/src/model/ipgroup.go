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
	Owner           int64         `gorm:"default:1"` /* The organization ID of the resource */
	Name            string        `gorm:"unique_index;type:varchar(64)"`
	TypeID          int64         `gorm:"index"`
	Type            string        `gorm:"type:varchar(20);default:'system';index"` // system or resource
	DictionaryType  *Dictionary   `gorm:"foreignKey:TypeID;references:ID"`
	Subnets         []*Subnet     `gorm:"foreignkey:GroupID;"`
	FloatingIPs     []*FloatingIp `gorm:"foreignkey:GroupID;"`
	SubnetNames     string        `gorm:"-"`
	FloatingIPNames string        `gorm:"-"`
}

func init() {
	dbs.AutoMigrate(&IpGroup{})
}
