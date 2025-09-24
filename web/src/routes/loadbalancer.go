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

func CreateVrrpInstance(ctx context.Context, name string, router *model.Router) (vrrpInstance *model.VrrpInstance, err error) {
	ctx, db := GetContextDB(ctx)
	name = fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	vrrpSubnet, err := subnetAdmin.Create(ctx, 0, name, "192.168.196.0/27", "", "", "", "vrrp", "", "", false, router, nil)
	if err != nil {
		logger.Error("Failed to create vrrp subnet")
		return
	}
	memberShip := GetMemberShip(ctx)
	vrrpInstance = &model.VrrpInstance{Model: model.Model{Creater: memberShip.UserID}, Owner: memberShip.OrgID, VrrpSubnetID: vrrpSubnet.ID}
	err = db.Create(vrrpInstance).Error
	if err != nil {
		logger.Error("DB failed to create vrrp instance ", err)
		return
	}
	return
}

func (a *LoadBalancerAdmin) Create(ctx context.Context, name string, router *model.Router) (loadBalancer *model.LoadBalancer, err error) {
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
	vrrpInstance, err := CreateVrrpInstance(ctx, name, router)
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
	if err = db.Preload("Router").Where(where).Take(loadBalancer).Error; err != nil {
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
	if err = db.Delete(loadBalancer).Error; err != nil {
		logger.Error("DB failed to delete load balancer", err)
		err = NewCLError(ErrRouterDeleteFailed, "Failed to delete load balancer", err)
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
	_, err = loadBalancerAdmin.Create(ctx, name, router)
	if err != nil {
		logger.Error("Failed to create load balancer, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
