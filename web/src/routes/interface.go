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
	"strings"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"
)

var (
	interfaceAdmin = &InterfaceAdmin{}
	interfaceView  = &InterfaceView{}
)

type InterfaceInfo struct {
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
		err = fmt.Errorf("Invalid interface ID: %d", id)
		logger.Debug(err)
		return
	}
	memberShip := GetMemberShip(ctx)
	db := DB()
	iface = &model.Interface{Model: model.Model{ID: id}}
	err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
		return db.Order("addresses.created_at DESC")
	}).Preload("SecondAddresses.Subnet").Take(iface).Error
	if err != nil {
		logger.Debug("DB failed to query interface, %v", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, iface.Owner)
	if !permit {
		logger.Debug("Not authorized to read the subnet")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *InterfaceAdmin) GetInterfaceByUUID(ctx context.Context, uuID string) (iface *model.Interface, err error) {
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	db := DB()
	iface = &model.Interface{}
	err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
                return db.Order("addresses.created_at DESC")
        }).Preload("SecondAddresses.Subnet").Where(where).Where("uuid = ?", uuID).Take(iface).Error
	if err != nil {
		logger.Debug("DB failed to query interface, %v", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, iface.Owner)
	if !permit {
		logger.Debug("Not authorized to read the subnet")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *InterfaceAdmin) List(ctx context.Context, offset, limit int64, order string, instance *model.Instance) (total int64, interfaces []*model.Interface, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Reader, instance.Owner)
	if !permit {
		logger.Debug("Not authorized for this operation")
		err = fmt.Errorf("Not authorized")
		return
	}
	db := DB()
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
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("SiteSubnets").Preload("SecurityGroups").Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses", func(db *gorm.DB) *gorm.DB {
                return db.Order("addresses.created_at DESC")
        }).Preload("SecondAddresses.Subnet").Where(where).Find(&interfaces).Error; err != nil {
		logger.Debug("DB failed to query security rule(s), %v", err)
		return
	}

	return
}

func (a *InterfaceAdmin) checkAddressesChange(ctx context.Context, iface *model.Interface, siteSubnets []*model.Subnet, secondAddrsCount int) (changed bool) {
	if siteSubnets == nil && secondAddrsCount == len(iface.SecondAddresses) {
		return false
	}

	if (len(iface.SiteSubnets) != len(siteSubnets)) || (len(iface.SecondAddresses) != secondAddrsCount) {
		return true
	}

	for _, ifaceSite := range iface.SiteSubnets {
		found := false
		for _, site := range siteSubnets {
			if ifaceSite.ID == site.ID {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}

	return false
}

func (a *InterfaceAdmin) allocateSecondAddresses(ctx context.Context, iface *model.Interface, ifaceSubnets []*model.Subnet, secondAddrsCount int) (err error) {
	cnt := 0
	for _, subnet := range ifaceSubnets {
		for i := 0; i < secondAddrsCount; i++ {
			var addr *model.Address
			addr, err = AllocateAddress(ctx, subnet, iface.ID, "", "second")
			if err == nil {
				iface.SecondAddresses = append(iface.SecondAddresses, addr)
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
		err = fmt.Errorf("Only %d addresses can be allocated", cnt)
		return
	}
	return
}

func (a *InterfaceAdmin) changeAddresses(ctx context.Context, iface *model.Interface, ifaceSubnets, siteSubnets []*model.Subnet, secondAddrsCount int) (err error) {
	ctx, db := GetContextDB(ctx)
	for _, site := range iface.SiteSubnets {
		err = db.Model(site).Updates(map[string]interface{}{"interface": 0}).Error
		if err != nil {
			logger.Error("Failed to update site subnets", err)
			return
		}
	}
	iface.SiteSubnets = nil
	for _, site := range siteSubnets {
		err = db.Model(site).Updates(map[string]interface{}{"interface": iface.ID}).Error
		if err != nil {
			logger.Error("Failed to update interface", err)
			return
		}
		iface.SiteSubnets = append(iface.SiteSubnets, site)
	}

	cnt := secondAddrsCount - len(iface.SecondAddresses)
	if cnt > 0 {
		err = a.allocateSecondAddresses(ctx, iface, ifaceSubnets, cnt)
		if err != nil {
			return
		}
	} else if cnt < 0 {
		for i := 0; i < -cnt; i++ {
			err = db.Model(&iface.SecondAddresses[i]).Updates(map[string]interface{}{"second_interface": 0, "allocated": false}).Error
			if err != nil {
				logger.Error("Update interface ", err)
				return
			}
		}
	}
	iface.SecondAddresses = nil
	err = db.Where("second_interface = ?", iface.ID).Find(&iface.SecondAddresses).Error
	if err != nil {
		logger.Error("Second addresses query failed", err)
		return
	}

	return
}

func (a *InterfaceAdmin) Update(ctx context.Context, instance *model.Instance, iface *model.Interface, name string, inbound, outbound int32, allowSpoofing bool, secgroups []*model.SecurityGroup, ifaceSubnets []*model.Subnet, siteSubnets []*model.Subnet, secondAddrsCount int) (err error) {
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
			return
		}
		iface.SecurityGroups = secgroups
		needRemoteUpdate = true
	} else {
		err = fmt.Errorf("At least one security group is needed")
		return
	}
	if iface.PrimaryIf {
		changed := a.checkAddressesChange(ctx, iface, siteSubnets, secondAddrsCount)
		if changed {
			var oldAddresses []string
			_, oldAddresses, err = GetInstanceNetworks(ctx, instance, iface, 0)
			if err != nil {
				logger.Errorf("Failed to get instance networks, %v", err)
				return
			}
			var oldAddrsJson []byte
			oldAddrsJson, err = json.Marshal(oldAddresses)
			if err != nil {
				logger.Errorf("Failed to marshal instance json data, %v", err)
				return
			}
			control := fmt.Sprintf("inter=%d", instance.Hyper)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_second_ips.sh '%d' '%s' '%s'<<EOF\n%s\nEOF", instance.ID, iface.MacAddr, GetImageOSCode(ctx, instance), oldAddrsJson)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Update vm nic command execution failed", err)
				return
			}
			err = a.changeAddresses(ctx, iface, ifaceSubnets, siteSubnets, secondAddrsCount)
			if err != nil {
				logger.Errorf("Failed to get instance networks, %v", err)
				return
			}
		}
	}
	if needUpdate || needRemoteUpdate {
		if err = db.Model(iface).Save(iface).Error; err != nil {
			logger.Debug("Failed to save interface", err)
			return
		}
	}
	if needRemoteUpdate {
		err = ApplyInterface(ctx, instance, iface)
		if err != nil {
			logger.Error("Update vm nic command execution failed", err)
			return
		}
	}
	return
}

func (v *InterfaceView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	db := DB()
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
	permit, err := memberShip.CheckOwner(model.Writer, "interfaces", int64(ifaceID))
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	iface := &model.Interface{Model: model.Model{ID: int64(ifaceID)}}
	err = db.Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("SiteSubnets").Preload("SecurityGroups").Take(iface).Error
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
	c.Data["Interface"] = iface
	c.Data["Secgroups"] = secgroups
	c.Data["IfaceSubnets"] = ifaceSubnets
	c.Data["IpCount"] = len(iface.SecondAddresses) + 1
	c.Data["Subnets"] = subnets
	c.Data["IfaceSubnets"] = ifaceSubnets
	c.Data["SiteSubnets"] = siteSubnets
	c.Data["IfaceSecgroups"] = iface.SecurityGroups
	c.Data["IfaceSites"] = iface.SiteSubnets
	c.HTML(200, "interfaces_patch")
}

func (v *InterfaceView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	subnetID := c.QueryInt64("subnet")
	subnet, err := subnetAdmin.Get(ctx, subnetID)
	if err != nil {
		logger.Error("Get subnet failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	instID := c.QueryInt64("instance")
	if instID > 0 {
		permit, _ := memberShip.CheckOwner(model.Writer, "instances", int64(instID))
		if !permit {
			logger.Error("Not authorized to access instance")
			c.Data["ErrorMsg"] = "Not authorized to access instance"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}
	address := c.QueryTrim("address")
	mac := c.QueryTrim("mac")
	ifname := c.QueryTrim("ifname")
	sgList := c.QueryTrim("secgroups")
	var sgIDs []int64
	if sgList != "" {
		sg := strings.Split(sgList, ",")
		for i := 0; i < len(sg); i++ {
			sgID, err := strconv.Atoi(sg[i])
			if err != nil {
				logger.Error("Invalid security group ID", err)
				continue
			}
			permit, _ := memberShip.CheckOwner(model.Writer, "security_groups", int64(sgID))
			if !permit {
				logger.Error("Not authorized to access security group")
				c.Data["ErrorMsg"] = "Not authorized to access security group"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			sgIDs = append(sgIDs, int64(sgID))
		}
	} else {
		sgID := store.Get("defsg").(int64)
		permit, _ := memberShip.CheckOwner(model.Writer, "security_groups", int64(sgID))
		if !permit {
			logger.Error("Not authorized to access security group")
			c.Data["ErrorMsg"] = "Not authorized to access security group"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		sgIDs = append(sgIDs, sgID)
	}
	secgroups := []*model.SecurityGroup{}
	if err = DB().Where(sgIDs).Find(&secgroups).Error; err != nil {
		logger.Error("Security group query failed", err)
		return
	}
	iface, err := CreateInterface(ctx, subnet, instID, memberShip.OrgID, -1, 0, 0, address, mac, ifname, "instance", secgroups, false)
	if err != nil {
		c.JSON(500, map[string]interface{}{
			"error": err.Error(),
		})
	}
	c.JSON(200, iface)
}

func (v *InterfaceView) Delete(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	id := c.ParamsInt64("id")
	permit, err := memberShip.CheckOwner(model.Writer, "interfaces", id)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.Error(http.StatusBadRequest)
		return
	}
	iface := &model.Interface{Model: model.Model{ID: id}}
	err = DB().Take(iface).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = DeleteInterface(ctx, iface)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, "ok")
}

func (v *InterfaceView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../instances"
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
	err = interfaceAdmin.Update(ctx, instance, iface, name, int32(inbound), int32(outbound), allowSpoofing, secgroups, ifaceSubnets, siteSubnets, ipCount)
	if err != nil {
		logger.Debug("Failed to update interface", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
	}
	c.Redirect(redirectTo)
}
