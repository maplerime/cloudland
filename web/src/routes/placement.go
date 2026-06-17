/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/scheduler"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

// ZoneEnabledOverride carries the explicit enabled setting for one zone.
// Zones not present in this list inherit the global enabled value.
type ZoneEnabledOverride struct {
	ZoneID  string
	Enabled bool
}

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
	c.Data["GlobalEnabled"] = cfg.Enabled

	// Flatten per-zone enabled overrides (only zones with an explicit Enabled setting).
	// *bool pointer cannot be nil-checked in templates, so we expand here.
	var zoneOverrides []ZoneEnabledOverride
	for zoneID, zoneCfg := range cfg.Zones {
		if zoneCfg != nil && zoneCfg.Enabled != nil {
			zoneOverrides = append(zoneOverrides, ZoneEnabledOverride{
				ZoneID:  zoneID,
				Enabled: *zoneCfg.Enabled,
			})
		}
	}
	sort.Slice(zoneOverrides, func(i, j int) bool {
		ni, ei := strconv.Atoi(zoneOverrides[i].ZoneID)
		nj, ej := strconv.Atoi(zoneOverrides[j].ZoneID)
		if ei == nil && ej == nil {
			return ni < nj
		}
		return zoneOverrides[i].ZoneID < zoneOverrides[j].ZoneID
	})
	c.Data["ZoneEnabledOverrides"] = zoneOverrides

	c.Data["AvailableFilters"] = scheduler.GetRegisteredFilters()
	c.Data["AvailableWeighers"] = scheduler.GetRegisteredWeighers()
	// Load recent placement decisions for display
	c.Data["RecentDecisions"] = scheduler.GetRecentDecisions(20)
	c.HTML(200, "placement")
}

// GetConfig returns the current placement config as JSON.
// Supports optional ?zone_id=<id> query parameter:
//   - No zone_id param  → returns global config
//   - zone_id=<id>      → returns the effective merged config for that zone
//     (per-zone overrides merged onto global, or global if no override exists)
//
// GET /placement/config?zone_id=<zone_id>
func (v *PlacementView) GetConfig(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		c.JSON(http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}

	zoneID := c.QueryInt64("zone_id")

	var cfg *scheduler.PlacementConfig
	if zoneID > 0 {
		// Return the effective config for the specified zone (merged with global).
		cfg = scheduler.ResolveZoneConfig(zoneID)
	} else {
		cfg, _ = scheduler.GetCurrentConfig()
	}

	_, loadedAt := scheduler.GetCurrentConfig()
	c.JSON(http.StatusOK, map[string]interface{}{
		"zone_id":            zoneID, // 0 means global config
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
