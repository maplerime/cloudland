/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"github.com/google/uuid"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"
)

var (
	interfaceAdmin = &InterfaceAdmin{}
	interfaceView  = &InterfaceView{}
)

type InterfaceInfo struct {
	PublicIps      []*model.FloatingIp
	Subnets        []*model.Subnet
	MacAddress     string
	IpAddress      string
	Count          int
	SiteSubnets    []*model.Subnet
	Inbound        int32
	Outbound       int32
	AllowSpoofing  bool
	SecurityGroups []*model.SecurityGroup
}

type InterfaceAdmin struct{}

type InterfaceView struct{}

func (a *InterfaceAdmin) Get(ctx context.Context, id int64) (iface *model.Interface, err error) {
	if id <= 0 {
		err = NewCLError(ErrInvalidParameter, "Invalid interface ID", nil)
		logger.Error(err)
		return
	}
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	iface = &model.Interface{Model: model.Model{ID: id}}
	err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.updated_at")
	}).Preload("SecondAddresses.Subnet").Take(iface).Error
	if err != nil {
		logger.Debug("DB failed to query interface, %v", err)
		err = NewCLError(ErrInterfaceNotFound, "Interface not found", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, iface.Owner)
	if !permit {
		logger.Debug("Not authorized to read the interface")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the interface", nil)
		return
	}
	return
}

func (a *InterfaceAdmin) GetInterfaceByUUID(ctx context.Context, uuID string) (iface *model.Interface, err error) {
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	ctx, db := GetContextDB(ctx)
	iface = &model.Interface{}
	err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.updated_at")
	}).Preload("SecondAddresses.Subnet").Where(where).Where("uuid = ?", uuID).Take(iface).Error
	if err != nil {
		logger.Debug("DB failed to query interface, %v", err)
		err = NewCLError(ErrInterfaceNotFound, "Interface not found", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, iface.Owner)
	if !permit {
		logger.Debug("Not authorized to read the subnet")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the interface", nil)
		return
	}
	return
}

func (a *InterfaceAdmin) Delete(ctx context.Context, instance *model.Instance, iface *model.Interface) (err error) {
	if iface.PrimaryIf {
		err = NewCLError(ErrCannotDeletePrimaryInterface, "Primary interface can not be deleted", nil)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, iface.Owner)
	if !permit {
		logger.Error("Not authorized to delete the interface")
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete the interface", nil)
		return
	}
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/detach_vm_nic.sh '%d' '%d' '%d' '%s' '%s'", instance.ID, iface.ID, iface.Address.Subnet.Vlan, iface.Address.Address, iface.MacAddr)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Detach vm nic command execution failed", err)
		return
	}
	return
}

func (a *InterfaceAdmin) List(ctx context.Context, offset, limit int64, order string, instance *model.Instance) (total int64, interfaces []*model.Interface, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Reader, instance.Owner)
	if !permit {
		logger.Debug("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	where := fmt.Sprintf("instance = %d", instance.ID)
	wm := memberShip.GetWhere()
	if wm != "" {
		where = fmt.Sprintf("%s and %s", where, wm)
	}
	interfaces = []*model.Interface{}
	if err = db.Model(&model.Interface{}).Where(where).Count(&total).Error; err != nil {
		logger.Debug("DB failed to count security rule(s), %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to count interfaces", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.updated_at")
	}).Preload("SecondAddresses.Subnet").Where(where).Find(&interfaces).Error; err != nil {
		logger.Debug("DB failed to query interface(s), %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query interfaces", err)
		return
	}

	return
}

func (a *InterfaceAdmin) checkAddresses(ctx context.Context, iface *model.Interface, ifaceSubnets, siteSubnets []*model.Subnet, secondAddrsCount int, publicIps []*model.FloatingIp) (valid, changed bool) {
	vlan := iface.Address.Subnet.Vlan
	publicIpsLength := len(publicIps)
	secondIpsLength := len(iface.SecondAddresses)
	if publicIpsLength > 0 {
		if publicIpsLength != secondIpsLength+1 {
			changed = true
		}
		for i, pubIp := range publicIps {
			if vlan != pubIp.Interface.Address.Subnet.Vlan {
				changed = true
				return
			}
			if i == 0 {
				if pubIp.FipAddress != iface.Address.Address {
					changed = true
					logger.Errorf("pubIp.FipAddress: %s, iface.Address.Address: %s, %d", pubIp.FipAddress, iface.Address.Address, i)
					return
				}
			} else {
				if (i - 1) < secondIpsLength {
					secondAddr := iface.SecondAddresses[i-1].Address
					if pubIp.FipAddress != secondAddr {
						changed = true
						logger.Errorf("pubIp.FipAddress: %s, iface.Address.Address: %s, %d", pubIp.FipAddress, secondAddr, i)
						return
					}
				}
			}
		}
	} else {
		if secondAddrsCount != secondIpsLength {
			changed = true
		}
		for _, subnet := range ifaceSubnets {
			if vlan != subnet.Vlan {
				changed = true
				return
			}
		}
	}
	if len(siteSubnets) != len(iface.SiteSubnets) {
		changed = true
	}
	for _, site := range siteSubnets {
		if vlan != site.Vlan {
			changed = true
			return
		}
		found := false
		for _, ifaceSite := range iface.SiteSubnets {
			if site.ID == ifaceSite.ID {
				found = true
				break
			}
		}
		if !found {
			changed = true
			break
		}
	}
	valid = true

	return
}

func (a *InterfaceAdmin) allocateSecondAddresses(ctx context.Context, instance *model.Instance, iface *model.Interface, ifaceSubnets []*model.Subnet, secondAddrsCount int) (err error) {
	cnt := 0
	for _, subnet := range ifaceSubnets {
		for i := 0; i < secondAddrsCount; i++ {
			var addr *model.Address
			addr, err = AllocateAddress(ctx, subnet, iface.ID, "", "second")
			if err == nil {
				iface.SecondAddresses = append(iface.SecondAddresses, addr)
				if subnet.Type == string(Public) {
					_, err = floatingIpAdmin.createDummyFloatingIp(ctx, instance, addr.Address)
					if err != nil {
						logger.Error("DB failed to create dummy floating ip", err)
						return
					}
				}
				cnt++
				if cnt >= secondAddrsCount {
					return
				}
			} else {
				logger.Errorf("Allocate address interface from subnet %s--%s/%s failed, %v", subnet.Name, subnet.Network, subnet.Netmask, err)
			}
		}
	}
	if cnt < secondAddrsCount {
		err = NewCLError(ErrInsufficientAddress, fmt.Sprintf("Only %d addresses can be allocated", cnt), nil)
		return
	}
	return
}

func (a *InterfaceAdmin) changeAddresses(ctx context.Context, instance *model.Instance, iface *model.Interface, ifaceSubnets, siteSubnets []*model.Subnet, secondAddrsCount int, publicIps []*model.FloatingIp, secgroups []*model.SecurityGroup) (iface2 *model.Interface, err error) {
	ctx, db := GetContextDB(ctx)
	for _, site := range iface.SiteSubnets {
		err = db.Model(site).Updates(map[string]interface{}{"interface": 0}).Error
		if err != nil {
			logger.Error("Failed to update site subnets", err)
			err = NewCLError(ErrSiteSubnetUpdateFailed, "Failed to update site subnets", err)
			return
		}
	}
	iface.SiteSubnets = nil

	if len(publicIps) > 0 {
		primaryMac := iface.MacAddr
		primaryUUID := iface.UUID
		if iface.FloatingIp != publicIps[0].ID {
			var floatingIp *model.FloatingIp
			floatingIp, err = floatingIpAdmin.Get(ctx, iface.FloatingIp)
			if err != nil {
				logger.Errorf("Failed to get floating ip, %v", err)
				return
			}
			err = floatingIpAdmin.Detach(ctx, floatingIp)
			if err != nil {
				logger.Errorf("Failed to detach floating ip, %v", err)
				return
			}
			if err = db.Model(iface).Association("Security_Groups").Replace([]*model.SecurityGroup{}).Error; err != nil {
				logger.Debug("Failed to save interface", err)
				return
			}
			mac := ""
			mac, err = GenerateMacaddr()
			if err != nil {
				logger.Error("Failed to generate random Mac address, %v", err)
				return
			}
			err = db.Model(iface).Update(map[string]interface{}{"instance": 0, "uuid": uuid.New().String(), "primary_if": false, "name": "fip", "inbound": 0, "outbound": 0, "allow_spoofing": false, "mac_addr": mac}).Error
			if err != nil {
				logger.Error("Failed to Update addresses, %v", err)
				return
			}
			iface = nil
		}
		iface, _, err = DerivePublicInterface(ctx, instance, iface, publicIps, primaryMac, primaryUUID)
		if err != nil {
			logger.Error("Failed to derive primary interface", err)
			return
		}
		if len(secgroups) > 0 {
			if err = db.Model(iface).Association("Security_Groups").Replace(secgroups).Error; err != nil {
				logger.Debug("Failed to save interface", err)
				return
			}
			iface.SecurityGroups = secgroups
		}
	} else {
		cnt := secondAddrsCount - len(iface.SecondAddresses)
		if cnt > 0 {
			err = a.allocateSecondAddresses(ctx, instance, iface, ifaceSubnets, cnt)
			if err != nil {
				return
			}
		} else if cnt < 0 {
			for i := 0; i < -cnt; i++ {
				err = db.Model(&iface.SecondAddresses[i]).Updates(map[string]interface{}{"second_interface": 0, "allocated": false}).Error
				if err != nil {
					logger.Errorf("Failed to update second address of interface %d, %+v", iface.ID, err)
					err = NewCLError(ErrAddressUpdateFailed, "Failed to update second address of interface", err)
					return
				}
			}
		}
	}
	for _, site := range siteSubnets {
		err = db.Model(site).Updates(map[string]interface{}{"interface": iface.ID}).Error
		if err != nil {
			logger.Error("Failed to update interface", err)
			err = NewCLError(ErrSiteSubnetUpdateFailed, "Failed to update interface", err)
			return
		}
		iface.SiteSubnets = append(iface.SiteSubnets, site)
	}
	iface.SecondAddresses = nil
	err = db.Preload("Subnet").Where("second_interface = ?", iface.ID).Find(&iface.SecondAddresses).Error
	if err != nil {
		logger.Error("Second addresses query failed", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query second addresses of interface", err)
		return
	}
	iface2 = iface

	return
}

func (a *InterfaceAdmin) checkSubnets(ctx context.Context, subnets []*model.Subnet, vlan int64) (err error) {
	if len(subnets) == 0 {
		err = fmt.Errorf("At least one subnet must be specified")
		return
	}
	for _, subnet := range subnets {
		if vlan == 0 {
			vlan = subnet.Vlan
		} else if vlan != subnet.Vlan {
			err = fmt.Errorf("Subnets are not all in the same vlan")
			return
		}
	}
	return
}

func (a *InterfaceAdmin) CheckIfaceSubnets(ctx context.Context, primaryIface *InterfaceInfo, secondaryIfaces []*InterfaceInfo) (err error) {
	checkVlan := int64(0)
	if len(primaryIface.Subnets) > 0 {
		err = a.checkSubnets(ctx, primaryIface.Subnets, 0)
		if err != nil {
			logger.Error("Failed to check primary subnets", err)
			return
		}
		checkVlan = primaryIface.Subnets[0].Vlan
	}
	if len(primaryIface.SiteSubnets) > 0 {
		err = a.checkSubnets(ctx, primaryIface.SiteSubnets, checkVlan)
		if err != nil {
			logger.Error("Failed to check site subnets", err)
			return
		}
	}
	for _, iface := range secondaryIfaces {
		err = a.checkSubnets(ctx, iface.Subnets, 0)
		if err != nil {
			logger.Error("Failed to check site subnets", err)
			return
		}
		if iface.Subnets[0].Vlan == checkVlan {
			err = fmt.Errorf("Second interfaces can not use same vlan with primary")
			return
		}
	}
	for i, iface := range secondaryIfaces {
		checkVlan = iface.Subnets[0].Vlan
		for _, rest := range secondaryIfaces[i+1:] {
			if rest.Subnets[0].Vlan == checkVlan {
				err = fmt.Errorf("Different interfaces can not use same vlan")
				return
			}
		}
	}
	return
}

func (a *InterfaceAdmin) Create(ctx context.Context, instance *model.Instance, address, mac string, inbound, outbound int32, allowSpoofing bool, secgroups []*model.SecurityGroup, subnets []*model.Subnet, secondAddrsCount int) (iface *model.Interface, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	ifaceLen := len(instance.Interfaces)
	if ifaceLen >= 8 {
		err = NewCLError(ErrTooManyInterfaces, "Can not create interfaces more than 8", nil)
		return
	}
	routerID := instance.RouterID
	ifname := fmt.Sprintf("eth%d", ifaceLen)
	err = a.checkSubnets(ctx, subnets, 0)
	if err != nil {
		logger.Error("Failed to check subnets", err)
		return
	}
	for _, instIface := range instance.Interfaces {
		if instIface.Address.Subnet.Vlan == subnets[0].Vlan {
			logger.Error("New interface can not use the same vlan of existing interfaces")
			err = NewCLError(ErrInterfaceInvalidSubnet, "Invalid or duplicate subnets for interfaces", nil)
			return
		}
	}
	for _, subnet := range subnets {
		if subnet.Type == "site" {
			logger.Error("Not allowed to create interface in site subnet")
			err = NewCLError(ErrNotAllowInterfaceInSiteSubnet, "Not allowed to create interface in site subnet", nil)
			return
		}
		if routerID > 0 && subnet.RouterID != routerID {
			logger.Error("Subnets can not belong to different router")
			err = NewCLError(ErrSubnetsCrossVPCInOneInstance, "Subnets can not belong to different router", nil)
			return
		}
		if iface == nil {
			iface, err = CreateInterface(ctx, subnet, instance.ID, memberShip.OrgID, instance.Hyper, inbound, outbound, address, mac, ifname, "instance", secgroups, allowSpoofing)
			if err == nil {
				if subnet.Type == "public" {
					_, err = floatingIpAdmin.createDummyFloatingIp(ctx, instance, iface.Address.Address)
					if err != nil {
						logger.Error("DB failed to create dummy floating ip", err)
						return
					}
				}
				break
			} else {
				logger.Errorf("Allocate address interface from subnet %s--%s/%s failed, %v", subnet.Name, subnet.Network, subnet.Netmask, err)
			}
		}
	}
	if iface == nil {
		if err == nil {
			err = NewCLError(ErrInterfaceCreateFailed, "Failed to create interface", nil)
		}
		return
	}
	if routerID == 0 {
		instance.RouterID = iface.Address.Subnet.RouterID
		err = db.Model(&model.Instance{Model: model.Model{ID: int64(instance.ID)}}).Update(map[string]interface{}{
			"router_id": instance.RouterID}).Error
		if err != nil {
			logger.Debug("Failed to update instance", err)
			err = NewCLError(ErrInstanceUpdateFailed, "Failed to update instance", err)
			return
		}
	}
	err = ApplyInterface(ctx, instance, iface, false)
	if err != nil {
		return
	}
	return
}

func (a *InterfaceAdmin) Update(ctx context.Context, instance *model.Instance, iface *model.Interface, name string, inbound, outbound int32, allowSpoofing bool, secgroups []*model.SecurityGroup, ifaceSubnets []*model.Subnet, siteSubnets []*model.Subnet, secondAddrsCount int, publicIps []*model.FloatingIp) (iface2 *model.Interface, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	needUpdate := false
	needRemoteUpdate := false
	if iface.Name != name {
		iface.Name = name
		needUpdate = true
	}
	if iface.Inbound != inbound {
		iface.Inbound = inbound
		needUpdate = true
		needRemoteUpdate = true
	}
	if iface.Outbound != outbound {
		iface.Outbound = outbound
		needUpdate = true
		needRemoteUpdate = true
	}
	if iface.AllowSpoofing != allowSpoofing {
		iface.AllowSpoofing = allowSpoofing
		needUpdate = true
		needRemoteUpdate = true
	}
	if len(secgroups) > 0 {
		if err = db.Model(iface).Association("Security_Groups").Replace(secgroups).Error; err != nil {
			logger.Debug("Failed to save interface", err)
			err = NewCLError(ErrInterfaceUpdateFailed, "Failed to update interface security groups", err)
			return
		}
		iface.SecurityGroups = secgroups
		needRemoteUpdate = true
	} else {
		err = NewCLError(ErrAtLeastOneSGRequired, "At least one security group is needed", nil)
		return
	}
	if needUpdate || needRemoteUpdate {
		err = db.Model(&model.Interface{Model: model.Model{ID: int64(iface.ID)}}).Update(map[string]interface{}{
			"inbound":        iface.Inbound,
			"outbound":       iface.Outbound,
			"allow_spoofing": iface.AllowSpoofing,
			"name":           iface.Name}).Error
		if err != nil {
			logger.Debug("Failed to save interface", err)
			err = NewCLError(ErrInterfaceUpdateFailed, "Failed to update interface", err)
			return
		}
	}
	changed := false
	if iface.PrimaryIf && iface.Address.Subnet.RouterID == 0 {
		// valid := true
		_, changed = a.checkAddresses(ctx, iface, ifaceSubnets, siteSubnets, secondAddrsCount, publicIps)
		// if !valid {
		// 	logger.Errorf("Failed to check addresses, %v", err)
		// 	err = fmt.Errorf("Failed to check addresses")
		// 	return
		// }

		if changed {
			var oldAddresses []string
			_, oldAddresses, err = GetInstanceNetworks(ctx, instance, []*model.Interface{iface})
			if err != nil {
				logger.Errorf("Failed to get instance networks, %v", err)
				return
			}
			var oldAddrsJson []byte
			oldAddrsJson, err = json.Marshal(oldAddresses)
			if err != nil {
				logger.Errorf("Failed to marshal instance json data, %v", err)
				err = NewCLError(ErrJSONMarshalFailed, "Failed to marshal instance json data", err)
				return
			}
			// 1. Get old addresses 2. Change addresses 3. Remote execute
			iface, err = a.changeAddresses(ctx, instance, iface, ifaceSubnets, siteSubnets, secondAddrsCount, publicIps, secgroups)
			if err != nil {
				logger.Errorf("Failed to get instance networks, %v", err)
				return
			}
			osCode := GetImageOSCode(ctx, instance)
			if osCode == "windows" {
				control := fmt.Sprintf("inter=%d", instance.Hyper)
				command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_second_ips.sh '%d' '%s' '%s'<<EOF\n%s\nEOF", instance.ID, iface.MacAddr, GetImageOSCode(ctx, instance), oldAddrsJson)
				err = HyperExecute(ctx, control, command)
				if err != nil {
					logger.Error("clear_second_ips command execution failed", err)
					return
				}
			}
		}

	}
	if needRemoteUpdate || changed {
		err = ApplyInterface(ctx, instance, iface, changed)
		if err != nil {
			logger.Error("Update vm nic command execution failed", err)
			return
		}
	}
	iface2 = iface
	return
}

func (v *InterfaceView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	instID := c.Params("instid")
	if instID == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(instID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		logger.Error("Instance ID error ", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err = instanceAdmin.Get(ctx, int64(instanceID))
	if err != nil {
		logger.Error("Instance query failed", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Instance query failed", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	interfaceID := c.Params("id")
	if interfaceID == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ifaceID, err := strconv.Atoi(interfaceID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	iface, err := interfaceAdmin.Get(ctx, int64(ifaceID))
	if err != nil {
		logger.Error("Interface query failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ifaceSubnets := []*model.Subnet{iface.Address.Subnet}
	for _, secondAddr := range iface.SecondAddresses {
		ifaceSubnets = append(ifaceSubnets, secondAddr.Subnet)
	}
	var subnets []*model.Subnet
	_, subnets, err = subnetAdmin.List(c.Req.Context(), 0, -1, "", "", fmt.Sprintf("vlan = %d and type != 'site'", iface.Address.Subnet.Vlan))
	if err != nil {
		logger.Error("Subnets query failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	var siteSubnets []*model.Subnet
	_, siteSubnets, err = subnetAdmin.List(c.Req.Context(), 0, -1, "", "", fmt.Sprintf("type = 'site' and (interface = 0 or interface = %d)", iface.ID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	_, secgroups, err := secgroupAdmin.List(c.Req.Context(), 0, -1, "", fmt.Sprintf("router_id = %d", iface.SecurityGroups[0].RouterID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	_, floatingIps, err := floatingIpAdmin.List(c.Req.Context(), 0, -1, "updated_at", "", fmt.Sprintf("instance_id = 0 or instance_id = %d", iface.Instance))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["Interface"] = iface
	c.Data["Secgroups"] = secgroups
	c.Data["PublicIps"] = floatingIps
	c.Data["IfaceSubnets"] = ifaceSubnets
	c.Data["IpCount"] = len(iface.SecondAddresses) + 1
	c.Data["Subnets"] = subnets
	c.Data["IfaceSubnets"] = ifaceSubnets
	c.Data["SiteSubnets"] = siteSubnets
	c.Data["IfaceSecgroups"] = iface.SecurityGroups
	c.Data["IfaceSites"] = iface.SiteSubnets
	c.HTML(200, "interfaces_patch")
}

func (v *InterfaceView) New(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		logger.Error("Instance ID error ", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance, err := instanceAdmin.Get(ctx, int64(instanceID))
	if err != nil {
		logger.Error("Instance query failed", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Instance query failed", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	where := "interface = 0"
	if instance.RouterID > 0 {
		where = fmt.Sprintf("%s and router_id = %d", where, instance.RouterID)
	}
	_, subnets, err := subnetAdmin.List(ctx, 0, -1, "", "", where)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	_, secgroups, err := secgroupAdmin.List(ctx, 0, -1, "", "")
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["Subnets"] = subnets
	c.Data["SecurityGroups"] = secgroups
	c.HTML(200, "interfaces_new")
}

func (v *InterfaceView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instanceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		logger.Error("Instance ID error ", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance, err := instanceAdmin.Get(ctx, int64(instanceID))
	if err != nil {
		logger.Error("Instance query failed", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Instance query failed", err)
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	subnets := c.QueryStrings("subnets")
	ifaceSubnets := []*model.Subnet{}
	if len(subnets) > 0 {
		for _, subnet := range subnets {
			subnetID, err := strconv.Atoi(subnet)
			if err != nil {
				logger.Debug("Invalid site subnet ID, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			ifaceSubnet, err := subnetAdmin.Get(ctx, int64(subnetID))
			if err != nil {
				logger.Debug("Failed to query interface subnet, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			ifaceSubnets = append(ifaceSubnets, ifaceSubnet)
		}
	}
	if len(ifaceSubnets) == 0 {
		logger.Debug("No valid subnet")
		err = fmt.Errorf("No valid subnet")
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	address := c.QueryTrim("address")
	mac := c.QueryTrim("mac")
	inbound := c.QueryInt("inbound")
	outbound := c.QueryInt("outbound")
	if inbound > 20000 || inbound < 0 {
		logger.Errorf("Inbound out of range %d", inbound)
		c.Data["ErrorMsg"] = "Invalid inbound range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if outbound > 20000 || outbound < 0 {
		logger.Errorf("Outbound out of range %d", outbound)
		c.Data["ErrorMsg"] = "Inbound out of range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	allowSpoofing := false
	allowSpf := c.QueryTrim("allow_spoofing")
	if allowSpf == "yes" {
		allowSpoofing = true
	}

	sgs := c.QueryStrings("secgroups")
	logger.Error("security groups: ", sgs)
	secgroups, err := instanceView.getSecurityGroups(ctx, ifaceSubnets[0].RouterID, sgs)
	if err != nil {
		logger.Debug("Failed to get security groups", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
	}
	_, err = interfaceAdmin.Create(ctx, instance, address, mac, int32(inbound), int32(outbound), allowSpoofing, secgroups, ifaceSubnets, 0)
	if err != nil {
		logger.Debug("Failed to update interface", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
	}
	redirectTo := "../interfaces"
	c.Redirect(redirectTo)
}

func (v *InterfaceView) List(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "created_at"
	}
	instid := c.Params("instid")
	if instid == "" {
		logger.Error("Instance ID is empty")
		c.Data["ErrorMsg"] = "Instance ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instID, err := strconv.Atoi(instid)
	if err != nil {
		logger.Error("Invalid instance ID", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance, err := instanceAdmin.Get(ctx, int64(instID))
	if err != nil {
		logger.Error("Failed to get instance", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	total, interfaces, err := interfaceAdmin.List(ctx, offset, limit, order, instance)
	if err != nil {
		logger.Error("Failed to list interface(s)", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Instance"] = instance
	c.Data["Interfaces"] = interfaces
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.HTML(200, "interfaces")
}

func (v *InterfaceView) Delete(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	instID := c.ParamsInt64("instid")
	instance, err := instanceAdmin.Get(ctx, instID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	id := c.ParamsInt64("id")
	iface, err := interfaceAdmin.Get(ctx, id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = interfaceAdmin.Delete(ctx, instance, iface)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "interfaces",
	})
}

func (v *InterfaceView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../interfaces"
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ifaceID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	iface, err := interfaceAdmin.Get(ctx, int64(ifaceID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instance, err := instanceAdmin.Get(ctx, iface.Instance)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	inbound := c.QueryInt("inbound")
	outbound := c.QueryInt("outbound")
	if inbound > 20000 || inbound < 0 {
		logger.Errorf("Inbound out of range %d", inbound)
		c.Data["ErrorMsg"] = "Invalid inbound range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if outbound > 20000 || outbound < 0 {
		logger.Errorf("Outbound out of range %d", outbound)
		c.Data["ErrorMsg"] = "Inbound out of range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	allowSpoofing := false
	allowSpf := c.QueryTrim("allow_spoofing")
	if allowSpf == "yes" {
		allowSpoofing = true
	}

	sgs := c.QueryStrings("secgroups")
	logger.Error("security groups: ", sgs)
	secgroups := []*model.SecurityGroup{}
	if len(sgs) > 0 {
		for _, sg := range sgs {
			sgID, err := strconv.Atoi(sg)
			if err != nil {
				logger.Debug("Invalid security group ID, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			secgroup, err := secgroupAdmin.Get(ctx, int64(sgID))
			if err != nil {
				logger.Debug("Failed to query security group, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			secgroups = append(secgroups, secgroup)
		}
	}
	var publicAddresses []*model.FloatingIp
	publicIps := c.QueryStrings("public_ips")
	logger.Error("public ips: ", publicIps)
	if len(publicIps) > 0 {
		for _, pubIp := range publicIps {
			fID, err := strconv.Atoi(pubIp)
			if err != nil {
				logger.Error("Invalid public ip ID", err)
				continue
			}
			var floatingIp *model.FloatingIp
			floatingIp, err = floatingIpAdmin.Get(ctx, int64(fID))
			if err != nil {
				logger.Error("Get public ip failed", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			publicAddresses = append(publicAddresses, floatingIp)
		}
	}
	logger.Errorf("public addresses: ", publicAddresses)
	subnets := c.QueryStrings("subnets")
	ifaceSubnets := []*model.Subnet{}
	if len(subnets) > 0 {
		for _, subnet := range subnets {
			subnetID, err := strconv.Atoi(subnet)
			if err != nil {
				logger.Debug("Invalid site subnet ID, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			ifaceSubnet, err := subnetAdmin.Get(ctx, int64(subnetID))
			if err != nil {
				logger.Debug("Failed to query interface subnet, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			ifaceSubnets = append(ifaceSubnets, ifaceSubnet)
		}
	}
	cnt := c.QueryTrim("ip_count")
	ipCount, err := strconv.Atoi(cnt)
	if err != nil {
		logger.Error("Invalid ip count", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ipCount -= 1
	if ipCount < 0 {
		logger.Error("Invalid ip count", err)
		c.Data["ErrorMsg"] = "IP count must >= 1"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	sites := c.QueryStrings("sites")
	siteSubnets := []*model.Subnet{}
	if len(sites) > 0 {
		for _, site := range sites {
			siteID, err := strconv.Atoi(site)
			if err != nil {
				logger.Debug("Invalid site subnet ID, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			siteSubnet, err := subnetAdmin.Get(ctx, int64(siteID))
			if err != nil {
				logger.Debug("Failed to query site subnet, %v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			siteSubnets = append(siteSubnets, siteSubnet)
		}
	}
	PrimaryFloating := c.QueryInt64("primary_floating")
	if PrimaryFloating > 0 && PrimaryFloating != iface.FloatingIp {
		primaryFip, err := floatingIpAdmin.Get(ctx, int64(PrimaryFloating))
		if err != nil {
			logger.Error("Get primary public ip failed", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		if iface.Address.Subnet.Vlan != primaryFip.Subnet.Vlan {
			logger.Error("New primary ip is not allowed to be in different vlan")
			c.Data["ErrorMsg"] = "New primary ip is not allowed to be in different vlan"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		for i, pubAddr := range publicAddresses {
			if PrimaryFloating == pubAddr.ID {
				publicAddresses = append(publicAddresses[:i], publicAddresses[i+1:]...)
				break
			}
		}
		publicAddresses = append([]*model.FloatingIp{primaryFip}, publicAddresses...)
	}
	_, err = interfaceAdmin.Update(ctx, instance, iface, name, int32(inbound), int32(outbound), allowSpoofing, secgroups, ifaceSubnets, siteSubnets, ipCount, publicAddresses)
	if err != nil {
		logger.Debug("Failed to update interface", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
	}
	c.Redirect(redirectTo)
}
