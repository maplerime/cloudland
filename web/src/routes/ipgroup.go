/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

History:
   Date     Who ID    Description
   -------- --- ---   -----------
   01/13/19 nanjj  Initial code

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
	ipgroupAdmin = &IpGroupAdmin{}
	ipgroupView  = &IpGroupView{}
)

type IpGroupAdmin struct{}

type IpGroupView struct{}

func (a *IpGroupAdmin) Create(ctx context.Context, name string, ipgrouptype string) (ipgroup *model.IpGroup, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to create the user")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	ipgroup = &model.IpGroup{
		Name: name,
		Type: ipgrouptype,
	}
	err = db.Create(ipgroup).Error
	return
}

func (a *IpGroupAdmin) Get(ctx context.Context, id int64) (ipgroup *model.IpGroup, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid ipgroup ID: %d", id)
		logger.Error("%v", err)
		return
	}
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	ipgroup = &model.IpGroup{Model: model.Model{ID: id}}
	err = db.Where(where).Take(ipgroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup, %v", err)
		return
	}
	return
}

func (a *IpGroupAdmin) Delete(ctx context.Context, ipgroup *model.IpGroup) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, ipgroup.Owner)
	if !permit {
		logger.Error("Not authorized to delete the ip group")
		err = fmt.Errorf("Not authorized")
		return
	}
	if err = db.Delete(ipgroup).Error; err != nil {
		logger.Error("DB failed to delete ip group", err)
		return
	}
	return
}

func (a *IpGroupAdmin) GetIpGroupByUUID(ctx context.Context, uuID string) (ipgroup *model.IpGroup, err error) {
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	ipgroup = &model.IpGroup{}
	err = db.Where(where).Where("uuid = ?", uuID).Take(ipgroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup, %v", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, ipgroup.Owner)
	if !permit {
		logger.Error("Not authorized to read the ipgroup")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *IpGroupAdmin) Update(ctx context.Context, ipgroup *model.IpGroup, name string, ipgrouptype string) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if name != "" && ipgroup.Name != name {
		ipgroup.Name = name
	}
	if ipgrouptype != "" && ipgroup.Type != ipgrouptype {
		ipgroup.Type = ipgrouptype
	}
	err = db.Model(ipgroup).Updates(ipgroup).Error
	if err != nil {
		logger.Error("Failed to save ip group", err)
		return
	}
	return
}

func (a *IpGroupAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, ipgroups []*model.IpGroup, err error) {
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}
	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}

	ipgroups = []*model.IpGroup{}
	if err = db.Model(&model.IpGroup{}).Where(query).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(query).Find(&ipgroups).Error; err != nil {
		return
	}

	return
}

func (v *IpGroupView) List(c *macaron.Context, store session.Store) {
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
	total, ipgroups, err := ipgroupAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list ipgroup(s)", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(500)
		return
	}
	pages := GetPages(total, limit)
	c.Data["IpGroups"] = ipgroups
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "ipgroups")
}

func (v *IpGroupView) Edit(c *macaron.Context, store session.Store) {
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ipgroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	ipgroup := &model.IpGroup{Model: model.Model{ID: int64(ipgroupID)}}
	err = db.Set("gorm:auto_preload", true).Take(ipgroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["IpGroup"] = ipgroup
	c.HTML(200, "ipgroups_patch")
}

func (v *IpGroupView) Change(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ipgroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../ipgroups"
	name := c.QueryTrim("name")
	ipgrouptype := c.QueryTrim("type")
	ipgroup, err := ipgroupAdmin.Get(ctx, int64(ipgroupID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	err = ipgroupAdmin.Update(ctx, ipgroup, name, ipgrouptype)
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	c.Redirect(redirectTo)
	return
}

func (v *IpGroupView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	ipgroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../ipgroups"
	name := c.QueryTrim("name")
	ipgrouptype := c.QueryTrim("type")
	ipgroup, err := ipgroupAdmin.Get(ctx, int64(ipgroupID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	err = ipgroupAdmin.Update(ctx, ipgroup, name, ipgrouptype)
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	c.Redirect(redirectTo)
	return
}

func (v *IpGroupView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "id does not exist"
		c.Error(http.StatusBadRequest)
		return
	}
	ipgroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get ipgroup id ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	ipgroup, err := ipgroupAdmin.Get(ctx, int64(ipgroupID))
	if err != nil {
		logger.Error("Failed to get ipgroup ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = ipgroupAdmin.Delete(ctx, ipgroup)
	if err != nil {
		logger.Error("Failed to delete ipgroup ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "/ipgroups",
	})
	return
}

func (v *IpGroupView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "ipgroups_new")
}

func (v *IpGroupView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "/ipgroups"
	name := c.QueryTrim("name")
	ipgrouptype := c.QueryTrim("type")

	_, err := ipgroupAdmin.Create(ctx, name, ipgrouptype)
	if err != nil {
		logger.Error("Failed to create ipgroup, %v", err)
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}
