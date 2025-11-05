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
	Add("set_vrrp_ip", SetVrrpIp)
}

func UpdateLoadBalancerStatus(ctx context.Context, vrrpInstance *model.VrrpInstance) (err error) {
	db := DB()
	err = db.Model(&model.LoadBalancer{}).Where("vrrp_instance_id = ?", vrrpInstance.ID).Updates(map[string]interface{}{
		"status": "available"}).Error
	if err != nil {
		logger.Error("Failed to update load balancer status", err)
	}
	return
}

func SetVrrpIp(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| set_vrrp_ip.sh '1' '0' 'MASTER'
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
	hyper := &model.Hyper{}
	err = db.Where("hostid = ?", hyperID).Take(hyper).Error
	if err != nil || hyper.Hostid < 0 {
		logger.Error("Failed to query hypervisor")
		return
	}
	role := args[3]
	vrrpInstance := &model.VrrpInstance{Model: model.Model{ID: int64(vrrpID)}}
	err = db.Preload("VrrpSubnet").Take(vrrpInstance).Error
	if err != nil {
		logger.Error("Failed to query vrrp instance", err)
		return
	}
	err = db.Model(&model.Interface{}).Where("type = 'vrrp' and name = ? and device = ?", role, vrrpID).Updates(map[string]interface{}{
		"hyper": hyperID}).Error
	if err != nil {
		logger.Error("Failed to update interface", err)
	}
	vrrpIface := &model.Interface{}
	err = db.Preload("Address").Preload("Address.Subnet").Where("type = 'vrrp' and name = ? and device = ?", role, vrrpID).Take(vrrpIface).Error
	if err != nil {
		logger.Error("Failed to query vrrp interface", err)
		return
	}
	err = sendFdbRules(ctx, nil, vrrpInstance, vrrpIface)
	if err != nil {
		logger.Error("Failed to send fdb rules for interface", err)
		return
	}
	if role == "MASTER" {
		vrrpIface2 := &model.Interface{}
		err = db.Preload("Address").Preload("Address.Subnet").Where("type = 'vrrp' and name = 'BACKUP' and device = ?", vrrpID).Take(vrrpIface2).Error
		if err != nil {
			logger.Error("Failed to query vrrp interface 2", err)
			return
		}
		hyperGroup := ""
		hyperGroup, err = GetHyperGroup(ctx, vrrpInstance.ZoneID, int32(hyperID))
		if err != nil {
			logger.Error("Failed to get hyper group", err)
			err = UpdateLoadBalancerStatus(ctx, vrrpInstance)
			if err != nil {
				logger.Error("Failed to update load balancer", err)
				return
			}
			return
		}
		control := "select=" + hyperGroup
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/set_vrrp_ip.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'BACKUP'", vrrpInstance.RouterID, vrrpInstance.ID, vrrpInstance.VrrpSubnet.Vlan, vrrpIface2.MacAddr, vrrpIface2.Address.Address, vrrpIface.MacAddr, vrrpIface.Address.Address)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("set vrrp ip execution failed", err)
			return
		}
	} else {
		err = UpdateLoadBalancerStatus(ctx, vrrpInstance)
		if err != nil {
			logger.Error("Failed to update load balancer", err)
			return
		}
	}
	return
}
