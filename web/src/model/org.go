/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

History:
   Date     Who ID    Description
   -------- --- ---   -----------
   01/13/19 nanjj  Initial code

*/

package model

import (
	"encoding/gob"
	"fmt"

	"web/src/dbs"
)

func init() {
	dbs.AutoMigrate(&Member{}, &Organization{})
	var role Role
	gob.Register(role)
	gob.Register(Member{})
	gob.Register([]*Member{})
}

type Role int

const (
	None   Role = iota /* No permissions  */
	Reader             /* Get List permissions */
	Writer             /* Create Edit Patch permission */
	Owner              /* Invite or Remove user to from org */
	Admin              /* Create user and org */
)

func (r Role) String() string {
	switch r {
	case None:
		return "None"
	case Reader:
		return "Reader"
	case Writer:
		return "Writer"
	case Owner:
		return "Owner"
	case Admin:
		return "Admin"
	default:
		return fmt.Sprintf("%d", int(r))
	}
}

type Organization struct {
	Model
	Owner     int64     `gorm:"default:1"` /* The organization ID of the resource */
	Name      string    `gorm:"size:255;unique_index" json:"name,omitempty"`
	Members   []*Member `gorm:"foreignkey:OrgID"`
	OwnerUser *User     `gorm:"foreignkey:ID";AssociationForeignKey:Owner`
	DefaultSG  int64
}

func (Organization) TableName() string {
	return "organizations"
}

type Member struct {
	Model
	Owner    int64 `gorm:"default:1"` /* The organization ID of the resource */
	UserID   int64
	UserName string
	OrgID    int64
	OrgName  string
	Role     Role
}
