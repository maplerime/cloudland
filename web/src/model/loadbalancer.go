/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type VrrpInstance struct {
	Model
	Owner        int64  `gorm:"default:1"` /* The organization ID of the resource */
	Hyper        int32  `gorm:"default:-1"`
	Peer         int32  `gorm:"default:-1"`
	VrrpSubnetID int64
	VrrpSubnet   *Subnet       `gorm:"foreignkey:VrrpSubnetID"`
	InterfaceID    int64
	Interface      *Interface `gorm:"foreignkey:InterfaceID"`
}

type LoadBalancer struct {
	Model
	Owner        int64  `gorm:"default:1"` /* The organization ID of the resource */
	Name         string `gorm:"unique_index:idx_router_lb;type:varchar(64)"`
	Status       string `gorm:"type:varchar(32)"`
	FloatingIps  []*FloatingIp `gorm:"foreignkey:LoadBalancerID"`
	RouterID     int64         `gorm:"unique_index:idx_router_lb"`
	Router       *Router
	VrrpInstanceID int64
	VrrpInstance *VrrpInstance `gorm:"foreignkey:VrrpInstanceID"`
	Listeners    []*Listener `gorm:"foreignkey:LoadBalancerID"`
}

type Listener struct {
	Model
	Owner          int64  `gorm:"default:1"` /* The organization ID of the resource */
	Name           string `gorm:"unique_index:idx_lb_listener;type:varchar(64)"`
	Status         string `gorm:"type:varchar(32)"`
	Port           int32  `gorm:"default:-1"`
	InterfaceID    int64
	Interface      *Interface `gorm:"foreignkey:InterfaceID"`
	LoadBalancerID int64      `gorm:"unique_index:idx_lb_listener"`
	Certificate    string     `gorm:"type:text"`
	Key            string     `gorm:"type:text"`
	Backends       []*Backend `gorm:"foreignkey:ListenerID"`
}

type Backend struct {
	Model
	Owner       int64  `default:1"` /* The organization ID of the resource */
	Name        string `type:varchar(64)"`
	ListenerID  int64
	BackendAddr string `gorm:"type:varchar(64)"`
}

func init() {
	dbs.AutoMigrate(&LoadBalancer{})
	dbs.AutoMigrate(&Listener{})
	dbs.AutoMigrate(&Backend{})
	dbs.AutoMigrate(&VrrpInstance{})
}
