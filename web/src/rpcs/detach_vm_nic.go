/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"fmt"
	"strconv"

	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("detach_vm_nic", DetachInterface)
}

func DetachInterface(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| detach_nic.sh 5 101 1
	db := DB()
	argn := len(args)
	if argn < 3 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	instID, err := strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid gateway ID", err)
		return
	}
	instance := &model.Instance{Model: model.Model{ID: int64(instID)}}
	err = db.Take(instance).Error
	if err != nil {
		logger.Error("Invalid instance ID", err)
		return
	}
	ifaceID, err := strconv.Atoi(args[2])
	if err != nil {
		logger.Error("Invalid interface ID", err)
		return
	}
	iface := &model.Interface{Model: model.Model{ID: int64(ifaceID)}}
	err = db.Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("Address").Preload("Address.Subnet").Where("instance = ?", instID).Take(iface).Error
	if err != nil {
		logger.Error("Failed to get interface", err)
		return
	}
	err = deleteInterfaces(ctx, instance, []*model.Interface{iface})
	if err != nil {
		logger.Error("Failed to delete interface", err)
		return
	}
	return
}
