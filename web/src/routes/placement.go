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
	"web/src/scheduler"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var placementView = &PlacementView{}

type PlacementView struct{}

// Show renders the placement configuration management page with recent decisions.
// GET /placement
func (v *PlacementView) Show(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cfg, loadedAt := scheduler.GetCurrentConfig()
	c.Data["PlacementConfig"] = cfg
	c.Data["LoadedAt"] = loadedAt.Format("2006-01-02 15:04:05")
	c.Data["AvailableFilters"] = scheduler.GetRegisteredFilters()
	c.Data["AvailableWeighers"] = scheduler.GetRegisteredWeighers()
	// Load recent placement decisions for display
	c.Data["RecentDecisions"] = scheduler.GetRecentDecisions(20)
	c.HTML(200, "placement")
}

// GetConfig returns the current placement config as JSON.
// GET /placement/config
func (v *PlacementView) GetConfig(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		c.JSON(http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}

	cfg, loadedAt := scheduler.GetCurrentConfig()
	c.JSON(http.StatusOK, map[string]interface{}{
		"config":             cfg,
		"loaded_at":          loadedAt,
		"available_filters":  scheduler.GetRegisteredFilters(),
		"available_weighers": scheduler.GetRegisteredWeighers(),
	})
}

// GetDecisions returns recent placement decisions as JSON.
// GET /placement/decisions
func (v *PlacementView) GetDecisions(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		c.JSON(http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}

	n := c.QueryInt("limit")
	if n <= 0 || n > 100 {
		n = 20
	}
	decisions := scheduler.GetRecentDecisions(n)
	c.JSON(http.StatusOK, map[string]interface{}{
		"decisions": decisions,
		"count":     len(decisions),
	})
}

// Reload re-reads the placement config file and rebuilds the scheduler chains.
// POST /placement/reload
func (v *PlacementView) Reload(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	result, err := scheduler.ReloadConfig()
	if err != nil {
		logger.Errorf("placement config reload failed: %v", err)
		c.Data["ErrorMsg"] = fmt.Sprintf("Config reload failed: %v", err)
		c.HTML(500, "error")
		return
	}

	// Invalidate host state cache on config reload
	scheduler.InvalidateHostStateCache()

	logger.Infof("placement config reloaded by user %s, filters=%v, weighers=%v",
		memberShip.UserName, result.FilterChain, result.WeigherChain)
	c.Redirect("/placement")
}
