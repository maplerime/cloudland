/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"web/src/dbs"
)

type APIKey struct {
	Model
	Owner       int64      `gorm:"default:1"`
	UserID      int64      `gorm:"index"`
	Name        string     `gorm:"size:64"`
	APIKey      string     `gorm:"size:64;unique_index"`
	APIKeyHash  string     `gorm:"size:255"`
	Description string     `gorm:"size:255"`
	ExpiresAt   *time.Time
	Disabled    bool       `gorm:"default:false"`
}

func (APIKey) TableName() string { return "api_keys" }

func init() {
	dbs.AutoMigrate(&APIKey{})
}
