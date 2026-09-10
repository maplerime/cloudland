/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/scheduler"

	"github.com/gin-gonic/gin"
)

var placementAPI = &PlacementAPI{}

type PlacementAPI struct{}

type ValidatePayload struct {
	HyperID  *int32 `json:"hyper_id" binding:"required"`
	VCPUs    int32  `json:"vcpus" binding:"required,gte=1"`
	MemoryMB int64  `json:"memory_mb" binding:"required,gte=1"`
	DiskGB   int64  `json:"disk_gb" binding:"required,gte=0"`
	ZoneID   int64  `json:"zone_id" binding:"omitempty"`
}

// Available returns the list of hypers that can host a VM with the given spec.
// @Summary      Query available placement candidates
// @Description  Runs the full filter+weigher chain and returns all passing hypers sorted by score
// @Tags         Placement
// @Accept       json
// @Produce      json
// @Param        zone_name  query string true  "Zone name"
// @Param        vcpus      query int    true  "Number of vCPUs"
// @Param        memory_mb  query int    true  "Memory in MB"
// @Param        disk_gb    query int    false "Disk in GB (default 1)"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} common.APIError
// @Failure      403 {object} common.APIError
// @Router       /placement/available [get]
func (v *PlacementAPI) Available(c *gin.Context) {
	ctx := c.Request.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return
	}

	zoneName := c.Query("zone_name")
	if zoneName == "" {
		ErrorResponse(c, http.StatusBadRequest, "Invalid or missing zone_name parameter", nil)
		return
	}
	zone, err := zoneAdmin.GetZoneByName(ctx, zoneName)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Zone not found", err)
		return
	}

	vcpus, err := strconv.ParseInt(c.Query("vcpus"), 10, 32)
	if err != nil || vcpus < 1 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid or missing vcpus parameter", err)
		return
	}
	memoryMB, err := strconv.ParseInt(c.Query("memory_mb"), 10, 64)
	if err != nil || memoryMB < 1 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid or missing memory_mb parameter", err)
		return
	}
	diskGB := int64(1)
	if diskStr := c.Query("disk_gb"); diskStr != "" {
		diskGB, err = strconv.ParseInt(diskStr, 10, 64)
		if err != nil || diskGB < 0 {
			ErrorResponse(c, http.StatusBadRequest, "Invalid disk_gb parameter", err)
			return
		}
	}

	req := &scheduler.PlacementRequest{
		VCPUs:  int32(vcpus),
		MemMB:  memoryMB,
		DiskGB: diskGB,
		ZoneID: zone.ID,
	}

	candidates, err := scheduler.QueryAvailableHosts(ctx, req)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to query available hosts", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"candidates": candidates,
		"total":      len(candidates),
		"request": gin.H{
			"vcpus":     req.VCPUs,
			"memory_mb": req.MemMB,
			"disk_gb":   req.DiskGB,
			"zone_name": zone.Name,
			"zone_id":   req.ZoneID,
		},
	})
}

// GetConfig returns the current placement config as JSON.
// Supports optional ?zone_id=<id> query parameter to get the effective merged
// config for a specific zone (per-zone overrides merged onto global).
// @Summary      Get placement configuration
// @Description  Returns global config, or effective merged config for a specific zone
// @Tags         Placement
// @Produce      json
// @Param        zone_id  query int false "Zone ID (0 or omit = global config)"
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} common.APIError
// @Router       /placement/config [get]
func (v *PlacementAPI) GetConfig(c *gin.Context) {
	ctx := c.Request.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return
	}

	zoneID, _ := strconv.ParseInt(c.Query("zone_id"), 10, 64)

	var cfg *scheduler.PlacementConfig
	if zoneID > 0 {
		cfg = scheduler.ResolveZoneConfig(zoneID)
	} else {
		cfg, _ = scheduler.GetCurrentConfig()
	}

	_, loadedAt := scheduler.GetCurrentConfig()
	c.JSON(http.StatusOK, gin.H{
		"zone_id":            zoneID,
		"config":             cfg,
		"loaded_at":          loadedAt,
		"available_filters":  scheduler.GetRegisteredFilters(),
		"available_weighers": scheduler.GetRegisteredWeighers(),
	})
}

// GetDecisions returns recent placement decisions.
// @Summary      Get recent placement decisions
// @Description  Returns the most recent placement scheduling decisions (max 100)
// @Tags         Placement
// @Produce      json
// @Param        limit  query int false "Number of decisions to return (default 20, max 100)"
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} common.APIError
// @Router       /placement/decisions [get]
func (v *PlacementAPI) GetDecisions(c *gin.Context) {
	ctx := c.Request.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return
	}
	_ = ctx

	n, _ := strconv.Atoi(c.Query("limit"))
	if n <= 0 || n > 100 {
		n = 20
	}
	decisions := scheduler.GetRecentDecisions(n)
	c.JSON(http.StatusOK, gin.H{
		"decisions": decisions,
		"count":     len(decisions),
	})
}

// Reload re-reads placement.toml and rebuilds the scheduler chains without restarting.
// @Summary      Reload placement configuration
// @Description  Hot-reloads placement.toml and invalidates the host state cache
// @Tags         Placement
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} common.APIError
// @Failure      500 {object} common.APIError
// @Router       /placement/reload [post]
func (v *PlacementAPI) Reload(c *gin.Context) {
	ctx := c.Request.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return
	}
	_ = ctx

	result, err := scheduler.ReloadConfig()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Config reload failed", err)
		return
	}

	scheduler.InvalidateHostStateCache()

	logger.Infof("placement config reloaded via API by user %s, filters=%v, weighers=%v",
		memberShip.UserName, result.FilterChain, result.WeigherChain)
	c.JSON(http.StatusOK, gin.H{
		"loaded_at":     result.LoadedAt,
		"config_path":   result.ConfigPath,
		"filter_chain":  result.FilterChain,
		"weigher_chain": result.WeigherChain,
	})
}

// Validate checks whether a specific hyper can host a VM with the given spec.
// @Summary      Validate hyper resource availability
// @Description  Checks if the specified hyper passes the filter chain for the given VM spec
// @Tags         Placement
// @Accept       json
// @Produce      json
// @Param        body body ValidatePayload true "Validation request"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} common.APIError
// @Failure      403 {object} common.APIError
// @Router       /placement/validate [post]
func (v *PlacementAPI) Validate(c *gin.Context) {
	ctx := c.Request.Context()
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return
	}

	payload := &ValidatePayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	req := &scheduler.PlacementRequest{
		VCPUs:  payload.VCPUs,
		MemMB:  payload.MemoryMB,
		DiskGB: payload.DiskGB,
		ZoneID: payload.ZoneID,
	}

	err := scheduler.ValidateHostForVM(ctx, *payload.HyperID, req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"valid":    false,
			"hyper_id": *payload.HyperID,
			"reason":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":    true,
		"hyper_id": *payload.HyperID,
	})
}
