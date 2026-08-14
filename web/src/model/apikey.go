/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"web/src/dbs"
)

// APIKey represents an API key credential used to authenticate REST API
// requests via the X-API-Key header, as an alternative to JWT.
//
// The plain-text key (format: cl_<uuid>_<secret>) is shown to the user only
// once at creation time and is never persisted. Only the UUID lookup portion
// (APIKey) and the bcrypt hash of the secret (APIKeyHash) are stored.
// The owning user is recorded by the embedded Model.Creater field.
type APIKey struct {
	Model
	Owner       int64      `gorm:"default:1"` // org ID the key belongs to (used for org-scoped filtering)
	Name        string     `gorm:"size:64"`
	APIKey      string     `gorm:"size:64;unique_index"` // UUID lookup key (not the secret)
	APIKeyHash  string     `gorm:"size:255"`             // bcrypt hash of the secret portion
	Description string     `gorm:"size:255"`
	ExpiresAt   *time.Time // nil means the key never expires
	Disabled    bool       `gorm:"default:false"`
}

func (APIKey) TableName() string { return "api_keys" }

func init() {
	dbs.AutoMigrate(&APIKey{})
}
