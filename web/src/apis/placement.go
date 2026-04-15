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
	HyperID  int32 `json:"hyper_id" binding:"required"`
	VCPUs    int32 `json:"vcpus" binding:"required,gte=1"`
	MemoryMB int64 `json:"memory_mb" binding:"required,gte=1"`
	DiskGB   int64 `json:"disk_gb" binding:"required,gte=0"`
	ZoneID   int64 `json:"zone_id" binding:"omitempty"`
}

// Available returns the list of hypers that can host a VM with the given spec.
// @Summary      Query available placement candidates
// @Description  Runs the full filter+weigher chain and returns all passing hypers sorted by score
// @Tags         Placement
// @Accept       json
// @Produce      json
// @Param        zone_id   query int true  "Zone ID"
// @Param        vcpus     query int true  "Number of vCPUs"
// @Param        memory_mb query int true  "Memory in MB"
// @Param        disk_gb   query int true  "Disk in GB"
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

	zoneID, err := strconv.ParseInt(c.Query("zone_id"), 10, 64)
	if err != nil || zoneID <= 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid or missing zone_id parameter", err)
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
	diskGB, err := strconv.ParseInt(c.Query("disk_gb"), 10, 64)
	if err != nil || diskGB < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid or missing disk_gb parameter", err)
		return
	}

	req := &scheduler.PlacementRequest{
		VCPUs:  int32(vcpus),
		MemMB:  memoryMB,
		DiskGB: diskGB,
		ZoneID: zoneID,
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
			"zone_id":   req.ZoneID,
		},
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

	err := scheduler.ValidateHostForVM(ctx, payload.HyperID, req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"valid":    false,
			"hyper_id": payload.HyperID,
			"reason":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":    true,
		"hyper_id": payload.HyperID,
	})
}
