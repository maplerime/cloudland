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
	zoneAdmin = &ZoneAdmin{}
	zoneView  = &ZoneView{}
)

type ZoneAdmin struct{}
type ZoneView struct{}

func (a *ZoneAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, zones []*model.Zone, err error) {
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "name"
	}
	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}

	zones = []*model.Zone{}
	if err = db.Model(&model.Zone{}).Where(query).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Model(&model.Zone{}).Where(query).Find(&zones).Error; err != nil {
		return
	}
	db = db.Offset(0).Limit(-1)
	return
}

func (a *ZoneAdmin) Get(ctx context.Context, id int64) (zone *model.Zone, err error) {
	ctx, db := GetContextDB(ctx)
	zone = &model.Zone{ID: id}
	if err = db.Take(zone).Error; err != nil {
		logger.Error("Failed to query zone", err)
		return
	}
	return
}

func (a *ZoneAdmin) GetZoneByName(ctx context.Context, name string) (zone *model.Zone, err error) {
	ctx, db := GetContextDB(ctx)
	zone = &model.Zone{}
	err = db.Where("name = ?", name).Take(zone).Error
	if err != nil {
		logger.Error("Failed to query zone, %v", err)
		return
	}
	return
}

func (a *ZoneAdmin) Create(ctx context.Context, name string, isDefault bool) (zone *model.Zone, err error) {
	logger.Debugf("Creating zone %s, default: %t", name, isDefault)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to create zone")
		err = fmt.Errorf("Not authorized")
		return
	}

	if isDefault {
		err = db.Model(&model.Zone{}).Where(`"default" = ?`, true).Update("Default", false).Error
		if err != nil {
			logger.Error("Failed to unset existing default zone", err)
			return
		}
	}

	zone = &model.Zone{
		Name:    name,
		Default: isDefault,
	}

	err = db.Create(zone).Error
	if err != nil {
		logger.Error("DB create zone failed, %v", err)
		return

	}

	logger.Debugf("Zone created successfully: %+v", zone)
	return
}

func (a *ZoneAdmin) Update(ctx context.Context, zone *model.Zone, isDefault bool) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to update zone")
		err = fmt.Errorf("Not authorized")
		return
	}

	if isDefault && !zone.Default {
		err = db.Model(&model.Zone{}).Where(`"default" = ? AND id != ?`, true, zone.ID).Update("default", false).Error
		if err != nil {
			logger.Error("Failed to unset existing default zone", err)
			return
		}
	}

	zone.Default = isDefault
	err = db.Model(zone).Updates(zone).Error
	if err != nil {
		logger.Error("Failed to update zone", err)
		return
	}

	logger.Debugf("Zone updated successfully: %+v", zone)
	return
}

func (a *ZoneAdmin) Delete(ctx context.Context, zone *model.Zone) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to delete zone")
		err = fmt.Errorf("Not authorized")
		return
	}

	hyperCount := int64(0)
	err = db.Model(&model.Hyper{}).Where("zone_id = ?", zone.ID).Count(&hyperCount).Error
	if err != nil {
		logger.Error("Failed to count hypervisors in zone", err)
		return
	}
	if hyperCount > 0 {
		logger.Error("Zone cannot be deleted while hypervisors belong to this zone")
		err = fmt.Errorf("Zone cannot be deleted while hypervisors belong to this zone")
		return
	}

	err = db.Delete(zone).Error
	if err != nil {
		logger.Error("Failed to delete zone", err)
		return
	}

	logger.Debugf("Zone deleted successfully: %+v", zone)
	return
}

func (v *ZoneView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
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
		order = "name"
	}
	query := c.QueryTrim("q")
	total, zones, err := zoneAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Zones"] = zones
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "zones")
}

func (v *ZoneView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "zones_new")
}

func (v *ZoneView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../zones"
	name := c.QueryTrim("name")
	isDefault := c.QueryBool("default")
	_, err := zoneAdmin.Create(c.Req.Context(), name, isDefault)
	if err != nil {
		logger.Error("Create zone failed", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *ZoneView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	id := c.Params(":id")
	zoneID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	zone, err := zoneAdmin.Get(c.Req.Context(), int64(zoneID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["Zone"] = zone
	c.HTML(200, "zones_patch")
}

func (v *ZoneView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../zones"
	id := c.Params(":id")
	isDefault := c.QueryBool("default")
	zoneID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	zone, err := zoneAdmin.Get(c.Req.Context(), int64(zoneID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	err = zoneAdmin.Update(c.Req.Context(), zone, isDefault)
	if err != nil {
		logger.Error("Failed to update zone", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *ZoneView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	zoneID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	zone, err := zoneAdmin.Get(ctx, int64(zoneID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = zoneAdmin.Delete(ctx, zone)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "zones",
	})
	return
}
