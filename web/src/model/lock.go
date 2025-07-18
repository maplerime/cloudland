package model

import (
	"web/src/dbs"
)

type Lock struct {
	Model
	Name string `gorm:"type:varchar(128);unique_index"`
}

func init() {
	dbs.AutoMigrate(&Lock{})
}
