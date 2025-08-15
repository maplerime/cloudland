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
	Add("attach_vm_nic", AttachInterface)
}

func AttachInterface(ctx context.Context, args []string) (status string, err error) {
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
	macAddr := args[2]
	iface := &model.Interface{}
	err = db.Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("Address").Preload("Address.Subnet").Where("instance = ? and mac_addr = ?", instID, macAddr).Take(iface).Error
	if err != nil {
		logger.Error("Failed to get interface", err)
		return
	}
	err = sendFdbRules(ctx, instance, iface)
	if err != nil {
		logger.Error("Failed to send fdb rules for interface", err)
		return
	}
	return
}
