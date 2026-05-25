/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"fmt"
	"net/http"

	. "web/src/common"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var ipWhitelistView = &IPWhitelistView{}

type IPWhitelistView struct{}

func (v *IPWhitelistView) List(c *macaron.Context, store session.Store) {
	listConfig, offset, limit := GetPaginationParams(c, "ip_whitelists")
	query := c.QueryTrim("q")
	if query != "" {
		query = fmt.Sprintf("ip like '%%%s%%' or instance_uuid like '%%%s%%'", query, query)
	}

	total, entries, err := ipWhitelistAdmin.List(c.Req.Context(), offset, limit, query)
	if err != nil {
		logger.Errorf("Failed to list ip whitelist entries: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["IPWhitelists"] = entries
	c.Data["Query"] = query
	SetPaginationData(c, "ip_whitelists", total, limit, offset, listConfig,
		`["UUID", "InstanceUUID", "IP", "Reason", "Delete"]`,
		[]string{"ID", "UUID", "InstanceUUID", "IP", "Reason", "Owner", "CreatedAt", "Delete"})
	c.HTML(200, "ip_whitelists")
}

func (v *IPWhitelistView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "ip_whitelists_new")
}

func (v *IPWhitelistView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../ip-whitelists"
	instanceUUID := c.QueryTrim("instance_uuid")
	ip := c.QueryTrim("ip")
	reason := c.QueryTrim("reason")
	if instanceUUID == "" || ip == "" {
		c.Data["ErrorMsg"] = "instance_uuid and ip are required"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err := ipWhitelistAdmin.Create(ctx, instanceUUID, ip, reason)
	if err != nil {
		logger.Errorf("Failed to create ip whitelist entry: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}

func (v *IPWhitelistView) Refresh(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	err := ipWhitelistAdmin.BroadcastAll(ctx)
	if err != nil {
		logger.Errorf("Failed to broadcast ip whitelist: %v", err)
		c.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	c.JSON(200, map[string]interface{}{"status": "ok"})
}

func (v *IPWhitelistView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	uuid := c.Params(":uuid")
	if uuid == "" {
		c.Data["ErrorMsg"] = "uuid is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	err = ipWhitelistAdmin.DeleteByUUID(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to delete ip whitelist entry: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "ip-whitelists",
	})
	return
}
