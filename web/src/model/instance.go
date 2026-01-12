/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type InstanceStatus string

const (
	InstanceStatusPending      InstanceStatus = "pending"
	InstanceStatusRunning      InstanceStatus = "running"
	InstanceStatusShutoff      InstanceStatus = "shut_off"
	InstanceStatusPaused       InstanceStatus = "paused"
	InstanceStatusMigrating    InstanceStatus = "migrating"
	InstanceStatusReinstalling InstanceStatus = "reinstalling"
	InstanceStatusResizing     InstanceStatus = "resizing"
	InstanceStatusDeleting     InstanceStatus = "deleting"
	InstanceStatusDeleted      InstanceStatus = "deleted"
)

func (s InstanceStatus) String() string {
	return string(s)
}

// UserDataType 用户数据类型常量
const (
	UserDataTypePlain  = "plain"
	UserDataTypeBase64 = "base64"
)

// IsValidUserDataType 检查用户数据类型是否有效
func IsValidUserDataType(userdataType string) bool {
	switch userdataType {
	case UserDataTypePlain, UserDataTypeBase64:
		return true
	}
	return false
}

type Instance struct {
	Model
	Owner        int64          `gorm:"default:1"` /* The organization ID of the resource */
	Hostname     string         `gorm:"unique_index:idx_router_instance;type:varchar(128)"`
	Domain       string         `gorm:"type:varchar(128)"`
	Status       InstanceStatus `gorm:"type:varchar(32)"`
	Reason       string         `gorm:"type:text"`
	FloatingIps  []*FloatingIp  `gorm:"foreignkey:InstanceID",gorm:"PRELOAD:false`
	Volumes      []*Volume      `gorm:"foreignkey:InstanceID",gorm:"PRELOAD:false"`
	Interfaces   []*Interface   `gorm:"foreignkey:Instance"`
	Portmaps     []*Portmap     `gorm:"foreignkey:instanceID"`
	Cpu          int32          `gorm:"default:0"`
	Memory       int32          `gorm:"default:0"`
	Disk         int32          `gorm:"default:0"`
	FlavorID     int64
	Flavor       *Flavor `gorm:"foreignkey:FlavorID"`
	ImageID      int64
	Image        *Image `gorm:"foreignkey:ImageID"`
	Snapshot     int64
	Keys         []*Key `gorm:"many2many:instance_keys;"`
	PasswdLogin  bool   `gorm:"default:false"`
	Userdata     string `gorm:"type:text"`
	UserdataType string `gorm:"type:varchar(16);default:'plain'"`
	LoginPort    int32
	Hyper        int32 `gorm:"index;default:-1"`
	ZoneID       int64
	Zone         *Zone `gorm:"foreignkey:ZoneID"`
	RouterID     int64 `gorm:"unique_index:idx_router_instance"`
	Router       *Router
}

func init() {
	dbs.AutoMigrate(&Instance{})
}
