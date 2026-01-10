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
	"web/src/routes"

	"github.com/jinzhu/gorm"
)

var floatingIpAdmin = &routes.FloatingIpAdmin{}

func init() {
	Add("launch_vm", LaunchVM)
}

type FdbRule struct {
	Instance string `json:"instance"`
	Vni      int64  `json:"vni"`
	InnerIP  string `json:"inner_ip"`
	InnerMac string `json:"inner_mac"`
	OuterIP  string `json:"outer_ip"`
	Gateway  string `json:"gateway"`
	Router   int64  `json:"router"`
}

func sendFdbRules(ctx context.Context, instance *model.Instance, vrrpInstance *model.VrrpInstance, instIface *model.Interface) (err error) {
	if instance != nil && instance.RouterID == 0 {
		logger.Error("No need to send fdb for classic")
		return
	}
	db := DB()
	localRules := []*FdbRule{}
	spreadRules := []*FdbRule{}
	hyperNode := int32(-1)
	var interfaces []*model.Interface
	routerID := int64(0)
	if instance != nil {
		hyperNode = instance.Hyper
		interfaces = instance.Interfaces
		routerID = instance.RouterID
	} else if vrrpInstance != nil {
		routerID = vrrpInstance.RouterID
	}
	if instIface != nil {
		hyperNode = instIface.Hyper
		interfaces = []*model.Interface{instIface}
	}
	if hyperNode == -1 {
		logger.Error("Invalid hyper node")
		return
	}
	hyper := &model.Hyper{}
	err = db.Where("hostid = ?", hyperNode).Take(hyper).Error
	if err != nil || hyper.Hostid < 0 {
		logger.Error("Failed to query hypervisor")
		return
	}
	for _, iface := range interfaces {
		subnetType := iface.Address.Subnet.Type
		if subnetType != string(Public) {
			gateway := iface.Address.Subnet.Gateway
			if subnetType == string(Vrrp) {
				gateway = "nogateway"
			}
			spreadRules = append(spreadRules, &FdbRule{Instance: iface.Name, Vni: iface.Address.Subnet.Vlan, InnerIP: iface.Address.Address, InnerMac: iface.MacAddr, OuterIP: hyper.HostIP, Gateway: gateway, Router: iface.Address.Subnet.RouterID})
		}
	}
	allIfaces := []*model.Interface{}
	hyperSet := make(map[int32]struct{})
	err = db.Preload("Address").Preload("Address.Subnet").Preload("Address.Subnet.Router").Where("router_id = ? and type <> 'gateway' and hyper <> ?", routerID, hyperNode).Find(&allIfaces).Error
	if err != nil {
		logger.Error("Failed to query all interfaces", err)
		return
	}
	for _, iface := range allIfaces {
		subnetType := iface.Address.Subnet.Type
		if iface.Address == nil || iface.Address.Subnet == nil || subnetType == "public" {
			continue
		}
		if iface.Hyper == -1 {
			continue
		}
		hyper := &model.Hyper{}
		hyperErr := db.Where("hostid = ? and hostid != ?", iface.Hyper, hyperNode).Take(hyper).Error
		if hyperErr != nil {
			logger.Error("Failed to query hypervisor", hyperErr)
			continue
		}
		hyperSet[iface.Hyper] = struct{}{}
		gateway := iface.Address.Subnet.Gateway
		if subnetType == string(Vrrp) {
			gateway = "nogateway"
		}
		localRules = append(localRules, &FdbRule{Instance: iface.Name, Vni: iface.Address.Subnet.Vlan, InnerIP: iface.Address.Address, InnerMac: iface.MacAddr, OuterIP: hyper.HostIP, Gateway: gateway, Router: iface.Address.Subnet.RouterID})
	}
	if len(hyperSet) > 0 && len(spreadRules) > 0 {
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
		fdbJson, _ := json.Marshal(spreadRules)
		control := "toall=" + hyperList
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/add_fwrule.sh <<EOF\n%s\nEOF", fdbJson)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Add_fwrule execution failed", err)
			return
		}
	}
	if len(localRules) > 0 {
		fdbJson, _ := json.Marshal(localRules)
		control := fmt.Sprintf("inter=%d", hyperNode)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/add_fwrule.sh <<EOF\n%s\nEOF", fdbJson)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Add_fwrule execution failed", err)
			return
		}
	}
	return
}

func LaunchVM(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| launch_vm.sh '127' 'running' '3' 'reason'
	db := DB()
	argn := len(args)
	if argn < 4 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	instID, err := strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid instance ID", err)
		return
	}
	instance := &model.Instance{Model: model.Model{ID: int64(instID)}}
	reason := ""
	errHndl := ctx.Value("error")
	if errHndl != nil {
		reason = "Resource is not enough"
		err = db.Model(instance).Updates(map[string]interface{}{
			"status": "error",
			"reason": reason}).Error
		if err != nil {
			logger.Error("Failed to update instance", err)
		}
		return
	}
	err = db.Preload("Volumes").Take(instance).Error
	if err != nil {
		logger.Error("Invalid instance ID", err)
		reason = err.Error()
		return
	}
	err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("Address.Subnet.Router").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.updated_at")
	}).Preload("SecondAddresses.Subnet").Where("instance = ?", instID).Find(&instance.Interfaces).Error
	if err != nil {
		logger.Error("Failed to get interfaces", err)
		reason = err.Error()
		return
	}
	serverStatus := args[2]
	hyperID, err := strconv.Atoi(args[3])
	if err != nil {
		logger.Error("Invalid hyper ID", err)
		reason = err.Error()
		return
	}
	reason = args[4]
	instance.Hyper = int32(hyperID)
	hyper := &model.Hyper{}
	err = db.Where("hostid = ?", hyperID).Take(hyper).Error
	if err != nil {
		logger.Error("Failed to query hypervisor", err)
		return
	}
	instance.ZoneID = hyper.ZoneID
	if instance.Status != model.InstanceStatusMigrating {
		err = db.Model(&model.Instance{Model: model.Model{ID: int64(instID)}}).Updates(map[string]interface{}{
			"status": serverStatus,
			"hyper":  int32(hyperID),
			"zoneID": hyper.ZoneID,
			"reason": reason}).Error
		if err != nil {
			logger.Error("Failed to update instance", err)
			return
		}
		err = db.Model(&model.Interface{}).Where("instance = ?", instance.ID).Update(map[string]interface{}{"hyper": int32(hyperID)}).Error
		if err != nil {
			logger.Error("Failed to update interface", err)
			return
		}
	}
	if serverStatus == "running" && reason == "sync" {
		err = syncMigration(ctx, instance)
		if err != nil {
			logger.Error("Failed to sync migration info", err)
			return
		}
		err = syncNicInfo(ctx, instance)
		if err != nil {
			logger.Error("Failed to sync nic info", err)
			return
		}
		if instance.RouterID > 0 {
			err = syncFloatingIp(ctx, instance)
			if err != nil {
				logger.Error("Failed to sync floating ip", err)
				return
			}
		}
	}
	return
}

func syncMigration(ctx context.Context, instance *model.Instance) (err error) {
	migration := &model.Migration{}
	db := DB()
	err = db.Preload("Phases", "name = 'Prepare_Source' and status == 'failed'").Where("instance_id = ? and source_hyper = ?", instance.ID, instance.Hyper).Last(migration).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = nil
			return
		}
		logger.Error("Failed to get migrations", err)
		return
	}
	for _, task := range migration.Phases {
		err = execSourceMigrate(ctx, instance, migration, task.ID, "cold")
		if err != nil {
			logger.Error("Failed to exec source migration", err)
			return
		}
	}
	return
}

func syncNicInfo(ctx context.Context, instance *model.Instance) (err error) {
	vlans := []*VlanInfo{}
	for _, iface := range instance.Interfaces {
		var vlanInfo *VlanInfo
		vlanInfo, err = GetInterfaceInfo(ctx, instance, iface)
		if err != nil {
			logger.Error("Failed to get interface info", err)
			return
		}
		vlans = append(vlans, vlanInfo)
	}
	jsonData, err := json.Marshal(vlans)
	if err != nil {
		logger.Error("Failed to marshal instance json data", err)
		return
	}
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/sync_nic_info.sh '%d' '%s' '%s' <<EOF\n%s\nEOF", instance.ID, instance.Hostname, GetImageOSCode(ctx, instance), jsonData)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Execute floating ip failed", err)
		return
	}
	return
}

func syncFloatingIp(ctx context.Context, instance *model.Instance) (err error) {
	db := DB()
	var primaryIface *model.Interface
	for i, iface := range instance.Interfaces {
		if iface.PrimaryIf {
			primaryIface = instance.Interfaces[i]
			break
		}
	}
	if primaryIface != nil {
		floatingIps := []*model.FloatingIp{}
		err = db.Preload("Interface").Preload("Interface.Address").Preload("Interface.Address.Subnet").Where("instance_id = ?", instance.ID).Find(&floatingIps).Error
		if err != nil {
			logger.Error("Failed to get floating ip", err)
			return
		}
		for _, floatingIp := range floatingIps {
			err = floatingIpAdmin.EnsureSubnetID(ctx, floatingIp)
			if err != nil {
				logger.Error("Failed to ensure subnet_id", err)
				continue
			}

			pubSubnet := floatingIp.Interface.Address.Subnet
			control := fmt.Sprintf("inter=%d", instance.Hyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_floating.sh '%d' '%s' '%s' '%d' '%s' '%d' '%d' '%d' '%d'", floatingIp.RouterID, floatingIp.FipAddress, pubSubnet.Gateway, pubSubnet.Vlan, primaryIface.Address.Address, primaryIface.Address.Subnet.Vlan, floatingIp.ID, floatingIp.Inbound, floatingIp.Outbound)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Execute floating ip failed", err)
				return
			}
		}
	}
	return
}
