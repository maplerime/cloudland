/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"

	. "web/src/common"
	"web/src/model"
)

type AddressAdmin struct{}

func (a *AddressAdmin) GetAddressByUUID(ctx context.Context, uuID string, subnet *model.Subnet) (addr *model.Address, err error) {
	ctx, db := GetContextDB(ctx)
	addr = &model.Address{}
	err = db.Where("uuid = ? and subnet_id = ?", uuID, subnet.ID).Take(addr).Error
	if err != nil {
		logger.Error("Failed to query address, %v", err)
		return
	}
	return
}

func (a *AddressAdmin) Update(ctx context.Context, addr *model.Address) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		err = fmt.Errorf("Not authorized for this operation")
		logger.Error("Not authorized for this operation", err)
		return
	}

	if err = db.Model(addr).Save(addr).Error; err != nil {
		logger.Error("Failed to update address, %v", err)
		return
	}

	return
}
