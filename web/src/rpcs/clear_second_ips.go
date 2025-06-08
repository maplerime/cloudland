/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("clear_second_ips", ClearSecondIps)
}

func ClearSecondIps(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| clear_second_ips.sh 5 52:54:55:99:a2:6e linux
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
	primaryIface := &model.Interface{}
	err = db.Preload("SiteSubnets").Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("Addresses").Preload("Addresses.Subnet").Where("instance = ? and PrimaryIf = true", instID).Take(primaryIface).Error
	if err != nil {
		logger.Error("Failed to get interface", err)
		return
	}
	mac := args[2]
	osCode := args[3]
	var moreAddresses []string
	_, moreAddresses, err = GetInstanceNetworks(ctx, instance, primaryIface)
	if err != nil {
		logger.Errorf("Failed to get instance networks, %v", err)
		return
	}
	var moreAddrsJson []byte
	moreAddrsJson, err = json.Marshal(moreAddresses)
	if err != nil {
		logger.Errorf("Failed to marshal instance json data, %v", err)
		return
	}
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/apply_second_ips.sh '%d' '%s' '%s' 'true'<<EOF\n%s\nEOF", instID, mac, osCode, moreAddrsJson)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Applying second ips execution failed", err)
		return
	}
	return
}
