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
	Add("create_keepalived_conf", CreateKeepalivedConf)
}

func sendVrrpFdbRules(ctx context.Context, vrrpID int64, vrrpIface1, vrrpIface2 *model.Interface) (err error) {
	db := DB()
	hyper1 := &model.Hyper{}
	err = db.Where("hostid = ?", vrrpIface1.Hyper).Take(hyper1).Error
	if err != nil {
		logger.Error("Failed to query vrrp hypervisor 1", err)
		return
	}
	hyper2 := &model.Hyper{}
	err = db.Where("hostid = ?", vrrpIface2.Hyper).Take(hyper2).Error
	if err != nil {
		logger.Error("Failed to query vrrp hypervisor 2", err)
		return
	}
	vrrpRules := []*FdbRule{
		{
			Instance: vrrpIface1.Name,
			Vni:      vrrpIface1.Address.Subnet.Vlan,
			InnerIP:  vrrpIface1.Address.Address,
			InnerMac: vrrpIface1.MacAddr,
			OuterIP:  hyper1.HostIP,
			Gateway:  "nogateway",
			Router:   vrrpIface1.Address.Subnet.RouterID,
		},
		{
			Instance: vrrpIface2.Name,
			Vni:      vrrpIface2.Address.Subnet.Vlan,
			InnerIP:  vrrpIface2.Address.Address,
			InnerMac: vrrpIface2.MacAddr,
			OuterIP:  hyper2.HostIP,
			Gateway:  "nogateway",
			Router:   vrrpIface2.Address.Subnet.RouterID,
		},
	}
	fdbJson, _ := json.Marshal(vrrpRules)
	control := fmt.Sprintf("toall=group-vrrp-%d:%d,%d", vrrpID, vrrpIface1.Hyper, vrrpIface2.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/add_fwrule.sh <<EOF\n%s\nEOF", fdbJson)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("add_fw.sh execution failed", err)
		return
	}
	return
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
	if role == "master" {
		vrrpIface2 := &model.Interface{}
		err = db.Preload("Address").Preload("Address.Subnet").Where("type = 'vrrp' and name = ? and device = ?", role, vrrpID).Take(vrrpIface2).Error
		if err != nil {
			logger.Error("Failed to query vrrp interface 2", err)
			return
		}
		hyperGroup := ""
		hyperGroup, err = GetHyperGroup(ctx, vrrpInstance.ZoneID, int32(hyperID))
		if err != nil {
			logger.Error("Failed to get hyper group", err)
			return
		}
		control := "select=" + hyperGroup
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'backup'", vrrpInstance.RouterID, vrrpInstance.ID, vrrpInstance.VrrpSubnet.Vlan, vrrpIface2.MacAddr, vrrpIface2.Address.Address, vrrpIface.MacAddr, vrrpIface.Address.Address)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("create_keepalived_conf.sh execution failed", err)
			return
		}
	}
	return
}
