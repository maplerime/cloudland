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

func (a *ListenerAdmin) Create(ctx context.Context, name string, port int32, loadBalancer *model.LoadBalancer) (listener *model.Listener, err error) {
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
	listener = &model.Listener{Model: model.Model{Creater: memberShip.UserID}, Owner: owner, Name: name, Port: port, LoadBalancerID: loadBalancer.ID, Status: "available"}
	err = db.Create(listener).Error
	if err != nil {
		logger.Error("DB failed to create listener ", err)
		err = NewCLError(ErrListenerCreateFailed, "Failed to create listener", err)
		return
	}
	return
}

func (a *ListenerAdmin) Get(ctx context.Context, id int64, loadBalancer *model.LoadBalancer) (listener *model.Listener, err error) {
	if id <= 0 {
		logger.Error("returning nil router")
		return
	}
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	listener = &model.Listener{Model: model.Model{ID: id}}
	if err = db.Where(where).Take(listener).Error; err != nil {
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
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, listener.Owner)
	if !permit {
		logger.Error("Not authorized to delete the listener")
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete the router", nil)
		return
	}
	if err = db.Delete(listener).Error; err != nil {
		logger.Error("DB failed to delete listener", err)
		err = NewCLError(ErrRouterDeleteFailed, "Failed to delete router", err)
		return
	}
	return
}

func (a *ListenerAdmin) List(ctx context.Context, offset, limit int64, order string, loadBalancer *model.LoadBalancer) (total int64, listeners []*model.Listener, err error) {
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}
	where := fmt.Sprintf("load_balancer_id = %d", loadBalancer.ID)
	wm := memberShip.GetWhere()
	if wm != "" {
		where = fmt.Sprintf("%s and %s", where, wm)
	}
	listeners = []*model.Listener{}
	if err = db.Model(&model.Listener{}).Where(where).Count(&total).Error; err != nil {
		logger.Error("DB failed to count listeners, %v", err)
		err = NewCLError(ErrSQLSyntaxError, "Failed to count listeners", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(where).Find(&listeners).Error; err != nil {
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
	ctx := c.Req.Context()
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	lbid := c.Params("lbid")
	if lbid == "" {
		logger.Error("Load balancer ID is empty")
		c.Data["ErrorMsg"] = "Load balancer ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancerID, err := strconv.Atoi(lbid)
	if err != nil {
		logger.Error("Invalid load balancer ID", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	total, listeners, err := listenerAdmin.List(c.Req.Context(), offset, limit, order, loadBalancer)
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
	lbid := c.Params("lbid")
	if lbid == "" {
		logger.Error("Load balancer ID is empty")
		c.Data["ErrorMsg"] = "Load balancer ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancerID, err := strconv.Atoi(lbid)
	if err != nil {
		logger.Error("Invalid load balancer ID", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listener, err := listenerAdmin.Get(ctx, int64(listenerID), loadBalancer)
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
		"redirect": "listeners",
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
	c.HTML(200, "listeners_new")
}

func (v *ListenerView) Edit(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	lbid := c.Params("lbid")
	if lbid == "" {
		logger.Error("Load balancer ID is empty")
		c.Data["ErrorMsg"] = "Load balancer ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancerID, err := strconv.Atoi(lbid)
	if err != nil {
		logger.Error("Invalid load balancer ID", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	id := c.Params("id")
	listenerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid listener id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listener, err := listenerAdmin.Get(ctx, int64(listenerID), loadBalancer)
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
	lbid := c.Params("lbid")
	if lbid == "" {
		logger.Error("Load balancer ID is empty")
		c.Data["ErrorMsg"] = "Load balancer ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancerID, err := strconv.Atoi(lbid)
	if err != nil {
		logger.Error("Invalid load balancer ID", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	id := c.Params("id")
	listenerID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid listener id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	listener, err := listenerAdmin.Get(ctx, int64(listenerID), loadBalancer)
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
	lbid := c.Params("lbid")
	if lbid == "" {
		logger.Error("Load balancer ID is empty")
		c.Data["ErrorMsg"] = "Load balancer ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancerID, err := strconv.Atoi(lbid)
	if err != nil {
		logger.Error("Invalid load balancer ID", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	loadBalancer, err := loadBalancerAdmin.Get(ctx, int64(loadBalancerID))
	if err != nil {
		logger.Error("Failed to get load balancer", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	name := c.QueryTrim("name")
	port := c.QueryInt("port")
	if port <= 0 {
		logger.Errorf("Invalid port %d", port)
		c.Data["ErrorMsg"] = "Invalid port"
		c.HTML(404, "404")
		return
	}
	_, err = listenerAdmin.Create(ctx, name, int32(port), loadBalancer)
	if err != nil {
		logger.Error("Failed to create listener, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
