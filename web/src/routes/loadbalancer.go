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
	"time"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	loadBalancerAdmin = &LoadBalancerAdmin{}
	loadBalancerView  = &LoadBalancerView{}
)

type LoadBalancerAdmin struct{}
type LoadBalancerView struct{}

type BackendConfig struct {
	BackendURL string `json:"backend_url"`
	Status     string `json:"status"`
}

type ListenerConfig struct {
	Name     string           `json:"name"`
	Mode     string           `json:"mode"`
	Port     int32            `json:"port"`
	Backends []*BackendConfig `json:"backends"`
}

type LoadBalancerConfig struct {
	Listeners []*ListenerConfig `json:"listeners"`
}

type LoadBalancerFloatingIp struct {
	Address string `json:"address"`
	Vlan    int64  `json:"vlan"`
}

func GetVrrpHyperGroup(ctx context.Context, vrrpInstance *model.VrrpInstance) (hyperGroup string, vrrpIface1, vrrpIface2 *model.Interface, err error) {
	ctx, db := GetContextDB(ctx)
	vrrpID := vrrpInstance.ID
	vrrpIface1 = &model.Interface{}
	vrrpIface2 = &model.Interface{}
	err = db.Preload("Address").Where("type = 'vrrp' and name = 'MASTER' and device = ?", vrrpID).Take(vrrpIface1).Error
	if err != nil {
		logger.Error("Failed to query vrrp interface 1", err)
		return
	}
	err = db.Preload("Address").Where("type = 'vrrp' and name = 'BACKUP' and device = ?", vrrpID).Take(vrrpIface2).Error
	if err != nil {
		logger.Error("Failed to query vrrp interface 2", err)
		return
	}
	hyperList := ""
	if vrrpIface1.Hyper >= 0 {
		hyperList = fmt.Sprintf("%d,", vrrpIface1.Hyper)
	}
	if vrrpIface2.Hyper >= 0 {
		hyperList = fmt.Sprintf("%s%d", hyperList, vrrpIface2.Hyper)
	}
	if hyperList == "" {
		err = fmt.Errorf("No valid hyper for vrrp interfaces")
		return
	}
	hyperGroup = fmt.Sprintf("group-vrrp-%d:%s", vrrpID, hyperList)
	return
}

func CreateVrrpConf(ctx context.Context, loadBalancer *model.LoadBalancer) (err error) {
	ctx, db := GetContextDB(ctx)
	if loadBalancer == nil || (loadBalancer.Status != "available") {
		logger.Error("Load balancer is not available")
		err = NewCLError(ErrLoadBalancerNotFound, "Load balancer is not available", nil)
		return
	}
	floatingIps := []*model.FloatingIp{}
	err = db.Preload("Subnet").Where("load_balancer_id = ?", loadBalancer.ID).Find(&floatingIps).Error
	if err != nil {
		logger.Error("DB failed to query load balancer floating ips, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query load balancers", err)
		return
	}
	lbFloatingIps := []*LoadBalancerFloatingIp{}
	for _, fip := range floatingIps {
		lbFloatingIps = append(lbFloatingIps, &LoadBalancerFloatingIp{
			Address: fip.FipAddress,
			Vlan:    fip.Subnet.Vlan,
		})
	}
	routerID := loadBalancer.RouterID
	vrrpID := loadBalancer.VrrpInstanceID
	vrrpVlan := loadBalancer.VrrpInstance.VrrpSubnet.Vlan
	jsonData, err := json.Marshal(lbFloatingIps)
	if err != nil {
		logger.Errorf("Failed to marshal load balancer floating ip json data, %v", err)
		return
	}
	_, vrrpIface1, vrrpIface2, err := GetVrrpHyperGroup(ctx, loadBalancer.VrrpInstance)
	if err != nil {
		logger.Error("No valid hypervisor", err)
		return
	}
	if vrrpIface1.Hyper >= 0 {
		control := fmt.Sprintf("inter=%d", vrrpIface1.Hyper)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' 'MASTER'<<EOF\n%s\nEOF", routerID, vrrpID, vrrpVlan, vrrpIface1.Address.Address, vrrpIface2.Address.Address, jsonData)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Execute MASTER keepalived conf failed", err)
			return
		}
	}
	if vrrpIface2.Hyper >= 0 {
		control := fmt.Sprintf("inter=%d", vrrpIface2.Hyper)
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' 'BACKUP'<<EOF\n%s\nEOF", routerID, vrrpID, vrrpVlan, vrrpIface2.Address.Address, vrrpIface1.Address.Address, jsonData)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Execute BACKUP create keepalived conf failed", err)
			return
		}
	}
	return
}

func CreateVrrpInstance(ctx context.Context, name string, router *model.Router, zone *model.Zone) (vrrpInstance *model.VrrpInstance, err error) {
	ctx, db := GetContextDB(ctx)
	name = fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	var vrrpSubnet *model.Subnet
	if router.VrrpSubnetID > 0 {
		vrrpSubnet, err = subnetAdmin.Get(ctx, router.VrrpSubnetID)
		if err != nil {
			logger.Error("Failed to create vrrp subnet")
			return
		}
	} else {
		vrrpSubnet, err = subnetAdmin.Create(ctx, 0, name, "192.168.196.0/24", "", "", "", "vrrp", "", "", false, router, nil)
		if err != nil {
			logger.Error("Failed to create vrrp subnet")
			return
		}
		router.VrrpSubnetID = vrrpSubnet.ID
		err = db.Model(router).Update("vrrp_subnet_id", vrrpSubnet.ID).Error
		if err != nil {
			logger.Error("DB failed to update router vrrp subnet", err)
			return
		}
	}
	memberShip := GetMemberShip(ctx)
	zoneID := int64(0)
	if zone != nil {
		zoneID = zone.ID
	}
	vrrpInstance = &model.VrrpInstance{Model: model.Model{Creater: memberShip.UserID}, Owner: memberShip.OrgID, VrrpSubnetID: vrrpSubnet.ID, ZoneID: zoneID, RouterID: router.ID}
	err = db.Create(vrrpInstance).Error
	if err != nil {
		logger.Error("DB failed to create vrrp instance ", err)
		return
	}
	vrrpIface1, err := CreateInterface(ctx, vrrpSubnet, vrrpInstance.ID, memberShip.OrgID, -1, 0, 0, "", "", "MASTER", "vrrp", nil, false)
	if err != nil {
		logger.Error("Failed to create vrrp interface 1", err)
		return
	}
	vrrpIface2, err := CreateInterface(ctx, vrrpSubnet, vrrpInstance.ID, memberShip.OrgID, -1, 0, 0, "", "", "BACKUP", "vrrp", nil, false)
	if err != nil {
		logger.Error("Failed to create vrrp interface 1", err)
		return
	}
	control := "inter="
	hyperGroup := ""
	if zone != nil {
		hyperGroup, err = GetHyperGroup(ctx, zone.ID, -1)
		if err != nil {
			logger.Error("Failed to get hyper group", err)
			return
		}
		control = "select=" + hyperGroup
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/set_vrrp_ip.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s' 'MASTER'", router.ID, vrrpInstance.ID, vrrpSubnet.Vlan, vrrpIface1.MacAddr, vrrpIface1.Address.Address, vrrpIface2.MacAddr, vrrpIface2.Address.Address)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Delete vm command execution failed ", err)
		return
	}
	return
}

func (a *LoadBalancerAdmin) Create(ctx context.Context, name string, router *model.Router, zone *model.Zone) (loadBalancer *model.LoadBalancer, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized to create routers")
		err = NewCLError(ErrPermissionDenied, "Not authorized to create routers", nil)
		return
	}
	owner := memberShip.OrgID
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	vrrpInstance, err := CreateVrrpInstance(ctx, name, router, zone)
	if err != nil {
		logger.Error("Failed to create vrrp instance", err)
		err = NewCLError(ErrVrrpInstanceCreateFailed, "Failed to create vrrp instance", err)
		return
	}
	loadBalancer = &model.LoadBalancer{Model: model.Model{Creater: memberShip.UserID}, Owner: owner, Name: name, RouterID: router.ID, VrrpInstanceID: vrrpInstance.ID, Status: "available"}
	err = db.Create(loadBalancer).Error
	if err != nil {
		logger.Error("DB failed to create load balancer ", err)
		err = NewCLError(ErrLoadBalancerCreateFailed, "Failed to create load balancer", err)
		return
	}
	return
}

func (a *LoadBalancerAdmin) Get(ctx context.Context, id int64) (loadBalancer *model.LoadBalancer, err error) {
	if id <= 0 {
		logger.Error("returning nil router")
		return
	}
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	loadBalancer = &model.LoadBalancer{Model: model.Model{ID: id}}
	if err = db.Preload("Router").Preload("VrrpInstance").Preload("VrrpInstance.VrrpSubnet").Preload("Listeners").Preload("Listeners.Backends").Where(where).Take(loadBalancer).Error; err != nil {
		logger.Error("Failed to query load balancer", err)
		err = NewCLError(ErrLoadBalancerNotFound, "Failed to find load balancer", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, loadBalancer.Owner)
	if !permit {
		logger.Error("Not authorized to read the load balancer")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the load balancer", nil)
		return
	}
	return
}

func (a *LoadBalancerAdmin) GetLoadBalancerByUUID(ctx context.Context, uuID string) (loadBalancer *model.LoadBalancer, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	loadBalancer = &model.LoadBalancer{}
	err = db.Preload("Router").Where(where).Where("uuid = ?", uuID).Take(loadBalancer).Error
	if err != nil {
		logger.Error("Failed to query load balancer, %v", err)
		err = NewCLError(ErrRouterNotFound, "Failed to find load balancer", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, loadBalancer.Owner)
	if !permit {
		logger.Error("Not authorized to read the load balancer")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the load balancer", nil)
		return
	}
	return
}

func (a *LoadBalancerAdmin) GetLoadBalancerByName(ctx context.Context, name string) (loadBalancer *model.LoadBalancer, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	loadBalancer = &model.LoadBalancer{}
	err = db.Preload("Router").Where(where).Where("name = ?", name).Take(loadBalancer).Error
	if err != nil {
		logger.Error("Failed to query load balancer, %v", err)
		err = NewCLError(ErrRouterNotFound, "Failed to find load balancer", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, loadBalancer.Owner)
	if !permit {
		logger.Error("Not authorized to read the load balancer")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the load balancer", nil)
		return
	}
	return
}

func (a *LoadBalancerAdmin) GetLoadBalancer(ctx context.Context, reference *BaseReference) (loadBalancer *model.LoadBalancer, err error) {
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = NewCLError(ErrInvalidParameter, "Router base reference must be provided with either uuid or name", nil)
		return
	}
	if reference.ID != "" {
		loadBalancer, err = a.GetLoadBalancerByUUID(ctx, reference.ID)
		return
	}
	if reference.Name != "" {
		loadBalancer, err = a.GetLoadBalancerByName(ctx, reference.Name)
		return
	}
	return
}

func (a *LoadBalancerAdmin) Update(ctx context.Context, loadBalancer *model.LoadBalancer, name string) (lb *model.LoadBalancer, err error) {
	ctx, db := GetContextDB(ctx)
	if loadBalancer.Name != name {
		loadBalancer.Name = name
		if err = db.Model(loadBalancer).Update("name", loadBalancer.Name).Error; err != nil {
			logger.Error("Failed to save load balancer", err)
			err = NewCLError(ErrRouterUpdateFailed, "Failed to update load balancer", err)
			return
		}
	}
	lb = loadBalancer
	return
}

func (a *LoadBalancerAdmin) Delete(ctx context.Context, loadBalancer *model.LoadBalancer) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, loadBalancer.Owner)
	if !permit {
		logger.Error("Not authorized to delete the load balancer")
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete the router", nil)
		return
	}
	intQuery := fmt.Sprintf("load_balancer_id = %d", loadBalancer.ID)
	_, floatingIps, err := floatingIpAdmin.List(ctx, 0, -1, "", "", intQuery)
	if err != nil {
		logger.Error("Failed to list floating ips", err)
		err = NewCLError(ErrFIPListFailed, "Failed to list floating ips", err)
		return
	}
	for _, floatingIp := range floatingIps {
		err = floatingIpAdmin.Delete(ctx, floatingIp)
		if err != nil {
			logger.Error("Failed to delete floating ip", err)
			err = NewCLError(ErrFIPListFailed, "Failed to delete floating ip", err)
			return
		}
	}
	_, listeners, err := listenerAdmin.List(ctx, 0, -1, "", loadBalancer)
	if err != nil {
		logger.Error("Failed to list listeners", err)
		err = NewCLError(ErrListenerListFailed, "Failed to list listeners", err)
		return
	}
	for _, listener := range listeners {
		err = listenerAdmin.Delete(ctx, listener)
		if err != nil {
			logger.Error("Failed to delete listener", err)
			err = NewCLError(ErrListenerDeleteFailed, "Failed to delete listener", err)
			return
		}
	}
	loadBalancer.Name = fmt.Sprintf("%s-%d", loadBalancer.Name, loadBalancer.CreatedAt.Unix())
	err = db.Model(loadBalancer).Update("name", loadBalancer.Name).Error
	if err != nil {
		logger.Error("DB failed to update loadBalancer name", err)
		err = NewCLError(ErrLoadBalancerUpdateFailed, "Failed to update loadBalancer name", err)
		return
	}
	vrrpInstance := loadBalancer.VrrpInstance
	vrrpSubnet := vrrpInstance.VrrpSubnet
	hyperGroup, vrrpIface1, vrrpIface2, err := GetVrrpHyperGroup(ctx, vrrpInstance)
	if err != nil {
		logger.Error("Failed to query vrrp hyper group and interfaces", err)
		return
	}
	control := "toall=" + hyperGroup
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_keepalived_conf.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s'", loadBalancer.RouterID, vrrpInstance.ID, vrrpSubnet.Vlan, vrrpIface1.MacAddr, vrrpIface1.Address.Address, vrrpIface2.MacAddr, vrrpIface2.Address.Address)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Clear keepalived conf command execution failed ", err)
		err = NewCLError(ErrExecuteOnHyperFailed, "Clear keepalived conf command execution failed", err)
		return
	}
	err = DeleteInterface(ctx, vrrpIface1)
	if err != nil {
		logger.Error("DB failed to delete vrrp interface 1", err)
		err = NewCLError(ErrInterfaceDeleteFailed, "Failed to delete vrrp interface 1", err)
		return
	}
	err = DeleteInterface(ctx, vrrpIface2)
	if err != nil {
		logger.Error("DB failed to delete vrrp interface 2", err)
		err = NewCLError(ErrInterfaceDeleteFailed, "Failed to delete vrrp interface 2", err)
		return
	}
	if err = db.Delete(loadBalancer).Error; err != nil {
		logger.Error("DB failed to delete load balancer", err)
		err = NewCLError(ErrLoadBalancerDeleteFailed, "Failed to delete load balancer", err)
		return
	}
	return
}

func (a *LoadBalancerAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, loadBalancers []*model.LoadBalancer, err error) {
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	where := memberShip.GetWhere()
	loadBalancers = []*model.LoadBalancer{}
	if err = db.Model(&model.LoadBalancer{}).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Error("DB failed to count load balancers, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to count load balancers", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Router").Where(where).Where(query).Find(&loadBalancers).Error; err != nil {
		logger.Error("DB failed to query load balancers, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query load balancers", err)
		return
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, loadBalancer := range loadBalancers {
			loadBalancer.OwnerInfo = &model.Organization{Model: model.Model{ID: loadBalancer.Owner}}
			if err = db.Take(loadBalancer.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				err = NewCLError(ErrOwnerNotFound, "Failed to query owner info", err)
				return
			}
		}
	}
	return
}

func (v *LoadBalancerView) List(c *macaron.Context, store session.Store) {
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	queryStr := c.QueryTrim("router_id")
	logger.Debugf("The query parameters is in LoadBalancerView list: query=%s, queryStr=%s", query, queryStr)

	if queryStr != "" {
		redirectURL := fmt.Sprintf("/loadbalancers?router_id=%s", queryStr)
		// Perform the redirect
		c.Redirect(redirectURL)
	}

	total, loadBalancers, err := loadBalancerAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list load balancers, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["LoadBalancers"] = loadBalancers
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "loadbalancers")
}

func (v *LoadBalancerView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		logger.Error("Id is empty")
		c.Data["ErrorMsg"] = "Id is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	loadBalancerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid load balancer id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Not able to get load balancer")
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = loadBalancerAdmin.Delete(ctx, loadBalancer)
	if err != nil {
		logger.Error("Failed to delete load balancer, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "loadbalancers",
	})
	return
}

func (v *LoadBalancerView) New(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, routers, err := routerAdmin.List(ctx, -1, -1, "", "")
	if err != nil {
		logger.Error("Database failed to query routers", err)
		return
	}
	c.Data["Routers"] = routers
	c.HTML(200, "loadbalancers_new")
}

func (v *LoadBalancerView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	loadBalancerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid load balancer id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["LoadBalancer"] = loadBalancer
	c.HTML(200, "loadbalancers_patch")
}

func (v *LoadBalancerView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../loadbalancers"
	id := c.Params("id")
	loadBalancerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid load balancer id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	_, err = loadBalancerAdmin.Update(ctx, loadBalancer, name)
	if err != nil {
		logger.Error("Failed to update load balancer", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *LoadBalancerView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../loadbalancers"
	name := c.QueryTrim("name")
	routerID := c.QueryInt64("router")
	if routerID <= 0 {
		logger.Error("Invalid router")
		c.Data["ErrorMsg"] = "Invalid VPC"
		c.HTML(404, "404")
		return
	}
	router, err := routerAdmin.Get(ctx, routerID)
	if err != nil {
		logger.Error("Get router failed ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(404, "404")
		return
	}
	zoneID := c.QueryInt64("zone")
	var zone *model.Zone
	if zoneID > 0 {
		zone, err = zoneAdmin.Get(ctx, zoneID)
		if err != nil {
			logger.Error("Get zone failed ", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(404, "404")
			return
		}
	}
	_, err = loadBalancerAdmin.Create(ctx, name, router, zone)
	if err != nil {
		logger.Error("Failed to create load balancer, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
