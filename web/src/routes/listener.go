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

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	listenerAdmin = &ListenerAdmin{}
	listenerView  = &ListenerView{}
)

type ListenerAdmin struct{}
type ListenerView struct{}

func (a *ListenerAdmin) Create(ctx context.Context, name string, loadBalancer *model.LoadBalancer) (listener *model.Listener, err error) {
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
	listener = &model.Listener{Model: model.Model{Creater: memberShip.UserID}, Owner: owner, Name: name, LoadBalancerID: loadBalancer.ID, Status: "available"}
	err = db.Create(listener).Error
	if err != nil {
		logger.Error("DB failed to create listener ", err)
		err = NewCLError(ErrListenerCreateFailed, "Failed to create listener", err)
		return
	}
	return
}

func (a *ListenerAdmin) Get(ctx context.Context, id int64) (listener *model.Listener, err error) {
	if id <= 0 {
		logger.Error("returning nil router")
		return
	}
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	listener = &model.Listener{Model: model.Model{ID: id}}
	if err = db.Preload("Router").Where(where).Take(listener).Error; err != nil {
		logger.Error("Failed to query router", err)
		err = NewCLError(ErrListenerNotFound, "Failed to find router", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, listener.Owner)
	if !permit {
		logger.Error("Not authorized to read the listener")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the listener", nil)
		return
	}
	return
}

func (a *ListenerAdmin) GetListenerByUUID(ctx context.Context, uuID string) (listener *model.Listener, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	listener = &model.Listener{}
	err = db.Preload("Router").Where(where).Where("uuid = ?", uuID).Take(listener).Error
	if err != nil {
		logger.Error("Failed to query listener, %v", err)
		err = NewCLError(ErrRouterNotFound, "Failed to find listener", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, listener.Owner)
	if !permit {
		logger.Error("Not authorized to read the listener")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the listener", nil)
		return
	}
	return
}

func (a *ListenerAdmin) GetListenerByName(ctx context.Context, name string) (listener *model.Listener, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	listener = &model.Listener{}
	err = db.Preload("Router").Where(where).Where("name = ?", name).Take(listener).Error
	if err != nil {
		logger.Error("Failed to query listener, %v", err)
		err = NewCLError(ErrRouterNotFound, "Failed to find listener", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, listener.Owner)
	if !permit {
		logger.Error("Not authorized to read the listener")
		err = NewCLError(ErrPermissionDenied, "Not authorized to read the listener", nil)
		return
	}
	return
}

func (a *ListenerAdmin) GetListener(ctx context.Context, reference *BaseReference) (listener *model.Listener, err error) {
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = NewCLError(ErrInvalidParameter, "Router base reference must be provided with either uuid or name", nil)
		return
	}
	if reference.ID != "" {
		listener, err = a.GetListenerByUUID(ctx, reference.ID)
		return
	}
	if reference.Name != "" {
		listener, err = a.GetListenerByName(ctx, reference.Name)
		return
	}
	return
}

func (a *ListenerAdmin) Update(ctx context.Context, listener *model.Listener, name string) (lb *model.Listener, err error) {
	ctx, db := GetContextDB(ctx)
	if listener.Name != name {
		listener.Name = name
		if err = db.Model(listener).Update("name", listener.Name).Error; err != nil {
			logger.Error("Failed to save listener", err)
			err = NewCLError(ErrRouterUpdateFailed, "Failed to update listener", err)
			return
		}
	}
	lb = listener
	return
}

func (a *ListenerAdmin) Delete(ctx context.Context, listener *model.Listener) (err error) {
	/*
		ctx, db, newTransaction := StartTransaction(ctx)
		defer func() {
			if newTransaction {
				EndTransaction(ctx, err)
			}
		}()
	*/
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, listener.Owner)
	if !permit {
		logger.Error("Not authorized to delete the listener")
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete the router", nil)
		return
	}
	/*
		count := 0
		err = db.Model(&model.FloatingIp{}).Where("router_id = ?", router.ID).Count(&count).Error
		if err != nil {
			logger.Error("Failed to count floating ip")
			err = NewCLError(ErrDatabaseError, "Failed to count floating ip in the router", err)
			return
		}
		if count > 0 {
			logger.Error("There are floating ips")
			err = NewCLError(ErrRouterHasFloatingIPs, "There are associated floating ips", nil)
			return
		}
		count = 0
		err = db.Model(&model.Subnet{}).Where("router_id = ?", router.ID).Count(&count).Error
		if err != nil {
			logger.Error("Failed to count subnet")
			err = NewCLError(ErrDatabaseError, "Failed to count subnet in the router", err)
			return
		}
		if count > 0 {
			logger.Error("There are associated subnets")
			err = NewCLError(ErrRouterHasSubnets, "There are associated subnets", nil)
			return
		}
		err = db.Model(&model.Portmap{}).Where("router_id = ?", router.ID).Count(&count).Error
		if err != nil {
			logger.Error("Failed to count portmap")
			err = NewCLError(ErrDatabaseError, "Failed to count portmap in the router", err)
			return
		}
		if count > 0 {
			logger.Error("There are associated portmaps")
			err = NewCLError(ErrRouterHasPortmaps, "There are associated portmaps", nil)
			return
		}
		control := "toall="
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/clear_local_router.sh '%d'", router.ID)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Delete master failed")
			return
		}
		router.Name = fmt.Sprintf("%s-%d", router.Name, router.CreatedAt.Unix())
		err = db.Model(router).Update("name", router.Name).Error
		if err != nil {
			logger.Error("DB failed to update router name", err)
			err = NewCLError(ErrRouterUpdateFailed, "Failed to update router name", err)
			return
		}
		if err = db.Delete(router).Error; err != nil {
			logger.Error("DB failed to delete router", err)
			err = NewCLError(ErrRouterDeleteFailed, "Failed to delete router", err)
			return
		}
		secgroups := []*model.SecurityGroup{}
		err = db.Where("router_id = ?", router.ID).Find(&secgroups).Error
		if err != nil {
			logger.Error("DB failed to query security groups", err)
			err = NewCLError(ErrDatabaseError, "Failed to query security groups in the router", err)
			return
		}
		for _, sg := range secgroups {
			err = secgroupAdmin.Delete(ctx, sg)
			if err != nil {
				logger.Error("Can not delete security group", err)
				err = NewCLError(ErrSecurityGroupDeleteFailed, "Failed to delete security group", err)
				return
			}
		}
	*/
	return
}

func (a *ListenerAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, listeners []*model.Listener, err error) {
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
	listeners = []*model.Listener{}
	if err = db.Model(&model.Listener{}).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Error("DB failed to count listeners, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to count listeners", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Router").Where(where).Where(query).Find(&listeners).Error; err != nil {
		logger.Error("DB failed to query listeners, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to query listeners", err)
		return
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, listener := range listeners {
			listener.OwnerInfo = &model.Organization{Model: model.Model{ID: listener.Owner}}
			if err = db.Take(listener.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				err = NewCLError(ErrOwnerNotFound, "Failed to query owner info", err)
				return
			}
		}
	}
	return
}

func (v *ListenerView) List(c *macaron.Context, store session.Store) {
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
	logger.Debugf("The query parameters is in ListenerView list: query=%s, queryStr=%s", query, queryStr)

	if queryStr != "" {
		redirectURL := fmt.Sprintf("/listeners?router_id=%s", queryStr)
		// Perform the redirect
		c.Redirect(redirectURL)
	}

	total, listeners, err := listenerAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list listeners, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Listeners"] = listeners
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "listeners")
}

func (v *ListenerView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		logger.Error("Id is empty")
		c.Data["ErrorMsg"] = "Id is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	listenerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid listener id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	listener, err := listenerAdmin.Get(ctx, int64(listenerID))
	if err != nil {
		logger.Error("Not able to get listener")
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = listenerAdmin.Delete(ctx, listener)
	if err != nil {
		logger.Error("Failed to delete listener, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "loadbalancer",
	})
	return
}

func (v *ListenerView) New(c *macaron.Context, store session.Store) {
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
	c.HTML(200, "listeners_new")
}

func (v *ListenerView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	listenerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid listener id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listener, err := listenerAdmin.Get(ctx, int64(listenerID))
	if err != nil {
		logger.Error("Failed to get listener, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["Listener"] = listener
	c.HTML(200, "listeners_patch")
}

func (v *ListenerView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../listeners"
	id := c.Params("id")
	listenerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid listener id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listener, err := listenerAdmin.Get(ctx, int64(listenerID))
	if err != nil {
		logger.Error("Failed to get listener, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	_, err = listenerAdmin.Update(ctx, listener, name)
	if err != nil {
		logger.Error("Failed to update listener", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *ListenerView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../listeners"
	name := c.QueryTrim("name")
	loadBalancerID := c.QueryInt64("loadbalancer")
	if loadBalancerID <= 0 {
		logger.Error("Invalid load balancer")
		c.Data["ErrorMsg"] = "Invalid load balancer"
		c.HTML(404, "404")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, loadBalancerID)
	if err != nil {
		logger.Error("Get router failed ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(404, "404")
		return
	}
	_, err = listenerAdmin.Create(ctx, name, loadBalancer)
	if err != nil {
		logger.Error("Failed to create listener, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
