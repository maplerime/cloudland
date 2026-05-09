/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"fmt"
	"net/http"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	hyperAdmin = &HyperAdmin{}
	hyperView  = &HyperView{}
)

type HyperAdmin struct{}
type HyperView struct{}

func (a *HyperAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, hypers []*model.Hyper, err error) {
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "hostid"
	}
	if query != "" {
		query = fmt.Sprintf("hostname like '%%%s%%'", query)
	}

	hypers = []*model.Hyper{}
	if err = db.Model(&model.Hyper{}).Where("hostid >= 0").Where(query).Count(&total).Error; err != nil {
		return 0, nil, NewCLError(ErrSQLSyntaxError, "Failed to count hypervisors", err)
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Zone").Where("hostid >= 0").Where(query).Find(&hypers).Error; err != nil {
		return 0, nil, NewCLError(ErrSQLSyntaxError, "Failed to retrieve hypervisors", err)
	}
	db = db.Offset(0).Limit(-1)
	for _, hyper := range hypers {
		hyper.Resource = &model.Resource{}
		err = db.Where("hostid = ?", hyper.Hostid).Take(hyper.Resource).Error
	}

	return
}



func (a *HyperAdmin) SetStatus(ctx context.Context, hostID int32, status int32) (err error) {
	hyper, err := a.GetHyperByHostid(ctx, hostID)
	if err != nil {
		return
	}
	if hyper.Status == status {
		return nil // No change needed
	}
	hyper.Status = status
	return a.Update(ctx, hyper)
}

// Update function is used to:
// 1. set the hypervisor status active or disabled
// 2. modify the hypervisor remark
// 3. modify the zone of the hypervisor
// 4. modify the over commit rates of hypervisor
func (a *HyperAdmin) Update(ctx context.Context, hyper *model.Hyper) (err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		logger.Error("Not authorized for this operation", err)
		return
	}
	ctx, db := GetContextDB(ctx)
	hyperInDB := &model.Hyper{ID: hyper.ID}
	if err = db.Preload("Zone").Take(hyperInDB).Error; err != nil {
		logger.Error("Specified hypervisor not found", err)
		return NewCLError(ErrHypervisorNotFound, "Specified hypervisor not found", err)
	}
	// Update the hypervisor status, remark, or zone
	callScript := false
	restartCloudlet := 0
	if hyper.Status != hyperInDB.Status {
		logger.Info("Updating hypervisor status from", hyperInDB.GetStatus(), "to", hyper.GetStatus())
		hyperInDB.Status = hyper.Status
		callScript = true
	}
	// update remark
	hyperInDB.Remark = hyper.Remark
	if hyper.ZoneID != hyperInDB.ZoneID {
		logger.Info("Updating hypervisor zone from", hyperInDB.ZoneID, "to", hyper.ZoneID)
		zone, err := zoneAdmin.Get(ctx, hyper.ZoneID)
		if err != nil {
			logger.Errorf("Failed to get zone(%d), %+v", hyper.ZoneID, err)
			return err
		}
		hyperInDB.Zone = zone
		hyperInDB.ZoneID = zone.ID
		callScript = true
		restartCloudlet = 1
	}
	// update over commit rates
	if hyper.CpuOverRate != hyperInDB.CpuOverRate {
		logger.Info("Updating hypervisor CPU over commit rate from", hyperInDB.CpuOverRate, "to", hyper.CpuOverRate)
		hyperInDB.CpuOverRate = hyper.CpuOverRate
		callScript = true
	}
	if hyper.MemOverRate != hyperInDB.MemOverRate {
		logger.Info("Updating hypervisor memory over commit rate from", hyperInDB.MemOverRate, "to", hyper.MemOverRate)
		hyperInDB.MemOverRate = hyper.MemOverRate
		callScript = true
	}
	if hyper.DiskOverRate != hyperInDB.DiskOverRate {
		logger.Info("Updating hypervisor disk over commit rate from", hyperInDB.DiskOverRate, "to", hyper.DiskOverRate)
		hyperInDB.DiskOverRate = hyper.DiskOverRate
		callScript = true
	}
	err = db.Model(&model.Hyper{}).Where("id = ?", hyper.ID).Updates(map[string]interface{}{
		"status":         hyperInDB.Status,
		"remark":         hyperInDB.Remark,
		"zone_id":        hyperInDB.ZoneID,
		"cpu_over_rate":  hyperInDB.CpuOverRate,
		"mem_over_rate":  hyperInDB.MemOverRate,
		"disk_over_rate": hyperInDB.DiskOverRate,
	}).Error
	if err != nil {
		logger.Error("Failed to update hypervisor", err)
		return
	}
	if callScript {
		logger.Info("Calling script to update hypervisor status")
		// Call the script to update hypervisor status
		control := fmt.Sprintf("inter=%d", hyperInDB.Hostid)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/update_hyper.sh '%d' '%s' '%d' '%f' '%f' '%f'",
			hyperInDB.Status, hyperInDB.Zone.Name, restartCloudlet, hyperInDB.CpuOverRate, hyperInDB.MemOverRate, hyperInDB.DiskOverRate)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Errorf("Failed to call script update hyper %+v", err)
			return
		}
	}
	return
}

func (a *HyperAdmin) GetHyperByHostid(ctx context.Context, hostid int32) (hyper *model.Hyper, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		logger.Error("Not authorized for this operation", err)
		return
	}
	_, db := GetContextDB(ctx)
	hyper = &model.Hyper{}
	if err = db.Preload("Zone").Where("hostid = ?", hostid).Take(hyper).Error; err != nil {
		logger.Error("Failed to query hypervisor", err)
		return nil, NewCLError(ErrHypervisorNotFound, "Specified hypervisor not found", err)
	}

	// Load resource information
	hyper.Resource = &model.Resource{}
	err = db.Where("hostid = ?", hyper.Hostid).Take(hyper.Resource).Error
	if err != nil {
		logger.Warning("Failed to query hypervisor resource, setting defaults", err)
		hyper.Resource = &model.Resource{
			Hostid: hyper.Hostid,
		}
	}
	return
}

func (a *HyperAdmin) GetHyperByHostname(ctx context.Context, hostname string) (hyper *model.Hyper, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		logger.Error("Not authorized for this operation", err)
		return
	}
	ctx, db := GetContextDB(ctx)
	hyper = &model.Hyper{}
	if err = db.Where("hostname = ?", hostname).Take(hyper).Error; err != nil {
		logger.Error("Failed to query hypervisor", err)
		return nil, NewCLError(ErrHypervisorNotFound, "Specified hypervisor not found", err)
	}
	return
}

func (a *HyperAdmin) GetHyperByHostIP(ctx context.Context, hostIP string) (hyper *model.Hyper, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		logger.Error("Not authorized for this operation", err)
		return
	}
	ctx, db := GetContextDB(ctx)
	hyper = &model.Hyper{}
	if err = db.Where("host_ip = ?", hostIP).Take(hyper).Error; err != nil {
		logger.Error("Failed to query hypervisor", err)
		return nil, NewCLError(ErrHypervisorNotFound, "Specified hypervisor not found", err)
	}
	return
}


func (a *HyperAdmin) ValidateRouteIPSubnet(ctx context.Context, routeIP string, subnetID int64) (err error) {
	_, db := GetContextDB(ctx)
	subnet, err := subnetAdmin.Get(ctx, subnetID)
	if err != nil {
		return fmt.Errorf("Subnet not found: %v", err)
	}
	// Check the address exists in the selected subnet
	address := &model.Address{}
	err = db.Where("address = ? AND subnet_id = ? AND allocated = ?", routeIP, subnetID, false).Take(address).Error
	if err != nil {
		return fmt.Errorf("Address %s is not available in subnet %s", routeIP, subnet.Name)
	}
	return nil
}

func (a *HyperAdmin) UpdateRouteIP(ctx context.Context, hyper *model.Hyper, newRouteIP string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Find the system interface for this hyper
	iface := &model.Interface{}
	err = db.Preload("Address").Preload("Address.Subnet").Where("hyper = ? AND type = ?", hyper.Hostid, "system").Take(iface).Error
	if err != nil {
		logger.Error("Failed to find system interface for hyper", hyper.Hostid, err)
		return NewCLError(ErrInterfaceNotFound, "System interface not found for hypervisor", err)
	}

	// Validate the new address exists and is available
	address := &model.Address{}
	err = db.Preload("Subnet").Where("address = ? AND allocated = ?", newRouteIP, false).Take(address).Error
	if err != nil {
		return fmt.Errorf("Address %s is not available", newRouteIP)
	}

	// Deallocate old address
	err = db.Model(&model.Address{}).Where("id = ?", iface.AddressID).Updates(map[string]interface{}{
		"allocated": false, "interface": 0,
	}).Error
	if err != nil {
		return fmt.Errorf("Failed to release old address: %v", err)
	}

	// Allocate new address to the interface
	err = db.Model(&model.Address{}).Where("id = ?", address.ID).Updates(map[string]interface{}{
		"allocated": true, "interface": iface.ID, "type": "native",
	}).Error
	if err != nil {
		return fmt.Errorf("Failed to allocate new address: %v", err)
	}

	// Update interface's address and subnet
	err = db.Model(&model.Interface{}).Where("id = ?", iface.ID).Updates(map[string]interface{}{
		"address_id": address.ID, "subnet": address.Subnet.ID,
	}).Error
	if err != nil {
		return fmt.Errorf("Failed to update interface: %v", err)
	}

	// Update hyper's RouteIP
	err = db.Model(&model.Hyper{}).Where("id = ?", hyper.ID).Update("route_ip", newRouteIP).Error
	if err != nil {
		return fmt.Errorf("Failed to update hypervisor RouteIP: %v", err)
	}

	// Execute system_router.sh to apply the change on the hypervisor
	subnet := address.Subnet
	control := fmt.Sprintf("inter=%d", hyper.Hostid)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/system_router.sh '%d' '%s' '%s'", subnet.Vlan, newRouteIP, subnet.Gateway)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Failed to execute system_router.sh", err)
		return fmt.Errorf("Failed to apply route IP change: %v", err)
	}

	return
}

func (a *HyperAdmin) UpdateRouteIPFromSubnet(ctx context.Context, hyper *model.Hyper, subnetID int64) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Find the system interface for this hyper
	iface := &model.Interface{}
	err = db.Preload("Address").Preload("Address.Subnet").Where("hyper = ? AND type = ?", hyper.Hostid, "system").Take(iface).Error
	if err != nil {
		logger.Error("Failed to find system interface for hyper", hyper.Hostid, err)
		return NewCLError(ErrInterfaceNotFound, "System interface not found for hypervisor", err)
	}

	// Get the target subnet
	subnet, err := subnetAdmin.Get(ctx, subnetID)
	if err != nil {
		return fmt.Errorf("Subnet %d not found: %v", subnetID, err)
	}

	// Allocate a new address from the subnet
	newAddr, err := AllocateAddress(ctx, subnet, iface.ID, "", "native")
	if err != nil {
		return fmt.Errorf("Failed to allocate address from subnet %s: %v", subnet.Name, err)
	}

	// Deallocate old address
	err = db.Model(&model.Address{}).Where("id = ?", iface.AddressID).Updates(map[string]interface{}{
		"allocated": false, "interface": 0,
	}).Error
	if err != nil {
		return fmt.Errorf("Failed to release old address: %v", err)
	}

	// Update interface's address and subnet
	err = db.Model(&model.Interface{}).Where("id = ?", iface.ID).Updates(map[string]interface{}{
		"address_id": newAddr.ID, "subnet": subnet.ID,
	}).Error
	if err != nil {
		return fmt.Errorf("Failed to update interface: %v", err)
	}

	newRouteIP := newAddr.Address

	// Update hyper's RouteIP
	err = db.Model(&model.Hyper{}).Where("id = ?", hyper.ID).Update("route_ip", newRouteIP).Error
	if err != nil {
		return fmt.Errorf("Failed to update hypervisor RouteIP: %v", err)
	}

	// Execute system_router.sh to apply the change
	control := fmt.Sprintf("inter=%d", hyper.Hostid)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/system_router.sh '%d' '%s' '%s'", subnet.Vlan, newRouteIP, subnet.Gateway)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Failed to execute system_router.sh", err)
		return fmt.Errorf("Failed to apply route IP change: %v", err)
	}

	return
}

func (v *HyperView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	// Get pagination parameters
	listConfig, offset, limit := GetPaginationParams(c, "hypers")

	order := c.Query("order")
	if order == "" {
		order = "hostid"
	}
	query := c.QueryTrim("q")
	total, hypers, err := hyperAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	// transform hypers Memory from KB to MB and Disk from B to GB
	for _, hyper := range hypers {
		hyper.Resource.Memory /= 1024                  // Convert from KB to MB
		hyper.Resource.MemoryTotal /= 1024             // Convert from KB to MB
		hyper.Resource.Disk /= 1024 * 1024 * 1024      // Convert from B to GB
		hyper.Resource.DiskTotal /= 1024 * 1024 * 1024 // Convert from B to GB
	}

	c.Data["Hypers"] = hypers
	c.Data["Query"] = query
	SetPaginationData(c, "hypers", total, limit, offset, listConfig,
		`["Hostid", "Hostname", "Parentid", "CpuModel", "HostIP", "RouteIP", "Status", "Zone", "Cpu", "Memory", "Disk", "OverCommitRates", "Remark", "Action"]`,
		[]string{"Hostid", "Hostname", "Parentid", "CpuModel", "HostIP", "RouteIP", "Status", "Zone", "Cpu", "Memory", "Disk", "OverCommitRates", "Remark", "Action"})

	c.HTML(200, "hypers")
}

func (v *HyperView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	id := c.QueryInt64("host_id")
	if id < 0 {
		c.Data["ErrorMsg"] = fmt.Sprintf("Invalid host ID %d", id)
		c.HTML(400, "error")
		return
	}
	hyper, err := hyperAdmin.GetHyperByHostid(c.Req.Context(), int32(id))
	if err != nil {
		c.Data["ErrorMsg"] = fmt.Sprintf("Specified hypervisor (%d) not found, %+v", id, err)
		c.HTML(500, "error")
		return
	}

	// Load zones for the dropdown
	_, zones, err := zoneAdmin.List(c.Req.Context(), 0, 1000, "name", "")
	if err != nil {
		logger.Error("Failed to load zones", err)
		c.Data["ErrorMsg"] = "Failed to load zones"
		c.HTML(500, "error")
		return
	}

	c.Data["Hyper"] = hyper
	c.Data["Zones"] = zones

	// Load public subnets for RouteIP selection
	_, subnets, err := subnetAdmin.List(c.Req.Context(), 0, -1, "", "", "type = 'public'")
	if err != nil {
		logger.Error("Failed to load public subnets", err)
		c.Data["ErrorMsg"] = "Failed to load public subnets"
		c.HTML(500, "error")
		return
	}
	c.Data["Subnets"] = subnets

	// Load current system interface subnet
	_, db := GetContextDB(c.Req.Context())
	iface := &model.Interface{}
	err = db.Preload("Address").Preload("Address.Subnet").Where("hyper = ? AND type = ?", hyper.Hostid, "system").Take(iface).Error
	if err == nil && iface.Address != nil && iface.Address.Subnet != nil {
		c.Data["CurrentSubnetID"] = iface.Address.Subnet.ID
	}

	c.HTML(200, "hypers_patch")
}

func (v *HyperView) SetHyperStatus(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	id := c.QueryInt64("host_id")
	status := c.QueryInt("status")
	if id < 0 || (status != 0 && status != 1) {
		c.Data["ErrorMsg"] = "Invalid host ID or status"
		c.HTML(400, "error")
		return
	}
	err := hyperAdmin.SetStatus(c.Req.Context(), int32(id), int32(status))
	if err != nil {
		c.Data["ErrorMsg"] = fmt.Sprintf("Failed to set hypervisor status: %v", err)
		c.HTML(500, "error")
		return
	}
	c.Redirect("/hypers")
}

func (v *HyperView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	status := c.QueryInt("status")
	zoneID := c.QueryInt64("zone_id")
	cpuOverRate := c.QueryFloat64("cpu_over_rate")
	memOverRate := c.QueryFloat64("mem_over_rate")
	diskOverRate := c.QueryFloat64("disk_over_rate")
	remark := c.QueryTrim("remark")
	routeIP := c.QueryTrim("route_ip")
	subnetID := c.QueryInt64("subnet_id")
	if status < 0 || status > 1 {
		c.Data["ErrorMsg"] = "Invalid status value"
		c.HTML(400, "error")
		return
	}
	if cpuOverRate < 1 || memOverRate < 1 || diskOverRate < 1 {
		c.Data["ErrorMsg"] = "Over commit rates must be greater than or equal to 1"
		c.HTML(400, "error")
		return
	}
	id := c.QueryInt64("host_id")
	hyper, err := hyperAdmin.GetHyperByHostid(c.Req.Context(), int32(id))
	if err != nil {
		c.Data["ErrorMsg"] = fmt.Sprintf("Specified hypervisor (%d) not found, %+v", id, err)
		c.HTML(500, "error")
		return
	}
	zone, err := zoneAdmin.Get(c.Req.Context(), zoneID)
	if err != nil {
		c.Data["ErrorMsg"] = fmt.Sprintf("Failed to get zone(%d), %+v", zoneID, err)
		c.HTML(500, "error")
		return
	}
	hyper.ZoneID = zone.ID
	hyper.Zone = zone

	hyper.Status = int32(status)
	hyper.Remark = remark
	hyper.CpuOverRate = float32(cpuOverRate)
	hyper.MemOverRate = float32(memOverRate)
	hyper.DiskOverRate = float32(diskOverRate)
	if err := hyperAdmin.Update(c.Req.Context(), hyper); err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "error")
		return
	}
	// Handle RouteIP change
	if routeIP != hyper.RouteIP {
		if routeIP == "" {
			// RouteIP cleared, auto-assign from selected subnet
			if err := hyperAdmin.UpdateRouteIPFromSubnet(c.Req.Context(), hyper, subnetID); err != nil {
				c.Data["ErrorMsg"] = fmt.Sprintf("Failed to allocate RouteIP from subnet: %v", err)
				c.HTML(500, "error")
				return
			}
		} else {
			// Validate the specified RouteIP belongs to the selected subnet
			if err := hyperAdmin.ValidateRouteIPSubnet(c.Req.Context(), routeIP, subnetID); err != nil {
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			if err := hyperAdmin.UpdateRouteIP(c.Req.Context(), hyper, routeIP); err != nil {
				c.Data["ErrorMsg"] = fmt.Sprintf("Failed to update RouteIP: %v", err)
				c.HTML(500, "error")
				return
			}
		}
	}
	c.Redirect("/hypers")
}

