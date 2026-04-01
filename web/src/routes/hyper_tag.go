/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"
	"net/http"

	. "web/src/common"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	hyperTagAdmin = &HyperTagAdmin{}
	hyperTagView  = &HyperTagView{}
)

// HyperTagAdmin handles business logic for hyper tag management.
type HyperTagAdmin struct{}

// HyperTagView handles HTTP layer for hyper tag management.
type HyperTagView struct{}

// List returns all tags for a given hyper.
func (a *HyperTagAdmin) List(ctx context.Context, hostid int32) (tags []*model.HyperTag, err error) {
	logger.Debugf("HyperTagAdmin.List entry: hostid=%d", hostid)
	_, db := GetContextDB(ctx)
	tags = []*model.HyperTag{}
	if err = db.Where("hostid = ?", hostid).Find(&tags).Error; err != nil {
		logger.Errorf("Failed to list hyper tags for hostid %d: %v", hostid, err)
		return nil, err
	}
	return
}

// Create adds a new tag to a hyper.
func (a *HyperTagAdmin) Create(ctx context.Context, hostid int32, tagName, tagValue string) (tag *model.HyperTag, err error) {
	logger.Debugf("HyperTagAdmin.Create entry: hostid=%d, tag=%s=%s", hostid, tagName, tagValue)
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		return nil, NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	tag = &model.HyperTag{
		Hostid:   hostid,
		TagName:  tagName,
		TagValue: tagValue,
	}
	if err = db.Create(tag).Error; err != nil {
		logger.Errorf("Failed to create hyper tag: %v", err)
		return nil, err
	}
	logger.Infof("HyperTag created: hostid=%d, %s=%s", hostid, tagName, tagValue)
	return
}

// Delete removes a tag by ID.
func (a *HyperTagAdmin) Delete(ctx context.Context, tagID int64) (err error) {
	logger.Debugf("HyperTagAdmin.Delete entry: tagID=%d", tagID)
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		return NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if err = db.Delete(&model.HyperTag{}, tagID).Error; err != nil {
		logger.Errorf("Failed to delete hyper tag %d: %v", tagID, err)
		return
	}
	logger.Infof("HyperTag deleted: id=%d", tagID)
	return
}

// List shows the tag management page for a hyper.
func (v *HyperTagView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	if !memberShip.CheckPermission(model.Admin) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	hostid := int32(c.ParamsInt64(":hostid"))
	if hostid <= 0 {
		c.Data["ErrorMsg"] = "Invalid host ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	// Get hyper info for display
	hyper, err := hyperAdmin.GetHyperByHostid(c.Req.Context(), hostid)
	if err != nil {
		c.Data["ErrorMsg"] = fmt.Sprintf("Hypervisor %d not found", hostid)
		c.HTML(500, "error")
		return
	}
	tags, err := hyperTagAdmin.List(c.Req.Context(), hostid)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "error")
		return
	}
	c.Data["Hyper"] = hyper
	c.Data["Tags"] = tags
	c.Data["Hostid"] = hostid
	c.HTML(200, "hyper_tags")
}

// Create handles the form submission to add a tag.
func (v *HyperTagView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	if !memberShip.CheckPermission(model.Admin) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	hostid := int32(c.ParamsInt64(":hostid"))
	tagName := c.QueryTrim("tag_name")
	tagValue := c.QueryTrim("tag_value")
	if hostid <= 0 || tagName == "" {
		c.Data["ErrorMsg"] = "Invalid parameters"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err := hyperTagAdmin.Create(c.Req.Context(), hostid, tagName, tagValue)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "error")
		return
	}
	c.Redirect(fmt.Sprintf("/hypers/%d/tags", hostid))
}

// Delete handles tag deletion.
func (v *HyperTagView) Delete(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	if !memberShip.CheckPermission(model.Admin) {
		c.Error(http.StatusForbidden)
		return
	}
	tagID := c.ParamsInt64(":id")
	hostid := c.ParamsInt64(":hostid")
	if err := hyperTagAdmin.Delete(c.Req.Context(), tagID); err != nil {
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{"redirect": fmt.Sprintf("/hypers/%d/tags", hostid)})
}
