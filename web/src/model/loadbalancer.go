/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type LoadBalancer struct {
	Model
	Owner        int64  `gorm:"unique_index:idx_account_lb;default:1"` /* The organization ID of the resource */
	Name         string `gorm:"unique_index:idx_account_lb;type:varchar(64)"`
	Status       string `gorm:"type:varchar(32)"`
	Hyper        int32  `gorm:"default:-1"`
	Peer         int32  `gorm:"default:-1"`
	VrrpSubnetID int64
	VrrpSubnet   *Subnet       `gorm:"foreignkey:VrrpSubnetID"`
	FloatingIps  []*FloatingIp `gorm:"foreignkey:LoadBalancerID"`
	RouterID     int64         `gorm:"unique_index:idx_router_lb"`
	Router       *Router
	Listeners    []*Listener `gorm:"foreignkey:LoadBalancerID"`
}

type Listener struct {
	Model
	Owner          int64  `gorm:"unique_index:idx_account_listener;default:1"` /* The organization ID of the resource */
	Name           string `gorm:"unique_index:idx_account_listener;type:varchar(64)"`
	Status         string `gorm:"type:varchar(32)"`
	Port           int32  `gorm:"default:-1"`
	InterfaceID    int64
	Interface      *Interface `gorm:"foreignkey:InterfaceID"`
	LoadBalancerID int64
	Certificate    string     `gorm:"type:text"`
	Key            string     `gorm:"type:text"`
	Backends       []*Backend `gorm:"foreignkey:ListenerID"`
}

type Backend struct {
	Model
	Owner       int64  `gorm:"unique_index:idx_account_backend;default:1"` /* The organization ID of the resource */
	Name        string `gorm:"unique_index:idx_account_backend;type:varchar(64)"`
	ListenerID  int64
	BackendAddr string `gorm:"type:varchar(64)"`
}

func init() {
	dbs.AutoMigrate(&LoadBalancer{})
	dbs.AutoMigrate(&Listener{})
	dbs.AutoMigrate(&Backend{})
}
