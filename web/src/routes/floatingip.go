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

func (a *FloatingIpAdmin) createAndAllocateFloatingIps(ctx context.Context, name string, inbound, outbound int32, count int, subnets []*model.Subnet, publicIp string, instance *model.Instance, isSite bool, group *model.IpGroup, loadBalancer *model.LoadBalancer) ([]*model.FloatingIp, error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	floatingIps := make([]*model.FloatingIp, 0)
	publicType := string(PublicFloating)
	routerID := int64(0)
	loadBalancerID := int64(0)
	if loadBalancer != nil {
		publicType = string(PublicLoadBalancer)
		loadBalancerID = loadBalancer.ID
		routerID = loadBalancer.RouterID
	}
	logger.Debugf("subnets: %v, publicIp: %s, instance: %v, count: %d, inbound: %d, outbound: %d", subnets, publicIp, instance, count, inbound, outbound)
	for i := 0; i < count; i++ {
		uniqueName := fmt.Sprintf("%s-%d-%d", name, i, time.Now().UnixNano())
		var groupID int64
		if group != nil && group.ID != 0 {
			groupID = group.ID
		}
		fip := &model.FloatingIp{Model: model.Model{Creater: memberShip.UserID}, Owner: memberShip.OrgID, Name: uniqueName, Inbound: inbound, Outbound: outbound, Type: publicType, GroupID: groupID, LoadBalancerID: loadBalancerID, RouterID: routerID}
		if err := db.Create(fip).Error; err != nil {
			logger.Error("DB failed to create floating ip", err)
			return nil, NewCLError(ErrFIPCreateFailed, "Failed to create floating ip", err)
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
		fip.SubnetID = fipIface.Address.Subnet.ID
		if instance != nil {
			if err := a.Attach(ctx, fip, instance); err != nil {
				logger.Error("Execute attaching floating ip failed", err)
				return nil, err
			}
		}
		if isSite {
			fip.Type = string(PublicSite)
			if i == 0 && instance != nil {
				var primaryInterfaceID int64
				for _, iface := range instance.Interfaces {
					if iface.PrimaryIf {
						primaryInterfaceID = iface.ID
						break
					}
				}
				for _, subnet := range subnets {
					if subnet.Interface != primaryInterfaceID {
						subnet.Interface = primaryInterfaceID
						if err := db.Model(subnet).Update("interface", primaryInterfaceID).Error; err != nil {
							logger.Error("Failed to update subnet interface", err)
							return nil, NewCLError(ErrSubnetUpdateFailed, "Failed to update subnet interface", err)
						}
					}
				}
			}
		}
		if err := db.Model(fip).Updates(fip).Error; err != nil {
			logger.Error("DB failed to update floating ip", err)
			return nil, NewCLError(ErrFIPUpdateFailed, "Failed to update floating ip", err)
		}
		floatingIps = append(floatingIps, fip)
	}
	return floatingIps, nil
}

func (a *FloatingIpAdmin) createDummyFloatingIp(ctx context.Context, instance *model.Instance, publicIp string) (floatingIp *model.FloatingIp, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	uniqueName := fmt.Sprintf("%s-%d", instance.Hostname, time.Now().UnixNano())
	fip := &model.FloatingIp{Model: model.Model{Creater: memberShip.UserID}, Owner: memberShip.OrgID, Name: uniqueName, Inbound: 0, Outbound: 0, FipAddress: publicIp, IntAddress: publicIp, InstanceID: instance.ID, Type: string(PublicNative)}
	if err = db.Create(fip).Error; err != nil {
		logger.Error("DB failed to create floating ip", err)
		return nil, NewCLError(ErrDummyFIPCreateFailed, "Failed to create floating ip", err)
	}
	return
}

func (a *FloatingIpAdmin) Create(ctx context.Context, instance *model.Instance, pubSubnets []*model.Subnet, publicIp string, name string, inbound, outbound, activationCount int32, siteSubnets []*model.Subnet, group *model.IpGroup, loadBalancer *model.LoadBalancer) (floatingIps []*model.FloatingIp, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	logger.Debugf("instance: %v, pubSubnets: %v, publicIp: %s, name: %s, inbound: %d, outbound: %d, activationCount: %d, siteSubnets: %v", instance, pubSubnets, publicIp, name, inbound, outbound, activationCount, siteSubnets)

	if publicIp != "" && (activationCount > 1 || len(siteSubnets) > 0) {
		logger.Error("Public ip and subnets cannot be specified at the same time")
		err = NewCLError(ErrInvalidParameter, "Public ip and subnets cannot be specified at the same time", nil)
		return
	}

	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	if len(pubSubnets) == 0 {
		err = db.Where("type = ?", "public").Find(&pubSubnets).Error
		if err != nil {
			logger.Error("Failed to query public subnets ", err)
			return nil, NewCLError(ErrSQLSyntaxError, "Failed to query public subnets", err)
		}
		if len(pubSubnets) == 0 {
			logger.Error("No public subnets available")
			return nil, NewCLError(ErrSubnetNotFound, "No public subnets available", nil)
		}
	}
	idleCountTotal := int64(0)
	for _, subnet := range pubSubnets {
		if subnet.Type != "public" {
			logger.Error("Subnet must be public", err)
			err = NewCLError(ErrSubnetShouldBePublic, "Subnet must be public", nil)
			return
		}
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
		return nil, NewCLError(ErrInsufficientAddress, "Not enough idle addresses for public subnets", nil)
	}

	if len(siteSubnets) > 0 {
		for _, subnet := range siteSubnets {
			if subnet.Type != "site" {
				logger.Error("Subnet must be site", err)
				err = NewCLError(ErrSubnetShouldBeSite, "Subnet must be site", nil)
				return
			}
			var idleCount int64
			idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(ctx, subnet)
			if err != nil {
				logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
				return
			}
			if idleCount == 0 {
				logger.Errorf("No idle addresses for site subnet %s", subnet.Name)
				err = NewCLError(ErrInsufficientAddress, "No idle addresses for site subnet", nil)
				return
			}
			subnet.IdleCount = idleCount
		}
	}

	floatingIps = make([]*model.FloatingIp, 0)
	logger.Debugf("pubSubnets: %v, publicIp: %s, instance: %v, activationCount: %d, inbound: %d, outbound: %d, siteSubnets: %v, group: %v", pubSubnets, publicIp, instance, activationCount, inbound, outbound, siteSubnets, group)
	var fips []*model.FloatingIp
	fips, err = a.createAndAllocateFloatingIps(ctx, name, inbound, outbound, int(activationCount), pubSubnets, publicIp, instance, false, group, loadBalancer)
	if err != nil {
		return
	}
	floatingIps = append(floatingIps, fips...)

	logger.Debugf("siteSubnets: %v", siteSubnets)
	for i := 0; i < len(siteSubnets); i++ {
		logger.Debugf("siteSubnets[%d]: %v, idleCount: %d, activationCount: %d, inbound: %d, outbound: %d, group: %v", i, siteSubnets[i], siteSubnets[i].IdleCount, siteSubnets[i].IdleCount, inbound, outbound, group)
		var siteFips []*model.FloatingIp
		siteFips, err = a.createAndAllocateFloatingIps(ctx, name, inbound, outbound, int(siteSubnets[i].IdleCount), []*model.Subnet{siteSubnets[i]}, "", instance, true, group, nil)
		if err != nil {
			return
		}
		floatingIps = append(floatingIps, siteFips...)
	}

	if loadBalancer != nil {
		err = CreateVrrpConf(ctx, loadBalancer)
		if err != nil {
			err = NewCLError(ErrVrrpInstanceCreateFailed, "Recreate keepalived config failed", err)
			return
		}
	}

	return floatingIps, nil
}

func (a *FloatingIpAdmin) Attach(ctx context.Context, floatingIp *model.FloatingIp, instance *model.Instance) (err error) {
	if floatingIp.Type != string(PublicFloating) && floatingIp.Type != string(PublicSite) {
		logger.Infof("Cannot attach floating IP of type %s, only PublicFloating and PublicSite types are supported for attachment", floatingIp.Type)
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Cannot attach floating IP of type %s, only PublicFloating and PublicSite types are supported for attachment", floatingIp.Type), nil)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
		return
	}
	ctx, db := GetContextDB(ctx)
	if instance == nil || (instance.Status == "pending") {
		logger.Error("Instance is not running")
		err = NewCLError(ErrInstanceInvalidState, "Instance is not running", nil)
		return
	}
	instID := instance.ID
	routerID := instance.RouterID
	if routerID == 0 {
		logger.Error("Instance has no router")
		err = NewCLError(ErrInstanceNoRouter, "Instance has no router", nil)
		return
	}
	router := &model.Router{Model: model.Model{ID: routerID}}
	err = db.Take(router).Error
	if err != nil {
		logger.Error("DB failed to query router", err)
		return NewCLError(ErrRouterNotFound, "DB failed to query router", err)
	}
	var primaryIface *model.Interface
	for i, iface := range instance.Interfaces {
		if iface.PrimaryIf {
			primaryIface = instance.Interfaces[i]
			break
		}
	}
	if primaryIface == nil {
		err = NewCLError(ErrInstanceNoPrimaryInterface, fmt.Sprintf("No primary interface for the instance, %d", instID), nil)
		return
	}
	floatingIp.IntAddress = primaryIface.Address.Address
	floatingIp.InstanceID = instance.ID
	floatingIp.RouterID = instance.RouterID
	err = db.Model(floatingIp).Updates(floatingIp).Error
	if err != nil {
		logger.Error("DB failed to update floating ip", err)
		return NewCLError(ErrFIPUpdateFailed, "DB failed to update floating ip", err)
	}

	err = a.EnsureSubnetID(ctx, floatingIp)
	if err != nil {
		logger.Error("Failed to ensure subnet_id", err)
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
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Invalid floatingIp ID: %d", id), nil)
		logger.Error(err)
		return
	}
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	where := memberShip.GetWhere()
	floatingIp = &model.FloatingIp{Model: model.Model{ID: id}}
	err = db.Preload("Interface").Preload("Interface.SecurityGroups").Preload("Interface.Address").Preload("Interface.Address.Subnet").Preload("Subnet").Preload("Group").Where(where).Take(floatingIp).Error
	if err != nil {
		logger.Error("DB failed to query floatingIp ", err)
		return nil, NewCLError(ErrSQLSyntaxError, "Failed to query floatingIp", err)
	}
	if floatingIp.InstanceID > 0 {
		floatingIp.Instance = &model.Instance{Model: model.Model{ID: floatingIp.InstanceID}}
		err = db.Take(floatingIp.Instance).Error
		if err != nil {
			msg := fmt.Sprintf("Failed to query instance: %d", floatingIp.InstanceID)
			logger.Error(msg, err)
			return nil, NewCLError(ErrInstanceNotFound, msg, err)
		}
		instance := floatingIp.Instance
		err = db.Preload("Address").Preload("Address.Subnet").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
		if err != nil {
			logger.Error("Failed to query interfaces %v", err)
			return nil, NewCLError(ErrSQLSyntaxError, "Failed to query interfaces", err)
		}
	}
	if floatingIp.RouterID > 0 {
		floatingIp.Router = &model.Router{Model: model.Model{ID: floatingIp.RouterID}}
		err = db.Take(floatingIp.Router).Error
		if err != nil {
			msg := fmt.Sprintf("Failed to query router: %d", floatingIp.RouterID)
			logger.Error(msg, err)
			return nil, NewCLError(ErrRouterNotFound, msg, err)
		}
	}

	err = a.EnsureSubnetID(ctx, floatingIp)
	if err != nil {
		logger.Error("Failed to ensure subnet_id", err)
		return
	}

	return
}

func (a *FloatingIpAdmin) GetFloatingIpByUUID(ctx context.Context, uuID string) (floatingIp *model.FloatingIp, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	floatingIp = &model.FloatingIp{}
	err = db.Preload("Interface").Preload("Interface.SecurityGroups").Preload("Interface.Address").Preload("Interface.Address.Subnet").Preload("Subnet").Preload("Group").Where(where).Where("uuid = ?", uuID).Take(floatingIp).Error
	if err != nil {
		logger.Error("Failed to query floatingIp, %v", err)
		return nil, NewCLError(ErrDatabaseError, "Failed to query floatingIp", err)
	}
	if floatingIp.InstanceID > 0 {
		floatingIp.Instance = &model.Instance{Model: model.Model{ID: floatingIp.InstanceID}}
		err = db.Take(floatingIp.Instance).Error
		if err != nil {
			msg := fmt.Sprintf("Failed to query instance: %d", floatingIp.InstanceID)
			logger.Error(msg, err)
			return nil, NewCLError(ErrInstanceNotFound, msg, err)
		}
		instance := floatingIp.Instance
		err = db.Preload("Address").Preload("Address.Subnet").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
		if err != nil {
			msg := fmt.Sprintf("Failed to query interfaces for instance: %d", instance.ID)
			logger.Error(msg, err)
			return nil, NewCLError(ErrSQLSyntaxError, msg, err)
		}
	}
	if floatingIp.RouterID > 0 {
		floatingIp.Router = &model.Router{Model: model.Model{ID: floatingIp.RouterID}}
		err = db.Take(floatingIp.Router).Error
		if err != nil {
			msg := fmt.Sprintf("Failed to query router: %d", floatingIp.RouterID)
			logger.Error(msg, err)
			return nil, NewCLError(ErrRouterNotFound, msg, err)
		}
	}

	err = a.EnsureSubnetID(ctx, floatingIp)
	if err != nil {
		logger.Error("Failed to ensure subnet_id", err)
		return
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
	if floatingIp.Type == string(PublicNative) {
		if err = db.Delete(floatingIp).Error; err != nil {
			logger.Error("DB: delete native fip failed", err)
			return NewCLError(ErrDeleteNativeFIPFailed, "Failed to delete native floating IP", err)
		}
		floatingIp.Instance = nil
		return
	}
	if floatingIp.Type == string(PublicReserved) {
		floatingIp.Instance = nil
		floatingIp.Interface = nil
		floatingIp.Router = nil
		floatingIp.InstanceID = 0
		floatingIp.IntAddress = ""
		floatingIp.Type = string(PublicFloating)

		updateFields := make(map[string]interface{})
		updateFields["instance_id"] = 0
		updateFields["int_address"] = ""
		updateFields["type"] = string(PublicFloating)

		err = db.Model(floatingIp).Updates(updateFields).Error
		if err != nil {
			logger.Errorf("Failed to update public ip, %v", err)
			return NewCLError(ErrUpdatePublicIPFailed, "Failed to update public ip", err)
		}
		return
	}
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
	logger.Debugf("Floating ip: %v\n", floatingIp)
	floatingIp.InstanceID = 0
	floatingIp.Instance = nil
	err = db.Model(floatingIp).Where("id = ?", floatingIp.ID).Update(map[string]interface{}{"instance_id": 0}).Error
	if err != nil {
		logger.Error("Failed to update instance ID for floating ip", err)
		return NewCLError(ErrUpdateInstIDOfFIPFailed, "Failed to update instance ID for floating ip", err)
	}
	return
}

func (a *FloatingIpAdmin) Update(ctx context.Context, floatingIp *model.FloatingIp, instance *model.Instance, group *model.IpGroup) (floatingIpTemp *model.FloatingIp, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	err = a.Detach(ctx, floatingIp)
	if err != nil {
		logger.Errorf("Failed to detach floating ip %+v", err)
		return
	}

	if instance != nil {
		err = a.Attach(ctx, floatingIp, instance)
		if err != nil {
			logger.Errorf("Failed to attach floating ip %+v", err)
			return
		}
	}

	if group != nil {
		groupID := int64(0)
		if group != nil {
			groupID = group.ID
		}

		err = db.Model(floatingIp).Where("id = ?", floatingIp.ID).Update("group_id", groupID).Error
		if err != nil {
			logger.Error("Failed to update floating ip group_id", err)
			return nil, NewCLError(ErrUpdateGroupIDFailed, "Failed to update floating ip group_id", err)
		}
	}

	floatingIpTemp, err = a.Get(ctx, floatingIp.ID)
	if err != nil {
		logger.Error("Failed to get updated floating ip", err)
		return
	}

	return floatingIpTemp, nil
}

func (a *FloatingIpAdmin) Delete(ctx context.Context, floatingIp *model.FloatingIp) (err error) {
	if floatingIp.Type != string(PublicFloating) && floatingIp.Type != string(PublicLoadBalancer) {
		errorStr := fmt.Sprintf("Cannot delete floating IP of type %s, only PublicFloating or PublicLoadBalancer type is supported for deletion", floatingIp.Type)
		logger.Info(errorStr)
		err = NewCLError(ErrInvalidParameter, errorStr, nil)
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
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
	err = a.DeallocateFloatingIp(ctx, floatingIp.ID)
	if err != nil {
		logger.Error("DB failed to deallocate floating ip", err)
		return
	}
	if floatingIp.LoadBalancerID > 0 {
		loadBalancer := &model.LoadBalancer{Model: model.Model{ID: floatingIp.LoadBalancerID}}
		err = db.Preload("VrrpInstance").Preload("VrrpInstance.VrrpSubnet").Take(loadBalancer).Error
		if err != nil {
			logger.Error("DB failed to query load balancer ", err)
			return
		}
		hyperGroup := ""
		hyperGroup, _, _, err = GetVrrpHyperGroup(ctx, loadBalancer.VrrpInstance)
		if err != nil {
			logger.Error("Failed to query vrrp hyper group and interfaces", err)
			return
		}
		control := "toall=" + hyperGroup
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_lb_floating.sh '%d' '%s' '%d' '%d'", floatingIp.RouterID, floatingIp.FipAddress, floatingIp.Subnet.Vlan, floatingIp.ID)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Clear lb floating ip execution failed ", err)
			err = NewCLError(ErrExecuteOnHyperFailed, "Clear lb floating execution failed", err)
			return
		}
		err = CreateVrrpConf(ctx, loadBalancer)
		if err != nil {
			logger.Error("Recreate keepalived config failed", err)
			err = NewCLError(ErrVrrpInstanceCreateFailed, "Recreate keepalived config failed", err)
			return
		}
	}
	return
}

func (a *FloatingIpAdmin) List(ctx context.Context, offset, limit int64, order, query string, intQuery string) (total int64, floatingIps []*model.FloatingIp, err error) {
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

	_, db := GetContextDB(ctx)
	where := memberShip.GetWhere()
	floatingIps = []*model.FloatingIp{}
	if err = db.Model(&model.FloatingIp{}).Where(where).Where(query).Where(intQuery).Count(&total).Error; err != nil {
		logger.Error("DB failed to count floating ip(s), %v", err)
		return 0, nil, NewCLError(ErrSQLSyntaxError, "Failed to count floating IPs", err)
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Group").Preload("Instance").Preload("Instance.Zone").Preload("Interface").Preload("Interface.Address").Preload("Interface.Address.Subnet").Preload("Subnet").Where(where).Where(query).Where(intQuery).Find(&floatingIps).Error; err != nil {
		logger.Error("DB failed to query floating ip(s), %v", err)
		return 0, nil, NewCLError(ErrSQLSyntaxError, "Failed to query floating IPs", err)
	}
	db = db.Offset(0).Limit(-1)
	for _, fip := range floatingIps {
		if fip.InstanceID > 0 {
			if fip.Instance != nil && fip.Instance.ID > 0 {
				instance := fip.Instance
				err = db.Preload("Address").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
				if err != nil {
					logger.Error("Failed to query interfaces ", err)
					err = nil
					continue
				}
			} else {
				fip.Instance = &model.Instance{Model: model.Model{ID: fip.InstanceID}}
				err = db.Take(fip.Instance).Error
				if err != nil {
					logger.Error("DB failed to query instance ", err)
					err = nil
					continue
				}
				instance := fip.Instance
				err = db.Preload("Address").Where("instance = ? and primary_if = true", instance.ID).Find(&instance.Interfaces).Error
				if err != nil {
					logger.Error("Failed to query interfaces ", err)
					err = nil
					continue
				}
			}
		}

		if fip.RouterID > 0 {
			fip.Router = &model.Router{Model: model.Model{ID: fip.RouterID}}
			err = db.Take(fip.Router).Error
			if err != nil {
				logger.Error("DB failed to query router ", err)
				err = nil
				continue
			}
		}
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		for _, fip := range floatingIps {
			fip.OwnerInfo = &model.Organization{Model: model.Model{ID: fip.Owner}}
			if err = db.Take(fip.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				return 0, nil, NewCLError(ErrOwnerNotFound, "Failed to query owner info", err)
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
	intQuery := ""
	lbid := c.Params("lbid")
	if lbid != "" {
		loadBalancerID, err := strconv.Atoi(lbid)
		if err != nil {
			logger.Error("Invalid load balancer ID", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		intQuery = fmt.Sprintf("load_balancer_id = %d", loadBalancerID)
	}
	query := c.QueryTrim("q")
	total, floatingIps, err := floatingIpAdmin.List(c.Req.Context(), offset, limit, order, query, intQuery)
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

func (v *FloatingIpView) LBNew(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()

	subnets := []*model.Subnet{}
	err := db.Where("type = ?", "public").Find(&subnets).Error
	if err != nil {
		logger.Error("Failed to query subnets %v", err)
		return
	}
	ipGroups := []*model.IpGroup{}
	err = db.Where("type = ?", string(ResourceIpGroupType)).Find(&ipGroups).Error
	if err != nil {
		logger.Error("Failed to query resource ip groups %v", err)
		return
	}

	c.Data["Subnets"] = subnets
	c.Data["IpGroups"] = ipGroups
	c.HTML(200, "floatingips_lbnew")
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
	err = db.Where("type = ? AND interface = ?", "site", 0).Find(&siteSubnets).Error
	if err != nil {
		logger.Error("Failed to query site subnets %v", err)
		return
	}
	ipGroups := []*model.IpGroup{}
	err = db.Where("type = ?", string(ResourceIpGroupType)).Find(&ipGroups).Error
	if err != nil {
		logger.Error("Failed to query resource ip groups %v", err)
		return
	}

	// Collect subnets with idle addresses
	var validSiteSubnets []*model.Subnet
	for _, subnet := range siteSubnets {
		var idleCount int64
		idleCount, err = subnetAdmin.CountIdleAddressesForSubnet(c.Req.Context(), subnet)
		if err != nil {
			logger.Errorf("Failed to count idle addresses for subnet, err=%v", err)
			return
		}
		if idleCount > 0 {
			validSiteSubnets = append(validSiteSubnets, subnet)
		}
	}
	siteSubnets = validSiteSubnets

	c.Data["Instances"] = instances
	c.Data["Subnets"] = subnets
	c.Data["SiteSubnets"] = siteSubnets
	c.Data["IpGroups"] = ipGroups
	c.HTML(200, "floatingips_new")
}

func (v *FloatingIpView) Edit(c *macaron.Context, store session.Store) {
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	floatingIpID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	floatingIp, err := floatingIpAdmin.Get(c.Req.Context(), int64(floatingIpID))
	if err != nil {
		logger.Error("Failed to get floating ip, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	where := memberShip.GetWhere()
	instances := []*model.Instance{}
	err = db.Where(where).Find(&instances).Error
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

	ipGroups := []*model.IpGroup{}
	err = db.Where("type = ?", string(ResourceIpGroupType)).Find(&ipGroups).Error
	if err != nil {
		logger.Error("Failed to query resource ip groups %v", err)
		return
	}

	c.Data["Instances"] = instances
	c.Data["IpGroups"] = ipGroups
	c.Data["FloatingIp"] = floatingIp
	c.HTML(200, "floatingips_patch")
}

func (v *FloatingIpView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	floatingIpID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	floatingIp, err := floatingIpAdmin.Get(ctx, int64(floatingIpID))
	if err != nil {
		logger.Error("Failed to get floating ip, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	name := c.QueryTrim("name")
	groupID := c.QueryTrim("group")
	instanceID := c.QueryInt64("instance")
	inbound := c.QueryInt("inbound")
	outbound := c.QueryInt("outbound")

	if name == "" {
		c.Data["ErrorMsg"] = "Name is required"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	if inbound < 0 || inbound > 20000 {
		c.Data["ErrorMsg"] = "Inbound out of range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if outbound < 0 || outbound > 20000 {
		c.Data["ErrorMsg"] = "Outbound out of range [0-20000]"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	floatingIp.Name = name
	floatingIp.Inbound = int32(inbound)
	floatingIp.Outbound = int32(outbound)

	var group *model.IpGroup
	if groupID != "" {
		groupIDInt, err := strconv.ParseInt(groupID, 10, 64)
		if err != nil {
			logger.Error("Invalid group ID ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		group, err = ipGroupAdmin.Get(ctx, groupIDInt)
		if err != nil {
			logger.Error("Failed to get ip group ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}

	var instance *model.Instance
	if instanceID > 0 {
		instance, err = instanceAdmin.Get(ctx, instanceID)
		if err != nil {
			logger.Error("Failed to get instance ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(500, "500")
			return
		}
	}

	_, err = floatingIpAdmin.Update(ctx, floatingIp, instance, group)
	if err != nil {
		logger.Error("Failed to update floating ip, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}

	redirectTo := "../floatingips"
	c.Redirect(redirectTo)
	return
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
	groupID := c.QueryTrim("group")
	var group *model.IpGroup
	if groupID != "" {
		groupID, err := strconv.ParseInt(groupID, 10, 64)
		if err != nil {
			logger.Error("Invalid group ID ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		group, err = ipGroupAdmin.Get(ctx, groupID)
		if err != nil {
			logger.Error("Failed to get ip group ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}
	var loadBalancer *model.LoadBalancer
	lbid := c.Params("lbid")
	if lbid != "" {
		loadBalancerID, err := strconv.Atoi(lbid)
		if err != nil {
			logger.Error("Invalid load balancer ID", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		loadBalancer, err = loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
		if err != nil {
			logger.Error("Failed to get ip load balancer ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
	}

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

	var instance *model.Instance
	var err error
	if instID > 0 {
		instance, err = instanceAdmin.Get(ctx, int64(instID))
		if err != nil {
			logger.Error("Failed to get instance ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(500, "500")
			return
		}
	}

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
	logger.Debugf("pubSubnets: %v, publicIp: %s, instance: %v, activationCount: %d, inbound: %d, outbound: %d, siteSubnets: %v, group: %v", pubSubnets, publicIp, instance, count, inbound, outbound, siteSubnetList, group)
	_, err = floatingIpAdmin.Create(c.Req.Context(), instance, pubSubnets, publicIp, name, int32(inbound), int32(outbound), int32(count), siteSubnetList, group, loadBalancer)
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
		err = db.Where("type = ?", "public").Find(&subnets).Error
		if err != nil {
			logger.Error("Failed to query public subnets ", err)
			return nil, NewCLError(ErrSQLSyntaxError, "Failed to query public subnets", err)
		}
		if len(subnets) == 0 {
			logger.Error("No public subnets available")
			return nil, NewCLError(ErrPublicSubnetNotFound, "No public subnets available", nil)
		}
	}
	name := "fip"
	logger.Debugf("Available subnets: %v", subnets)
	for _, subnet := range subnets {
		fipIface, err = CreateInterface(ctx, subnet, floatingIpID, owner, -1, 0, 0, address, "", name, "floating", nil, false)
		if err == nil {
			logger.Debugf("Successfully created floating IP interface: %v", fipIface)
			break
		}
		logger.Debugf("Failed to create floating IP interface on subnet %d: %v", subnet.ID, err)
	}
	return
}

func (a *FloatingIpAdmin) DeallocateFloatingIp(ctx context.Context, floatingIpID int64) (err error) {
	ctx, db := GetContextDB(ctx)
	DeleteInterfaces(ctx, floatingIpID, 0, "floating")
	floatingIp := &model.FloatingIp{Model: model.Model{ID: floatingIpID}}
	err = db.Delete(floatingIp).Error
	if err != nil {
		logger.Error("Failed to delete floating ip, %v", err)
		return NewCLError(ErrFIPDeleteFailed, "Failed to delete floating ip", err)
	}
	return
}

func (a *FloatingIpAdmin) EnsureSubnetID(ctx context.Context, floatingIp *model.FloatingIp) error {
	_, db := GetContextDB(ctx)
	if floatingIp.SubnetID == 0 && floatingIp.Interface != nil && floatingIp.Interface.Address != nil && floatingIp.Interface.Address.Subnet != nil {
		floatingIp.SubnetID = floatingIp.Interface.Address.Subnet.ID
		err := db.Model(floatingIp).Where("id = ?", floatingIp.ID).Update("subnet_id", floatingIp.SubnetID).Error
		if err != nil {
			logger.Errorf("Failed to update floating ip subnet_id: %v", err)
			return NewCLError(ErrUpdateSubnetIDOfFIPFailed, "Failed to update floating ip subnet_id", err)
		}
		logger.Debugf("Updated floating ip %d subnet_id to %d", floatingIp.ID, floatingIp.SubnetID)
	}

	if floatingIp.Subnet == nil && floatingIp.SubnetID > 0 {
		subnet := &model.Subnet{Model: model.Model{ID: floatingIp.SubnetID}}
		err := db.Take(subnet).Error
		if err != nil {
			logger.Errorf("Failed to load subnet for floating ip %d: %v", floatingIp.ID, err)
			return NewCLError(ErrSubnetNotFound, "Failed to load subnet for floating ip", err)
		}
		floatingIp.Subnet = subnet
		logger.Debugf("Loaded subnet for floating ip %d", floatingIp.ID)
	}

	return nil
}
