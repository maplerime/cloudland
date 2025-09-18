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
	err = db.Preload("Subnet").Where("uuid = ? and subnet_id = ?", uuID, subnet.ID).Take(addr).Error
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

	// 构建需要更新的字段映射
	updateFields := make(map[string]interface{})
	updateFields["address"] = addr.Address
	updateFields["netmask"] = addr.Netmask
	updateFields["type"] = addr.Type
	updateFields["allocated"] = addr.Allocated
	updateFields["reserved"] = addr.Reserved
	updateFields["subnet_id"] = addr.SubnetID
	updateFields["interface"] = addr.Interface
	updateFields["second_interface"] = addr.SecondInterface
	updateFields["remark"] = addr.Remark

	if err = db.Model(addr).Updates(updateFields).Error; err != nil {
		logger.Error("Failed to update address, %v", err)
		return NewCLError(ErrAddressUpdateFailed, "Failed to update address", err)
	}

	return
}
