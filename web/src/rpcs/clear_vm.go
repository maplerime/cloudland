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
	Add("clear_vm", ClearVM)
}

func deleteInterfaces(ctx context.Context, instance *model.Instance, instIface *model.Interface) (err error) {
	ctx, db := GetContextDB(ctx)
	hyperSet := make(map[int32]struct{})
	instances := []*model.Instance{}
	hyperNode := instance.Hyper
	hyper := &model.Hyper{}
	err = db.Where("hostid = ?", hyperNode).Take(hyper).Error
	if err != nil {
		logger.Error("Failed to query hypervisor")
		return
	}
	if instance.RouterID > 0 {
		err = db.Where("router_id = ?", instance.RouterID).Find(&instances).Error
		if err != nil {
			logger.Error("Failed to query all instances", err)
			return
		}
		for _, inst := range instances {
			hyperSet[inst.Hyper] = struct{}{}
		}
	}
	hyperList := fmt.Sprintf("group-fdb-%d", hyperNode)
	i := 0
	for key := range hyperSet {
		if i == 0 {
			hyperList = fmt.Sprintf("%s:%d", hyperList, key)
		} else {
			hyperList = fmt.Sprintf("%s,%d", hyperList, key)
		}
		i++
	}
	interfaces := instance.Interfaces
	if instIface != nil {
		interfaces = []*model.Interface{instIface}
	}
	for _, iface := range interfaces {
		if iface.FloatingIp == 0 {
			err = db.Model(&model.Address{}).Where("interface = ?", iface.ID).Update(map[string]interface{}{"allocated": false, "interface": 0}).Error
			if err != nil {
				logger.Error("Failed to Update addresses, %v", err)
				return
			}
		}
		err = db.Model(&model.Address{}).Where("second_interface = ? and interface = 0", iface.ID).Update(map[string]interface{}{"allocated": false, "second_interface": 0}).Error
		if err != nil {
			logger.Error("Failed to Update addresses, %v", err)
			return
		}
		err = db.Model(&model.Address{}).Where("second_interface = ? and interface > 0", iface.ID).Update(map[string]interface{}{"second_interface": 0}).Error
		if err != nil {
			logger.Error("Failed to Update addresses, %v", err)
			return
		}
		if iface.FloatingIp == 0 {
			err = db.Delete(iface).Error
			if err != nil {
				logger.Error("Failed to delete interface", err)
				return
			}
		} else {
			err = db.Model(iface).Update(map[string]interface{}{"instance": 0, "primary_if": false, "name": "fip", "inbound": 0, "outbound": 0, "allow_spoofing": false}).Error
			if err != nil {
				logger.Error("Failed to Update addresses, %v", err)
				return
			}
		}
		err = db.Model(&model.Subnet{}).Where("interface = ?", iface.ID).Updates(map[string]interface{}{
			"interface": 0}).Error
		if err != nil {
			logger.Error("Failed to update subnet", err)
			return
		}
		if instance.RouterID > 0 && hyperNode >= 0 {
			spreadRules := []*FdbRule{{Instance: iface.Name, Vni: iface.Address.Subnet.Vlan, InnerIP: iface.Address.Address, InnerMac: iface.MacAddr, OuterIP: hyper.HostIP, Gateway: iface.Address.Subnet.Gateway, Router: iface.Address.Subnet.RouterID}}
			fdbJson, _ := json.Marshal(spreadRules)
			control := "toall=" + hyperList
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/del_fwrule.sh <<EOF\n%s\nEOF", fdbJson)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Execute floating ip failed", err)
				return
			}
		}
	}
	return
}

func ClearVM(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| clear_vm.sh '127'
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	argn := len(args)
	if argn < 2 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	instID, err := strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid instance ID", err)
		return
	}
	reason := ""
	instance := &model.Instance{Model: model.Model{ID: int64(instID)}}
	err = db.Take(instance).Error
	if err != nil {
		logger.Error("Invalid instance ID", err)
		reason = err.Error()
		return
	}
	err = db.Preload("Address").Preload("Address.Subnet").Preload("Address.Subnet").Where("instance = ?", instID).Find(&instance.Interfaces).Error
	if err != nil {
		logger.Error("Failed to get interfaces", err)
		reason = err.Error()
		return
	}
	err = deleteInterfaces(ctx, instance, nil)
	if err != nil {
		logger.Error("Failed to delete interfaces", err)
		return
	}
	instance.Hostname = fmt.Sprintf("%s-%d", instance.Hostname, instance.CreatedAt.Unix())
	instance.Status = model.InstanceStatusDeleted
	instance.Reason = reason
	instance.Interfaces = nil
	err = db.Save(instance).Error
	if err != nil {
		return
	}
	if err = db.Delete(instance).Error; err != nil {
		logger.Error("Failed to delete instance, %v", err)
		return
	}
	// update attached volumes status to available
	err = db.Model(&model.Volume{}).Where("instance_id = ? and booting = false", instance.ID).Updates(map[string]interface{}{
		"status":      model.VolumeStatusAvailable,
		"target":      "",
		"instance_id": 0,
	}).Error
	if err != nil {
		logger.Error("Failed to update attached volumes", err)
		return
	}
	return
}
