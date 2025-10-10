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
	Add("create_keepalived_conf", CreateKeepalivedConf)
}

func CreateKeepalivedConf(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| create_keepalived_conf.sh '1' '0' 'master'
	db := DB()
	argn := len(args)
	if argn < 3 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	vrrpID, err := strconv.Atoi(args[1])
	if err != nil || vrrpID < 0 {
		logger.Error("Invalid vrrp ID", err)
		return
	}
	hyperID, err := strconv.Atoi(args[2])
	if err != nil || hyperID < 0 {
		logger.Error("Invalid hypervisor ID", err)
		return
	}
	role := args[3]
	vrrpInstance := &model.VrrpInstance{Model: model.Model{ID: int64(vrrpID)}}
	err = db.Preload("VrrpSubnet").Take(vrrpInstance).Error
	if err != nil {
		logger.Error("Failed to query vrrp instance", err)
		return
	}
	hyper := &model.Hyper{}
	err = db.Where("hostid = ?", hyperID).Take(hyper).Error
	if err != nil {
		logger.Error("Failed to query hypervisor", err)
		return
	}
	err = db.Model(&model.Interface{}).Where("type == 'vrrp' and name == ? and device == ?", role, vrrpID).Updates(map[string]interface{}{
		"hyper": hyperID}).Error
	if err != nil {
		logger.Error("Failed to update interface", err)
	}
	vrrpIface1 := &model.Interface{}
	vrrpIface2 := &model.Interface{}
	err = db.Where("type == 'vrrp' and name == 'master' and device == ?", vrrpID).Take(vrrpIface1).Error
	if err != nil {
		logger.Error("Failed to query vrrp interface 1", err)
		return
	}
	err = db.Where("type == 'vrrp' and name == 'backup' and device == ?", vrrpID).Take(vrrpIface2).Error
	if err != nil {
		logger.Error("Failed to query vrrp interface 2", err)
		return
	}
	if role == "master" {
		hyperGroup := ""
		hyperGroup, err = GetHyperGroup(ctx, vrrpInstance.ZoneID, int32(hyperID))
		if err != nil {
			logger.Error("Failed to get hyper group", err)
			return
		}
		control := "select=" + hyperGroup
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'backup'", vrrpInstance.RouterID, vrrpInstance.ID, vrrpInstance.VrrpSubnet.Vlan, vrrpIface2.MacAddr, vrrpIface2.Address.Address, vrrpIface1.MacAddr, vrrpIface1.Address.Address)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Add_fwrule execution failed", err)
			return
		}
	}
	return
}
