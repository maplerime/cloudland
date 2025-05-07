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

func (a *IpGroupAdmin) Create(ctx context.Context, name string, ipGroupType int) (ipGroup *model.IpGroup, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to create the IpGroup")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	ipGroup = &model.IpGroup{
		Name:   name,
		TypeID: int64(ipGroupType),
	}
	err = db.Create(ipGroup).Error
	return
}

func (a *IpGroupAdmin) Get(ctx context.Context, id int64) (ipGroup *model.IpGroup, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid ipGroup ID: %d", id)
		logger.Error("%v", err)
		return
	}
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	ipGroup = &model.IpGroup{Model: model.Model{ID: id}}
	err = db.Where(where).Preload("DictionaryType").Take(ipGroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup, %v", err)
		return
	}
	return
}

func (a *IpGroupAdmin) Delete(ctx context.Context, ipGroup *model.IpGroup) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, ipGroup.Owner)
	if !permit {
		logger.Error("Not authorized to delete the ip group")
		err = fmt.Errorf("Not authorized")
		return
	}
	if err = db.Delete(ipGroup).Error; err != nil {
		logger.Error("DB failed to delete ip group", err)
		return
	}
	return
}

func (a *IpGroupAdmin) GetIpGroupByUUID(ctx context.Context, uuID string) (ipGroup *model.IpGroup, err error) {
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	ipGroup = &model.IpGroup{}
	err = db.Where(where).Where("uuid = ?", uuID).Preload("DictionaryType").Take(ipGroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup, %v", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, ipGroup.Owner)
	if !permit {
		logger.Error("Not authorized to read the ipgroup")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *IpGroupAdmin) GetIpGroup(ctx context.Context, reference *BaseReference) (ipGroup *model.IpGroup, err error) {
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = fmt.Errorf("IpGroup base reference must be provided with either uuid or name")
		return
	}
	if reference.ID != "" {
		ipGroup, err = a.GetIpGroupByUUID(ctx, reference.ID)
		return
	}
	return
}

func (a *IpGroupAdmin) Update(ctx context.Context, ipGroup *model.IpGroup, name string, ipGroupType int) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if name != "" && ipGroup.Name != name {
		ipGroup.Name = name
	}
	err = db.Model(&model.IpGroup{}).Where("id = ?", ipGroup.ID).Updates(map[string]interface{}{
		"name":    ipGroup.Name,
		"type_id": ipGroupType,
	}).Error
	if err != nil {
		logger.Error("Failed to save ip group", err)
		return
	}
	return
}

func (a *IpGroupAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, ipGroups []*model.IpGroup, err error) {
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

	ipGroups = []*model.IpGroup{}
	if err = db.Model(&model.IpGroup{}).Where(query).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("DictionaryType").Where(query).Find(&ipGroups).Error; err != nil {
		return
	}

	return
}

func (v *IpGroupView) List(c *macaron.Context, store session.Store) {
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
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	total, ipGroups, err := ipgroupAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list ipgroup(s)", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(500)
		return
	}
	pages := GetPages(total, limit)
	c.Data["IpGroups"] = ipGroups
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
	ipGroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	ipGroup := &model.IpGroup{Model: model.Model{ID: int64(ipGroupID)}}
	err = db.Set("gorm:auto_preload", true).Take(ipGroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	var ipGroupTypes []model.Dictionary
	if err := db.Where("category = ?", "ipgroup").Find(&ipGroupTypes).Error; err != nil {
		c.Data["ErrorMsg"] = "Failed to load ipgroup categorys"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["IpGroup"] = ipGroup
	c.Data["IpGroupTypes"] = ipGroupTypes
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
	ipgrouptypeInt, err := strconv.Atoi(ipgrouptype) // 将 string 转换为 int
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	ipgroup, err := ipgroupAdmin.Get(ctx, int64(ipgroupID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	err = ipgroupAdmin.Update(ctx, ipgroup, name, ipgrouptypeInt)
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
	ipGroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../ipgroups"
	name := c.QueryTrim("name")
	ipgrouptype := c.QueryTrim("ipgrouptype")
	ipgrouptypeInt, err := strconv.Atoi(ipgrouptype) // 将 string 转换为 int
	logger.Debugf("ipgrouptypeInt: %d", ipgrouptypeInt)
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	ipGroup, err := ipgroupAdmin.Get(ctx, int64(ipGroupID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	err = ipgroupAdmin.Update(ctx, ipGroup, name, ipgrouptypeInt)
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
	ipGroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get ipgroup id ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	ipGroup, err := ipgroupAdmin.Get(ctx, int64(ipGroupID))
	if err != nil {
		logger.Error("Failed to get ipgroup ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = ipgroupAdmin.Delete(ctx, ipGroup)
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
	var ipGroupTypes []model.Dictionary
	db := dbs.DB() // 获取数据库连接
	if err := db.Where("category = ?", "ipgroup").Find(&ipGroupTypes).Error; err != nil {
		c.Data["ErrorMsg"] = "Failed to load ipgroup categorys"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["IpGroupTypes"] = ipGroupTypes
	c.HTML(200, "ipgroups_new")
}

func (v *IpGroupView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "/ipgroups"
	name := c.QueryTrim("name")
	ipGroupType := c.QueryTrim("type")
	ipGroupTypeInt, err := strconv.Atoi(ipGroupType) // 将 string 转换为 int
	if err != nil {
		c.HTML(500, err.Error())
		return
	}

	var dictionaryEntry model.Dictionary
	db := dbs.DB() // 获取数据库连接
	if err := db.Where("id = ? AND category = ?", int64(ipGroupTypeInt), "ipgroup").First(&dictionaryEntry).Error; err != nil {
		c.Data["ErrorMsg"] = "Invalid category ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	_, err = ipgroupAdmin.Create(ctx, name, ipGroupTypeInt)
	if err != nil {
		logger.Error("Failed to create ipgroup, %v", err)
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}
