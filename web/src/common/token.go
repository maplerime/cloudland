/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"web/src/model"

	"github.com/golang-jwt/jwt/v4"
)

type TokenClaim struct {
	OrgID      int64
	Role       model.Role
	InstanceID int    `json:"instanceID"`
	Secret     string `json:"secret"`
	jwt.RegisteredClaims
}
