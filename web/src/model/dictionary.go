/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"

	"github.com/jinzhu/gorm"
)

const (
	DICT_CATEGORY_OS_FAMILY          = "os_family"
	DICT_CATEGORY_STORAGE_POOL       = "storage_pool"
	DICT_CATEGORY_STORAGE_POOL_GROUP = "storage_pool_group"
	DICT_CATEGORY_IPGROUP            = "ipgroup"
)

type Dictionary struct {
	Model
	Owner     int64  `gorm:"default:1"` /* The organization ID of the resource */
	Category  string `gorm:"column:category;type:varchar(64);index"`
	Name      string `gorm:"type:varchar(64)"`
	ShortName string `gorm:"type:varchar(64)"`
	Value     string `gorm:"type:text"`
	SubType1  string `gorm:"type:varchar(32);default:''"` // data center
	SubType2  string `gorm:"type:varchar(32);default:''"` // ddos/ ddospro / siteip
	SubType3  string `gorm:"type:varchar(32);default:''"` //
}

func init() {
	dbs.AutoMigrate(&Dictionary{})
	dbs.AutoUpgrade("dictionary_drop_value_unique_v2", func(db *gorm.DB) error {
		_ = db.Exec("DROP INDEX IF EXISTS uix_dictionaries_value").Error
		return nil
	})
}
