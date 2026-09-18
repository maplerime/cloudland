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
	Add("system_router", SystemRouter)
}

func SystemRouter(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| system_router.sh '1' 'hyper-1'
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
	hyperID, err := strconv.Atoi(args[1])
	if err != nil || hyperID < 0 {
		logger.Error("Invalid hypervisor ID", err)
		return
	}
	hyperName := args[2]
	// Lock the hyper row so concurrent/repeated status reports for the SAME hyper
	// are serialized: only one of them can (re)establish the system router at a
	// time, and the others observe the route_ip it committed instead of racing to
	// allocate a second public IP.
	hyper := &model.Hyper{}
	err = db.Set("gorm:query_option", "FOR UPDATE").Where("hostid = ?", hyperID).Take(hyper).Error
	if err != nil {
		logger.Error("Failed to query hypervisor", err)
		return
	}
	if hyper.Hostname != hyperName {
		logger.Error("Hypervisor hostname mismatch", err)
		return
	}
	subnets := []*model.Subnet{}
	err = db.Where("type = 'public'").Find(&subnets).Error
	if err != nil {
		logger.Error("Failed to get public subnet", err)
		return
	}
	var sysIface *model.Interface
	// Try to reuse the address this hyper already owns. Resolve it through the
	// hyper's OWN system interface (addresses.interface -> interface.hyper), not by
	// the route_ip string: the same address string exists in multiple subnets, so a
	// string-only lookup could bind to another subnet's row or one owned by another
	// hyper. Matching via ownership makes both impossible.
	if hyper.RouteIP != "" {
		var ifaceIDs []int64
		err = db.Model(&model.Interface{}).Where("hyper = ? AND type = ?", hyperID, "system").Pluck("id", &ifaceIDs).Error
		if err != nil {
			logger.Error("Failed to query system interfaces", err)
			return
		}
		if len(ifaceIDs) > 0 {
			address := &model.Address{}
			qErr := db.Set("gorm:query_option", "FOR UPDATE").Preload("Subnet").Where("address = ? AND allocated = ? AND interface IN (?)", hyper.RouteIP, true, ifaceIDs).Take(address).Error
			if qErr == nil {
				sysIface = &model.Interface{Address: address}
			} else {
				logger.Infof("Hyper %d route_ip %s is not owned by a live system interface, reallocating: %v", hyperID, hyper.RouteIP, qErr)
			}
		}
	}
	// No reusable address (new hyper, or a stale/broken route_ip): allocate a fresh
	// public IP. Clean up any existing system interfaces first so a hyper never
	// accumulates more than one system IP (self-heals past leaks).
	if sysIface == nil {
		if err = CleanupSystemInterfaces(ctx, int32(hyperID)); err != nil {
			logger.Error("Failed to cleanup old system interfaces", err)
			return
		}
		hyper.RouteIP = ""
		for _, subnet := range subnets {
			sysIface, err = CreateInterface(ctx, subnet, 0, 0, int32(hyperID), 0, 0, "", "", hyperName, "system", nil, false)
			if err == nil && sysIface != nil {
				hyper.RouteIP = sysIface.Address.Address
				err = db.Model(&model.Hyper{}).Where("id = ?", hyper.ID).Updates(map[string]interface{}{
					"route_ip": hyper.RouteIP,
				}).Error
				if err != nil {
					logger.Error("Failed to save hyper address", err)
					return
				}
				break
			}
			logger.Errorf("Failed to create system router interface for hypervisor %d from subnet %d, %v", hyperID, subnet.ID, err)
		}
	}
	if sysIface == nil {
		logger.Errorf("Failed to allocate public ip for system router of hypervisor %d", hyperID)
		return
	}
	subnet := sysIface.Address.Subnet
	control := fmt.Sprintf("inter=%d", hyperID)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/system_router.sh '%d' '%s' '%s'", subnet.Vlan, sysIface.Address.Address, subnet.Gateway)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Add_fwrule execution failed", err)
		return
	}
	return
}
