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

func (a *AddressAdmin) GetAddressByUUID(ctx context.Context, uuID string) (addr *model.Address, err error) {
	ctx, db := GetContextDB(ctx)
	addr = &model.Address{}
	err = db.Preload("Subnet").Where("uuid = ?", uuID).Take(addr).Error
	if err != nil {
		logger.Error("Failed to query address, %v", err)
		return nil, NewCLError(ErrAddressNotFound, "Address not found", err)
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
		return NewCLError(ErrPermissionDenied, "Not authorized for this operation", err)
	}

	if err = db.Model(addr).Save(addr).Error; err != nil {
		logger.Error("Failed to update address, %v", err)
		return NewCLError(ErrAddressUpdateFailed, "Failed to update address", err)
	}

	return
}
