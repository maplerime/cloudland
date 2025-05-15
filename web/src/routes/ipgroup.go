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
	"strings"
	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	ipGroupAdmin = &IpGroupAdmin{}
	ipGroupView  = &IpGroupView{}
)

type IpGroupAdmin struct{}

type IpGroupView struct{}

func (a *IpGroupAdmin) Create(ctx context.Context, name string, ipGroupType int) (ipGroup *model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.Create, name=%s, ipGroupType=%d", name, ipGroupType)
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
		logger.Debugf("Exit IpGroupAdmin.Create, ipGroup=%+v, err=%v", ipGroup, err)
	}()
	ipGroup = &model.IpGroup{
		Name:   name,
		TypeID: int64(ipGroupType),
	}
	err = db.Create(ipGroup).Error
	if err != nil {
		logger.Error("Failed to create ipGroup", err)
		return
	}
	err = db.Preload("DictionaryType").Preload("Subnets").Where("id = ?", ipGroup.ID).First(&ipGroup).Error
	if err != nil {
		logger.Error("Error loading IpGroup details after creation:", err)
		return nil, err
	}
	logger.Debugf("IpGroupAdmin.Create: success, ipGroup=%+v", ipGroup)
	return ipGroup, nil
}

func (a *IpGroupAdmin) Get(ctx context.Context, id int64) (ipGroup *model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.Get, id=%d", id)
	if id <= 0 {
		err = fmt.Errorf("Invalid ipGroup ID: %d", id)
		logger.Errorf("%v", err)
		return
	}
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	ipGroup = &model.IpGroup{Model: model.Model{ID: id}}
	err = db.Where(where).Preload("Subnets").Preload("DictionaryType").Take(ipGroup).Error
	if err != nil {
		logger.Errorf("Failed to query ipGroup, %v", err)
		return
	}
	logger.Debugf("IpGroupAdmin.Get: success, ipGroup=%+v", ipGroup)
	return
}

func (a *IpGroupAdmin) Delete(ctx context.Context, ipGroup *model.IpGroup) (err error) {
	logger.Debugf("Enter IpGroupAdmin.Delete, id=%d", ipGroup.ID)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
		logger.Debugf("Exit IpGroupAdmin.Delete, id=%d, err=%v", ipGroup.ID, err)
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, ipGroup.Owner)
	if !permit {
		logger.Error("Not authorized to delete the ip group")
		err = fmt.Errorf("Not authorized")
		return
	}
	if err = db.Delete(ipGroup).Error; err != nil {
		logger.Errorf("DB failed to delete ip group, err=%v", err)
		return
	}
	return
}

func (a *IpGroupAdmin) GetIpGroupByUUID(ctx context.Context, uuID string) (ipGroup *model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.GetIpGroupByUUID, uuID=%s", uuID)
	db := DB()
	ipGroup = &model.IpGroup{}
	err = db.Where("uuid = ?", uuID).Preload("Subnets").Preload("DictionaryType").Take(ipGroup).Error
	if err != nil {
		logger.Errorf("Failed to query ipGroup, %v", err)
		return
	}
	logger.Debugf("IpGroupAdmin.GetIpGroupByUUID: success, uuid=%s, ipGroup=%+v", ipGroup.UUID, ipGroup)
	return
}

func (a *IpGroupAdmin) GetIpGroupByName(ctx context.Context, name string) (ipGroup *model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.GetIpGroupByName, name=%s", name)
	db := DB()
	ipGroup = &model.IpGroup{}
	err = db.Where("name = ?", name).Preload("Subnets").Preload("DictionaryType").Take(ipGroup).Error
	if err != nil {
		logger.Errorf("Failed to query ipGroup, %v", err)
		return
	}
	logger.Debugf("IpGroupAdmin.GetIpGroupByName: success, name=%s, ipGroup=%+v", name, ipGroup)
	return
}

func (a *IpGroupAdmin) GetIpGroup(ctx context.Context, reference *BaseReference) (ipGroup *model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.GetIpGroup, reference=%+v", reference)
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = fmt.Errorf("IpGroup base reference must be provided with either uuid or name")
		logger.Errorf("Exit IpGroupAdmin.GetIpGroup with error")
		return
	}
	if reference.ID != "" {
		ipGroup, err = a.GetIpGroupByUUID(ctx, reference.ID)
		logger.Debugf("Exit IpGroupAdmin.GetIpGroup by UUID, uuid=%s, ipGroup=%+v, err=%v", reference.ID, ipGroup, err)
		return
	}
	logger.Debugf("Exit IpGroupAdmin.GetIpGroup with nil result")
	return
}

func (a *IpGroupAdmin) Update(ctx context.Context, ipGroup *model.IpGroup, name string, ipGroupType int) (ipGroupTemp *model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.Update, id=%d, name=%s, ipGroupType=%d", ipGroup.ID, name, ipGroupType)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
		logger.Debugf("Exit IpGroupAdmin.Update, ipGroup=%+v, err=%v", ipGroup, err)
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to update the IpGroup")
		err = fmt.Errorf("Not Authorized")
		return
	}
	if name != "" && ipGroup.Name != name {
		ipGroup.Name = name
	}
	err = db.Model(&model.IpGroup{}).Where("id = ?", ipGroup.ID).Updates(map[string]interface{}{
		"name":    ipGroup.Name,
		"type_id": ipGroupType,
	}).Error
	if err != nil {
		logger.Errorf("Failed to save ipGroups, err=%v", err)
		return ipGroup, err
	}
	return ipGroup, nil
}

func (a *IpGroupAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, ipGroups []*model.IpGroup, err error) {
	logger.Debugf("Enter IpGroupAdmin.List, offset=%d, limit=%d, order=%s, query=%s", offset, limit, order, query)
	db := DB()
	if limit == 0 {
		limit = 16
	}
	if order == "" {
		order = "created_at"
	}
	ipGroups = []*model.IpGroup{}
	if err = db.Model(&model.IpGroup{}).Where(query).Count(&total).Error; err != nil {
		logger.Errorf("IpGroupAdmin.List: count error, err=%v", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Subnets").Preload("DictionaryType").Where(query).Find(&ipGroups).Error; err != nil {
		logger.Errorf("IpGroupAdmin.List: find error, err=%v", err)
		return
	}
	for _, ipGroup := range ipGroups {
		var names []string
		for _, subnet := range ipGroup.Subnets {
			names = append(names, subnet.Name)
		}
		ipGroup.SubnetNames = strings.Join(names, ",")
	}
	logger.Debugf("IpGroupAdmin.List: success, total=%d, count=%d", total, len(ipGroups))
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
	total, ipGroups, err := ipGroupAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list ipGroup(s)", err)
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
	err = db.Preload("Subnets").Preload("DictionaryType").Take(ipGroup).Error
	if err != nil {
		logger.Error("Failed to query ipgroup", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	var ipGroupTypes []model.Dictionary
	if err := db.Where("category = ?", "ipgroup").Find(&ipGroupTypes).Error; err != nil {
		c.Data["ErrorMsg"] = "Failed to load ipGroup categorys"
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
	ipGroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../ipgroups"
	name := c.QueryTrim("name")
	ipGroupType := c.QueryTrim("type")
	ipGroupTypeInt, err := strconv.Atoi(ipGroupType) // 将 string 转换为 int
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	ipGroup, err := ipGroupAdmin.Get(ctx, int64(ipGroupID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	ipGroup, err = ipGroupAdmin.Update(ctx, ipGroup, name, ipGroupTypeInt)
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
	ipGroupType := c.QueryTrim("ipgrouptype")
	ipGroupTypeInt, err := strconv.Atoi(ipGroupType) // 将 string 转换为 int
	logger.Debugf("ipGroupTypeInt: %d", ipGroupTypeInt)
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	ipGroup, err := ipGroupAdmin.Get(ctx, int64(ipGroupID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	ipGroup, err = ipGroupAdmin.Update(ctx, ipGroup, name, ipGroupTypeInt)
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
		logger.Error("Failed to get ipGroup id ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	ipGroup, err := ipGroupAdmin.Get(ctx, int64(ipGroupID))
	if err != nil {
		logger.Error("Failed to get ipGroup ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = ipGroupAdmin.Delete(ctx, ipGroup)
	if err != nil {
		logger.Error("Failed to delete ipGroup ", err)
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

	_, err = ipGroupAdmin.Create(ctx, name, ipGroupTypeInt)
	if err != nil {
		logger.Error("Failed to create ipGroup, %v", err)
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}
