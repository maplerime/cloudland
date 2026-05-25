/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var ipWhitelistAPI = &IPWhitelistAPI{}
var ipWhitelistAdminAPI = &routes.IPWhitelistAdmin{}

type IPWhitelistAPI struct{}

type IPWhitelistPayload struct {
	InstanceUUID string `json:"instance_uuid" binding:"required"`
	IP           string `json:"ip" binding:"required"`
	Reason       string `json:"reason"`
}

type IPWhitelistResponse struct {
	UUID         string `json:"uuid"`
	InstanceUUID string `json:"instance_uuid"`
	IP           string `json:"ip"`
	Reason       string `json:"reason"`
	CreatedAt    string `json:"created_at"`
}

type IPWhitelistListResponse struct {
	Offset  int                    `json:"offset"`
	Total   int                    `json:"total"`
	Limit   int                    `json:"limit"`
	Entries []*IPWhitelistResponse `json:"entries"`
}

func (a *IPWhitelistAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	payload := &IPWhitelistPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		logger.Errorf("Failed to bind json: %v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	entry, err := ipWhitelistAdminAPI.Create(ctx, payload.InstanceUUID, payload.IP, payload.Reason)
	if err != nil {
		logger.Errorf("Failed to create ip whitelist entry: %v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create ip whitelist entry", err)
		return
	}
	c.JSON(http.StatusOK, &IPWhitelistResponse{
		UUID:         entry.UUID,
		InstanceUUID: entry.InstanceUUID,
		IP:           entry.IP,
		Reason:       entry.Reason,
		CreatedAt:    entry.CreatedAt.Format(TimeStringForMat),
	})
}

func (a *IPWhitelistAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid offset", err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid limit", err)
		return
	}
	total, entries, err := ipWhitelistAdminAPI.List(ctx, int64(offset), int64(limit), queryStr)
	if err != nil {
		logger.Errorf("Failed to list ip whitelist: %v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to list ip whitelist", err)
		return
	}
	resp := &IPWhitelistListResponse{
		Total:   int(total),
		Offset:  offset,
		Limit:   len(entries),
		Entries: make([]*IPWhitelistResponse, 0, len(entries)),
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, &IPWhitelistResponse{
			UUID:         e.UUID,
			InstanceUUID: e.InstanceUUID,
			IP:           e.IP,
			Reason:       e.Reason,
			CreatedAt:    e.CreatedAt.Format(TimeStringForMat),
		})
	}
	c.JSON(http.StatusOK, resp)
}

func (a *IPWhitelistAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("uuid")
	if err := ipWhitelistAdminAPI.DeleteByUUID(ctx, uuid); err != nil {
		logger.Errorf("Failed to delete ip whitelist entry %s: %v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to delete ip whitelist entry", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// Refresh triggers a broadcast of the full whitelist to all compute nodes.
func (a *IPWhitelistAPI) Refresh(c *gin.Context) {
	ctx := c.Request.Context()
	_, entries, err := ipWhitelistAdminAPI.List(ctx, 0, 10000, "")
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Failed to query whitelist", err)
		return
	}
	if len(entries) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "whitelist is empty, nothing to broadcast"})
		return
	}
	// Reuse Create flow to trigger broadcast; just call broadcastAll via a dummy Create+Delete cycle
	// Instead, directly trigger broadcast by calling Delete+Create won't work cleanly.
	// The simplest safe approach: re-call broadcastAll via a helper.
	if err := ipWhitelistAdminAPI.BroadcastAll(ctx); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Failed to broadcast whitelist", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "whitelist broadcast triggered", "count": len(entries)})
}
