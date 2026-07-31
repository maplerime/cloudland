/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"net/http"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var trafficBillingView = &TrafficBillingView{}

type TrafficBillingView struct{}

// List/New/Create/Delete/Sync are thin macaron<->template wrappers around
// trafficBillingAdmin (web/src/routes/traffic_billing.go), the same base
// layer apis/traffic_billing.go calls for the REST API. Authorization
// (Admin-only) is enforced once, inside trafficBillingAdmin itself -- it is
// NOT re-checked here, so there is a single source of truth for it.
func (v *TrafficBillingView) List(c *macaron.Context, store session.Store) {
	listConfig, offset, limit := GetPaginationParams(c, "traffic_billing")
	// Pass the raw search term through as-is: TrafficBillingAdmin.List does
	// its own "%...%" wrapping and binds it as a query parameter (it does not
	// accept a pre-built SQL fragment the way some other Admin.List methods
	// in this codebase do -- e.g. IPWhitelistAdmin.List's query is a literal
	// WHERE clause). Wrapping it here too would double-wrap it into a string
	// that can never match any real instance_uuid.
	query := c.QueryTrim("q")
	total, entries, err := trafficBillingAdmin.List(c.Req.Context(), offset, limit, query)
	if err != nil {
		logger.Errorf("Failed to list traffic billing mappings: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Data["TrafficBillings"] = entries
	c.Data["Query"] = query
	SetPaginationData(c, "traffic_billing", total, limit, offset, listConfig,
		`["ID", "UUID", "InstanceUUID", "CreatedAt", "Delete"]`,
		[]string{"ID", "UUID", "InstanceUUID", "CreatedAt", "Delete"})
	c.HTML(200, "traffic_billing")
}

func (v *TrafficBillingView) New(c *macaron.Context, store session.Store) {
	c.HTML(200, "traffic_billing_new")
}

func (v *TrafficBillingView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../traffic-billing"
	instanceUUID := c.QueryTrim("instance_uuid")
	if instanceUUID == "" {
		c.Data["ErrorMsg"] = "instance_uuid is required"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	_, err := trafficBillingAdmin.Create(ctx, instanceUUID)
	if err != nil {
		logger.Errorf("Failed to mark instance as traffic billing: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}

func (v *TrafficBillingView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	uuid := c.Params(":uuid")
	if uuid == "" {
		c.Data["ErrorMsg"] = "uuid is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	err = trafficBillingAdmin.Delete(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to unmark traffic billing: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "traffic-billing",
	})
	return
}

// Refresh takes no input: it broadcasts the DB's current "should be traffic
// billing" domain list to every compute node via TrafficBillingAdmin.BroadcastSync,
// which rebuilds each node's local metric file from that list -- a domain the
// DB no longer has gets dropped wherever it's still sitting on disk, a domain
// the DB has gets (re)added wherever that VM actually still lives. Named to
// match IPWhitelistView.Refresh (same button/route naming convention). The DB
// is already the source of truth (visible right there in the list above);
// there is no separate mechanism anywhere in this system for an external
// caller to push its own list for the DB to reconcile against -- the REST
// API's POST /api/v1/traffic-billing/sync calls this exact same method.
func (v *TrafficBillingView) Refresh(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	if err := trafficBillingAdmin.BroadcastSync(ctx); err != nil {
		logger.Errorf("Failed to broadcast traffic billing refresh: %v", err)
		c.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	c.JSON(200, map[string]interface{}{"message": "refresh broadcast to all compute nodes"})
}
