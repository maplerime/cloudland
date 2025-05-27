/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"
)

var (
	floatingIpAdmin = &FloatingIpAdmin{}
	floatingIpView  = &FloatingIpView{}
)

type FloatingIps struct {
	Instance  int64  `json:"instance"`
	PublicIp  string `json:"public_ip"`
	PrivateIp string `json:"private_ip"`
}

type FloatingIpAdmin struct{}
type FloatingIpView struct{}

func (a *FloatingIpAdmin) createAndAllocateFloatingIps(ctx context.Context, db *gorm.DB, memberShip *MemberShip, name string, inbound, outbound int32, count int, subnets []*model.Subnet, publicIp string, instance *model.Instance) ([]*model.FloatingIp, error) {
	floatingIps := make([]*model.FloatingIp, 0)
	logger.Debugf("subnets: %v, publicIp: %s, instance: %v, count: %d, inbound: %d, outbound: %d", subnets, publicIp, instance, count, inbound, outbound)
	for i := 0; i < count; i++ {
		uniqueName := fmt.Sprintf("%s-%d-%d", name, i, time.Now().UnixNano())
		fip := &model.FloatingIp{Model: model.Model{Creater: memberShip.UserID}, Owner: memberShip.OrgID, Name: uniqueName, Inbound: inbound, Outbound: outbound}
		if err := db.Create(fip).Error; err != nil {
			logger.Error("DB failed to create floating ip", err)
			return nil, err
		}
		logger.Debugf("fip: %v, subnets: %v, publicIp: %s", fip, subnets, publicIp)
		fipIface, err := AllocateFloatingIp(ctx, fip.ID, memberShip.OrgID, subnets, publicIp)
		if err != nil {
			logger.Error("DB failed to allocate floating ip", err)
			return nil, err
		}
		fip.FipAddress = fipIface.Address.Address
		fip.IPAddress = strings.Split(fip.FipAddress, "/")[0]
		fip.Interface = fipIface
		if instance != nil {
			if err := a.Attach(ctx, fip, instance); err != nil {
				logger.Error("Execute floating ip failed", err)
				return nil, err
			}
		}
		if err := db.Model(fip).Updates(fip).Error; err != nil {
			logger.Error("DB failed to update floating ip", err)
			return nil, err
		}
		floatingIps = append(floatingIps, fip)
	}
	return floatingIps, nil
}

func (a *FloatingIpAdmin) Create(ctx context.Context, instance *model.Instance, pubSubnets []*model.Subnet, publicIp string, name string, inbound, outbound, activationCount int32, siteSubnets []*model.Subnet) (floatingIps []*model.FloatingIp, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = fmt.Errorf("Not authorized for this operation")
		return
	}
	logger.Debugf("instance: %v, pubSubnets: %v, publicIp: %s, name: %s, inbound: %d, outbound: %d, activationCount: %d, siteSubnets: %v", instance, pubSubnets, publicIp, name, inbound, outbound, activationCount, siteSubnets)

	if publicIp != "" && (activationCount > 1 || len(siteSubnets) > 0) {
		logger.Error("Public ip and subnets cannot be specified at the same time")
		err = fmt.Errorf("Public ip and subnets cannot be specified at the same time")
		return
	}

	if len(pubSubnets) != 0 {
		for _, pubSubnet := range pubSubnets {
			if pubSubnet.Type != "public" {
				logger.Error("Subnet must be public", err)
				err = fmt.Errorf("Subnet must be public")
				return
			}
		}
	}
	if len(siteSubnets) != 0 {
		for _, siteSubnet := range siteSubnets {
			if siteSubnet.Type != "site" {
				logger.Error("Subnet must be site", err)
				err = fmt.Errorf("Subnet must be site")
				return
			}
		}
	}

	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	floatingIps = make([]*model.FloatingIp, 0)
	logger.Debugf("pubSubnets: %v, publicIp: %s, instance: %v, activationCount: %d, inbound: %d, outbound: %d", pubSubnets, publicIp, instance, activationCount, inbound, outbound)

	if len(pubSubnets) < 1 {
		err = db.Where("type = ?", "public").Find(&pubSubnets).Error
		if err != nil {
			logger.Error("Failed to query public subnets ", err)
			return
		}
		if len(pubSubnets) == 0 {
			logger.Error("No public subnets available")
			return nil, fmt.Errorf("No public subnets available")
		}

		idleCountTotal := int64(0)
		for _, subnet := range pubSubnets {
			var idleCount int64
			idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
				return
			}
			idleCountTotal += idleCount
		}
		if idleCountTotal < int64(activationCount) {
			logger.Errorf("Not enough idle addresses for public subnets, idleCountTotal: %d, activationCount: %d, pubSubnets: %v", idleCountTotal, activationCount, pubSubnets)
			return nil, fmt.Errorf("Not enough idle addresses for public subnets")
		}
	}

	ips, err := a.createAndAllocateFloatingIps(ctx, db, memberShip, name, inbound, outbound, int(activationCount), pubSubnets, publicIp, instance)
	if err != nil {
		return nil, err
	}
	floatingIps = append(floatingIps, ips...)

	logger.Debugf("siteSubnets: %v", siteSubnets)
	for i := 0; i < len(siteSubnets); i++ {
		logger.Debugf("siteSubnets[%d]: %v, idleCount: %d, activationCount: %d, inbound: %d, outbound: %d", i, siteSubnets[i], siteSubnets[i].IdleCount, siteSubnets[i].IdleCount, inbound, outbound)
		ips, err := a.createAndAllocateFloatingIps(ctx, db, memberShip, name, inbound, outbound, int(siteSubnets[i].IdleCount), []*model.Subnet{siteSubnets[i]}, "", instance)
		if err != nil {
			return nil, err
		}
		floatingIps = append(floatingIps, ips...)
	}

	return floatingIps, nil
}

func (a *FloatingIpAdmin) Attach(ctx context.Context, floatingIp *model.FloatingIp, instance *model.Instance) (err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = fmt.Errorf("Not authorized for this operation")
		return
	}
	ctx, db := GetContextDB(ctx)
	if instance == nil || instance.Status != "running" {
		logger.Error("Instance is not running")
		err = fmt.Errorf("Instance must be running")
		return
	}
	instID := instance.ID
	routerID := instance.RouterID
	if routerID == 0 {
		logger.Error("Instance has no router")
		err = fmt.Errorf("Instance has no router")
		return
	}
	router := &model.Router{Model: model.Model{ID: routerID}}
	err = db.Take(router).Error
	if err != nil {
		logger.Error("DB failed to query router", err)
		return
	}
	var primaryIface *model.Interface
	for i, iface := range instance.Interfaces {
		if iface.PrimaryIf {
			primaryIface = instance.Interfaces[i]
			break
		}
	}
	if primaryIface == nil {
		err = fmt.Errorf("No primary interface for the instance, %d", instID)
		return
	}
	floatingIp.IntAddress = primaryIface.Address.Address
	floatingIp.InstanceID = instance.ID
	floatingIp.RouterID = instance.RouterID
	err = db.Model(floatingIp).Updates(floatingIp).Error
	if err != nil {
		logger.Error("DB failed to update floating ip", err)
		return
	}
	pubSubnet := floatingIp.Interface.Address.Subnet
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_floating.sh '%d' '%s' '%s' '%d' '%s' '%d' '%d' '%d' '%d'", router.ID, floatingIp.FipAddress, pubSubnet.Gateway, pubSubnet.Vlan, primaryIface.Address.Address, primaryIface.Address.Subnet.Vlan, floatingIp.ID, floatingIp.Inbound, floatingIp.Outbound)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Execute floating ip failed", err)
		return
	}
	return
}

func (a *FloatingIpAdmin) Get(ctx context.Context, id int64) (floatingIp *model.FloatingIp, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid floatingIp ID: %d", id)
		logger.Error(err)
		return
	}
	memberShip := GetMemberShip(ctx)
	db := DB()
	where := memberShip.GetWhere()
	floatingIp = &model.FloatingIp{Model: model.Model{ID: id}}
	err = db.Preload("Interface").Preload("Interface.Address").Preload("Interface.Address.Subnet").Where(where).Take(floatingIp).Error
	if err != nil {
		logger.Error("DB failed to query floatingIp ", err)
		return
	}
	if floatingIp.InstanceID > 0 {
		floatingIp.Instance = &model.Instance{Model: model.Model{ID: floatingIp.InstanceID}}
		err = db.Take(floatingIp.Instance).Error
		if err != nil {
			logger.Error("DB failed to query instance ", err)
			return
		}
		instance := floatingIp.Instance
		err = db.Preload("Address").Preload("Address.Subnet").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
		if err != nil {
			logger.Error("Failed to query interfaces %v", err)
			return
		}
	}
	if floatingIp.RouterID > 0 {
		floatingIp.Router = &model.Router{Model: model.Model{ID: floatingIp.RouterID}}
		err = db.Take(floatingIp.Router).Error
		if err != nil {
			logger.Error("DB failed to query instance ", err)
			return
		}
	}
	return
}

func (a *FloatingIpAdmin) GetFloatingIpByUUID(ctx context.Context, uuID string) (floatingIp *model.FloatingIp, err error) {
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	floatingIp = &model.FloatingIp{}
	err = db.Preload("Interface").Preload("Interface.Address").Preload("Interface.Address.Subnet").Where(where).Where("uuid = ?", uuID).Take(floatingIp).Error
	if err != nil {
		logger.Error("Failed to query floatingIp, %v", err)
		return
	}
	if floatingIp.InstanceID > 0 {
		floatingIp.Instance = &model.Instance{Model: model.Model{ID: floatingIp.InstanceID}}
		err = db.Take(floatingIp.Instance).Error
		if err != nil {
			logger.Error("DB failed to query instance ", err)
			return
		}
		instance := floatingIp.Instance
		err = db.Preload("Address").Preload("Address.Subnet").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
		if err != nil {
			logger.Error("Failed to query interfaces %v", err)
			return
		}
	}
	if floatingIp.RouterID > 0 {
		floatingIp.Router = &model.Router{Model: model.Model{ID: floatingIp.RouterID}}
		err = db.Take(floatingIp.Router).Error
		if err != nil {
			logger.Error("DB failed to query instance ", err)
			return
		}
	}
	return
}

func (a *FloatingIpAdmin) Detach(ctx context.Context, floatingIp *model.FloatingIp) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if floatingIp.Instance != nil {
		var primaryIface *model.Interface
		instance := floatingIp.Instance
		for i, iface := range instance.Interfaces {
			if iface.PrimaryIf {
				primaryIface = instance.Interfaces[i]
				break
			}
		}
		control := fmt.Sprintf("inter=%d", floatingIp.Instance.Hyper)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_floating.sh '%d' '%s' '%s' '%d' '%d'", floatingIp.RouterID, floatingIp.FipAddress, floatingIp.IntAddress, primaryIface.Address.Subnet.Vlan, floatingIp.ID)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Detach floating ip failed", err)
			return
		}
	}
	logger.Errorf("Floating ip: %v\n", floatingIp)
	floatingIp.InstanceID = 0
	floatingIp.Instance = nil
	err = db.Model(floatingIp).Where("id = ?", floatingIp.ID).Update(map[string]interface{}{"instance_id": 0}).Error
	if err != nil {
		logger.Error("Failed to update instance ID for floating ip", err)
		return
	}
	return
}

func (a *FloatingIpAdmin) Delete(ctx context.Context, floatingIp *model.FloatingIp) (err error) {
	ctx, _, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if floatingIp.Instance != nil {
		err = a.Detach(ctx, floatingIp)
		if err != nil {
			logger.Error("Failed to detach floating ip", err)
			return
		}
	}
	err = DeallocateFloatingIp(ctx, floatingIp.ID)
	if err != nil {
		logger.Error("DB failed to deallocate floating ip", err)
		return
	}
	return
}

func (a *FloatingIpAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, floatingIps []*model.FloatingIp, err error) {
	memberShip := GetMemberShip(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}
	if query != "" {
		query = fmt.Sprintf("fip_address like '%%%s%%' or int_address like '%%%s%%' or name like '%%%s%%'", query, query)
	}

	db := DB()
	where := memberShip.GetWhere()
	floatingIps = []*model.FloatingIp{}
	if err = db.Model(&model.FloatingIp{}).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Error("DB failed to count floating ip(s), %v", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Instance").Preload("Instance.Zone").Where(where).Where(query).Find(&floatingIps).Error; err != nil {
		logger.Error("DB failed to query floating ip(s), %v", err)
		return
	}
	for _, fip := range floatingIps {
		if fip.InstanceID <= 0 {
			continue
		}
		fip.Instance = &model.Instance{Model: model.Model{ID: fip.InstanceID}}
		err = db.Take(fip.Instance).Error
		if err != nil {
			logger.Error("DB failed to query instance ", err)
		}
		instance := fip.Instance
		err = db.Preload("Address").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
		if err != nil {
			logger.Error("Failed to query interfaces ", err)
			err = nil
			continue
		}
		if fip.RouterID > 0 {
			fip.Router = &model.Router{Model: model.Model{ID: fip.RouterID}}
			err = db.Take(fip.Router).Error
			if err != nil {
				logger.Error("DB failed to query instance ", err)
				err = nil
				continue
			}
		}
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, fip := range floatingIps {
			fip.OwnerInfo = &model.Organization{Model: model.Model{ID: fip.Owner}}
			if err = db.Take(fip.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				return
			}
		}
	}

	return
}

func (v *FloatingIpView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.Query("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	total, floatingIps, err := floatingIpAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list floating ip(s), %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	pages := GetPages(total, limit)
	c.Data["FloatingIps"] = floatingIps
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "floatingips")
}

func (v *FloatingIpView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "id does not exist"
		c.Error(http.StatusBadRequest)
		return
	}
	floatingIpID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid floating ip ID ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	floatingIp, err := floatingIpAdmin.Get(ctx, int64(floatingIpID))
	if err != nil {
		logger.Error("Failed to get floating ip ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = floatingIpAdmin.Delete(ctx, floatingIp)
	if err != nil {
		logger.Error("Failed to delete floating ip, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "floatingips",
	})
	return
}

func (v *FloatingIpView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	where := memberShip.GetWhere()
	instances := []*model.Instance{}
	err := db.Where(where).Find(&instances).Error
	if err != nil {
		logger.Error("Failed to query instances %v", err)
		return
	}
	for _, instance := range instances {
		if err = db.Preload("Address").Preload("Address.Subnet").Preload("Address.Subnet.Router").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error; err != nil {
			logger.Error("Failed to query interfaces %v", err)
			return
		}
	}
	logger.Debugf("Instances count: %d", len(instances))

	subnets := []*model.Subnet{}
	err = db.Where("type = ?", "public").Find(&subnets).Error
	if err != nil {
		logger.Error("Failed to query subnets %v", err)
		return
	}
	siteSubnets := []*model.Subnet{}
	err = db.Where("type = ?", "site").Find(&siteSubnets).Error
	if err != nil {
		logger.Error("Failed to query site subnets %v", err)
		return
	}

	c.Data["Instances"] = instances
	c.Data["Subnets"] = subnets
	c.Data["SiteSubnets"] = siteSubnets
	c.HTML(200, "floatingips_new")
}

func (v *FloatingIpView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../floatingips"
	instID := c.QueryInt64("instance")
	publicIp := c.QueryTrim("publicip")
	name := c.QueryTrim("name")
	inbound := c.QueryInt("inbound")
	outbound := c.QueryInt("outbound")
	count := c.QueryInt("count")
	publicSubnetStr := c.QueryTrim("publicsubnet")
	siteSubnetStr := c.QueryTrim("sitesubnet")

	var publicSubnets, siteSubnets []string
	if publicSubnetStr != "" {
		publicSubnets = strings.Split(publicSubnetStr, ",")
	}
	if siteSubnetStr != "" {
		siteSubnets = strings.Split(siteSubnetStr, ",")
	}

	if (count < 1 && len(siteSubnets) < 1) || count > 64 {
		logger.Error("Count must be greater than 0 and less than 64 or site subnet must be specified")
		c.Data["ErrorMsg"] = "Count must be greater than 0 and less than 64 or site subnet must be specified"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	if inbound < 0 || inbound > 20000 {
		logger.Errorf("Inbound out of range %d", inbound)
		c.Data["ErrorMsg"] = "Inbound out of range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if outbound < 0 || outbound > 20000 {
		logger.Errorf("Outbound out of range %d", outbound)
		c.Data["ErrorMsg"] = "Outbound out of range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	instance, err := instanceAdmin.Get(ctx, int64(instID))
	if err != nil {
		logger.Error("Failed to get instance ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	// 获取站点子网
	siteSubnetList := make([]*model.Subnet, 0)
	if len(siteSubnets) > 0 {
		for _, subnetID := range siteSubnets {
			if subnetID == "" {
				continue
			}
			id, err := strconv.ParseInt(subnetID, 10, 64)
			if err != nil {
				logger.Error("Invalid subnet ID ", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			subnet := &model.Subnet{Model: model.Model{ID: id}}
			if err := DB().Take(subnet).Error; err != nil {
				logger.Error("Failed to get subnet ", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(500, "500")
				return
			}
			var idleCount int64
			idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(500, "500")
				return
			}
			if idleCount == 0 {
				logger.Errorf("No idle addresses for site subnet %s", subnet.Name)
				c.Data["ErrorMsg"] = "No idle addresses for site subnet"
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			subnet.IdleCount = idleCount
			siteSubnetList = append(siteSubnetList, subnet)
		}
	}

	// 获取公共子网
	pubSubnets := make([]*model.Subnet, 0)
	if len(publicSubnets) > 0 {
		idleCountTotal := int64(0)
		for _, subnetID := range publicSubnets {
			if subnetID == "" {
				continue
			}
			id, err := strconv.ParseInt(subnetID, 10, 64)
			if err != nil {
				logger.Error("Invalid subnet ID ", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(http.StatusBadRequest, "error")
				return
			}
			subnet := &model.Subnet{Model: model.Model{ID: id}}
			if err := DB().Take(subnet).Error; err != nil {
				logger.Error("Failed to get subnet ", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(500, "500")
				return
			}
			var idleCount int64
			idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
				c.Data["ErrorMsg"] = err.Error()
				c.HTML(500, "500")
				return
			}
			idleCountTotal += idleCount
			subnet.IdleCount = idleCount
			pubSubnets = append(pubSubnets, subnet)
		}
		if idleCountTotal < int64(count) {
			logger.Errorf("Not enough idle addresses for public subnets, idleCountTotal: %d, activationCount: %d", idleCountTotal, count)
			c.Data["ErrorMsg"] = "Not enough idle addresses for public subnets"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}
	logger.Debugf("pubSubnets: %v, publicIp: %s, instance: %v, activationCount: %d, inbound: %d, outbound: %d, siteSubnets: %v", pubSubnets, publicIp, instance, count, inbound, outbound, siteSubnetList)
	_, err = floatingIpAdmin.Create(c.Req.Context(), instance, pubSubnets, publicIp, name, int32(inbound), int32(outbound), int32(count), siteSubnetList)
	if err != nil {
		logger.Error("Failed to create floating ip", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}

func AllocateFloatingIp(ctx context.Context, floatingIpID, owner int64, pubSubnets []*model.Subnet, address string) (fipIface *model.Interface, err error) {
	ctx, db := GetContextDB(ctx)
	subnets := []*model.Subnet{}
	if len(pubSubnets) > 0 {
		subnets = append(subnets, pubSubnets...)
	} else {
		// 如果没有指定子网，查询所有可用的公共子网
		err = db.Where("type = ?", "public").Find(&subnets).Error
		if err != nil {
			logger.Error("Failed to query public subnets ", err)
			return
		}
		if len(subnets) == 0 {
			logger.Error("No public subnets available")
			return nil, fmt.Errorf("No public subnets available")
		}
	}
	name := "fip"
	logger.Debugf("Available subnets: %v", subnets)
	for _, subnet := range subnets {
		fipIface, err = CreateInterface(ctx, subnet, floatingIpID, owner, -1, 0, 0, address, "", name, "floating", nil)
		if err == nil {
			logger.Debugf("Successfully created floating IP interface: %v", fipIface)
			break
		}
		logger.Debugf("Failed to create floating IP interface on subnet %d: %v", subnet.ID, err)
	}
	return
}

func DeallocateFloatingIp(ctx context.Context, floatingIpID int64) (err error) {
	ctx, db := GetContextDB(ctx)
	DeleteInterfaces(ctx, floatingIpID, 0, "floating")
	floatingIp := &model.FloatingIp{Model: model.Model{ID: floatingIpID}}
	err = db.Delete(floatingIp).Error
	if err != nil {
		logger.Error("Failed to delete floating ip, %v", err)
		return
	}
	return
}
