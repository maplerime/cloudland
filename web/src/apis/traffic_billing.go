/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var trafficBillingAPI = &TrafficBillingAPI{}
var trafficBillingAdminAPI = &routes.TrafficBillingAdmin{}

type TrafficBillingAPI struct{}

// TrafficBillingResponse is a single mapping entry, reused for both List
// items and Create's "data" payload.
type TrafficBillingResponse struct {
	UUID         string `json:"uuid"`
	InstanceUUID string `json:"instance_uuid"`
	CreatedAt    string `json:"created_at"`
}

// TrafficBillingMeta is the pagination block accompanying List's data. The
// single-item lookup reports a fixed one-entry page rather than omitting it,
// so both GET routes share one response shape.
type TrafficBillingMeta struct {
	Total       int64 `json:"total"`
	CurrentPage int   `json:"current_page"`
	PerPage     int   `json:"per_page"`
	TotalPages  int   `json:"total_pages"`
}

// TrafficBillingListResponse is returned by both GET routes.
type TrafficBillingListResponse struct {
	Data []TrafficBillingResponse `json:"data"`
	Meta TrafficBillingMeta       `json:"meta"`
}

// TrafficBillingCreateResponse carries the mapping row just created.
type TrafficBillingCreateResponse struct {
	Status string                 `json:"status"`
	Data   TrafficBillingResponse `json:"data"`
}

// TrafficBillingInstanceRef echoes back the instance the request acted on;
// Delete has no row left to return, so this is all its data holds.
type TrafficBillingInstanceRef struct {
	InstanceUUID string `json:"instance_uuid"`
}

// TrafficBillingDeleteResponse is returned once the mark has been removed.
type TrafficBillingDeleteResponse struct {
	Status string                    `json:"status"`
	Data   TrafficBillingInstanceRef `json:"data"`
}

// TrafficBillingSyncResponse acknowledges the broadcast; it reports that the
// push was issued to every compute node, not that each node has applied it.
type TrafficBillingSyncResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// TrafficBillingErrorResponse is this family's error body. Status and
// InstanceUUID are omitempty because the handlers differ on both: the two GET
// failures carry no "status", and Sync has no instance to name.
type TrafficBillingErrorResponse struct {
	Status       string `json:"status,omitempty"`
	Error        string `json:"error"`
	Code         string `json:"code"`
	InstanceUUID string `json:"instance_uuid,omitempty"`
}

// errorCodeFor maps a CLError's numeric code to the family's string "code"
// constant and HTTP status; anything not explicitly called out here falls
// back to a generic 500/INTERNAL_ERROR, matching how the alarm/adjust
// handlers default unrecognized failures.
func errorCodeFor(err error) (status int, code string) {
	status, code = http.StatusInternalServerError, "INTERNAL_ERROR"
	var clErr *CLError
	if !errors.As(err, &clErr) {
		return
	}
	switch clErr.Code {
	case ErrTrafficBillingAlreadyMarked:
		status, code = http.StatusConflict, "ALREADY_MARKED"
	case ErrTrafficBillingNotFound:
		status, code = http.StatusNotFound, "NOT_FOUND"
	case ErrInstanceNotFound:
		status, code = http.StatusNotFound, "INSTANCE_NOT_FOUND"
	case ErrInstanceInvalidState:
		status, code = http.StatusBadRequest, "INVALID_STATE"
	case ErrPermissionDenied:
		status, code = http.StatusForbidden, "PERMISSION_DENIED"
	}
	return
}

// Create marks an instance as traffic billing. Being called is the only
// signal: no billing-type judgement happens here or in the admin layer below.
// @Summary mark an instance as traffic billing
// @Description Mark one instance as traffic billing, then push the mapping to the
// @Description compute node hosting it so that node begins exporting the instance's
// @Description 15-minute traffic metrics. Being called is the only signal: no
// @Description billing-type judgement happens server side. Takes no request body --
// @Description the instance UUID in the path is the entire input. Rejects an
// @Description instance that is already marked rather than silently succeeding, and
// @Description rejects one with no hypervisor assigned. Admin permission required.
// @tags Compute
// @Accept  json
// @Produce json
// @Param   uuid  path  string  true  "Instance UUID"
// @Success 200 {object} TrafficBillingCreateResponse
// @Failure 400 {object} TrafficBillingErrorResponse "INVALID_STATE: instance has no hypervisor assigned"
// @Failure 403 {object} TrafficBillingErrorResponse "PERMISSION_DENIED: admin permission required"
// @Failure 404 {object} TrafficBillingErrorResponse "INSTANCE_NOT_FOUND: no such instance"
// @Failure 409 {object} TrafficBillingErrorResponse "ALREADY_MARKED: instance is already traffic billing"
// @Failure 500 {object} TrafficBillingErrorResponse "INTERNAL_ERROR: DB write or compute-node push failed"
// @Router /traffic-billing/{uuid} [post]
func (a *TrafficBillingAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("uuid")
	entry, err := trafficBillingAdminAPI.Create(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to mark instance %s as traffic billing: %v", uuid, err)
		status, code := errorCodeFor(err)
		c.JSON(status, TrafficBillingErrorResponse{Status: "error", Error: err.Error(), Code: code, InstanceUUID: uuid})
		return
	}
	c.JSON(http.StatusOK, TrafficBillingCreateResponse{
		Status: "success",
		Data: TrafficBillingResponse{
			UUID:         entry.UUID,
			InstanceUUID: entry.InstanceUUID,
			CreatedAt:    entry.CreatedAt.Format(TimeStringForMat),
		},
	})
}

// @Summary unmark an instance as traffic billing
// @Description Remove one instance's traffic-billing mark and tell its compute node
// @Description to stop exporting the instance's traffic metrics. An instance that was
// @Description never marked is rejected with NOT_FOUND rather than reported as a
// @Description successful no-op, so a typo'd UUID does not look like it worked.
// @Description Admin permission required.
// @tags Compute
// @Accept  json
// @Produce json
// @Param   uuid  path  string  true  "Instance UUID"
// @Success 200 {object} TrafficBillingDeleteResponse
// @Failure 403 {object} TrafficBillingErrorResponse "PERMISSION_DENIED: admin permission required"
// @Failure 404 {object} TrafficBillingErrorResponse "NOT_FOUND: instance is not currently marked as traffic billing"
// @Failure 500 {object} TrafficBillingErrorResponse "INTERNAL_ERROR: DB delete or compute-node push failed"
// @Router /traffic-billing/{uuid} [delete]
func (a *TrafficBillingAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("uuid")
	if err := trafficBillingAdminAPI.Delete(ctx, uuid); err != nil {
		logger.Errorf("Failed to unmark instance %s as traffic billing: %v", uuid, err)
		status, code := errorCodeFor(err)
		c.JSON(status, TrafficBillingErrorResponse{Status: "error", Error: err.Error(), Code: code, InstanceUUID: uuid})
		return
	}
	c.JSON(http.StatusOK, TrafficBillingDeleteResponse{
		Status: "success",
		Data:   TrafficBillingInstanceRef{InstanceUUID: uuid},
	})
}

// List handles both GET /api/v1/traffic-billing (paginated list) and
// GET /api/v1/traffic-billing/:uuid (single-item lookup by instance UUID) --
// same handler serving both routes, matching the pattern already used by
// e.g. AlarmAPI.GetDiskRules/GetBWRules.
// @Summary list traffic billing instances
// @Description List the instances currently marked as traffic billing. The underlying
// @Description query applies no explicit ORDER BY, so ordering within a page is
// @Description whatever the DB returns. Admin permission required.
// @Description
// @Description The same handler also serves GET /traffic-billing/{uuid}, a lookup of
// @Description one instance by its UUID. That variant ignores the paging and query
// @Description parameters below and returns the identical response shape with the one
// @Description matching entry in "data" plus a fixed meta of total/current_page/
// @Description per_page/total_pages = 1; an instance that is not marked comes back as
// @Description 404 NOT_FOUND. It is not documented as a separate operation here
// @Description because one Go handler carries one annotation block.
// @tags Compute
// @Accept  json
// @Produce json
// @Param   page       query  int     false  "Page number, 1-based; values below 1 fall back to 1"  default(1)
// @Param   page_size  query  int     false  "Entries per page, valid range 1-1000; out-of-range values fall back to 20"  default(20)
// @Param   query      query  string  false  "Filter by instance UUID substring (SQL LIKE %query%); empty means no filter"
// @Success 200 {object} TrafficBillingListResponse
// @Failure 403 {object} TrafficBillingErrorResponse "PERMISSION_DENIED: admin permission required"
// @Failure 404 {object} TrafficBillingErrorResponse "NOT_FOUND: only for the /{uuid} variant, instance is not marked as traffic billing"
// @Failure 500 {object} TrafficBillingErrorResponse "INTERNAL_ERROR: DB count or query failed"
// @Router /traffic-billing [get]
func (a *TrafficBillingAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	if uuid := c.Param("uuid"); uuid != "" {
		entry, err := trafficBillingAdminAPI.GetByInstanceUUID(ctx, uuid)
		if err != nil {
			logger.Errorf("Failed to get traffic billing mapping for instance %s: %v", uuid, err)
			status, code := errorCodeFor(err)
			c.JSON(status, TrafficBillingErrorResponse{Error: err.Error(), Code: code, InstanceUUID: uuid})
			return
		}
		c.JSON(http.StatusOK, TrafficBillingListResponse{
			Data: []TrafficBillingResponse{{
				UUID:         entry.UUID,
				InstanceUUID: entry.InstanceUUID,
				CreatedAt:    entry.CreatedAt.Format(TimeStringForMat),
			}},
			Meta: TrafficBillingMeta{Total: 1, CurrentPage: 1, PerPage: 1, TotalPages: 1},
		})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	queryStr := c.DefaultQuery("query", "")
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}
	offset := int64((page - 1) * pageSize)
	limit := int64(pageSize)
	total, entries, err := trafficBillingAdminAPI.List(ctx, offset, limit, queryStr)
	if err != nil {
		logger.Errorf("Failed to list traffic billing mappings: %v", err)
		status, code := errorCodeFor(err)
		c.JSON(status, TrafficBillingErrorResponse{Error: err.Error(), Code: code})
		return
	}
	responseData := make([]TrafficBillingResponse, 0, len(entries))
	for _, e := range entries {
		responseData = append(responseData, TrafficBillingResponse{
			UUID:         e.UUID,
			InstanceUUID: e.InstanceUUID,
			CreatedAt:    e.CreatedAt.Format(TimeStringForMat),
		})
	}
	c.JSON(http.StatusOK, TrafficBillingListResponse{
		Data: responseData,
		Meta: TrafficBillingMeta{
			Total:       total,
			CurrentPage: page,
			PerPage:     pageSize,
			TotalPages:  int(math.Ceil(float64(total) / float64(pageSize))),
		},
	})
}

// Sync takes no input: the DB is always the source of truth. It broadcasts
// the complete "should be traffic billing" domain list to every compute node
// via TrafficBillingAdmin.BroadcastSync -- the exact same call the UI's
// Refresh button makes. There is no mechanism here for an external caller to
// push its own list for the DB to reconcile against; "sync" only ever means
// DB-to-compute-node in this system.
// @Summary broadcast the traffic billing list to all compute nodes
// @Description Push the complete set of instances that should be traffic billing from
// @Description the DB to every compute node at once, so each node rebuilds its local
// @Description traffic-billing map from that list. Use it to repair nodes that drifted
// @Description (missed a create/delete, or were rebuilt). Admin permission required.
// @Description
// @Description Takes no request body and accepts no caller-supplied list: the DB is
// @Description always the source of truth, so "sync" only ever means DB-to-compute-node
// @Description here, never the reverse. Success means the broadcast was dispatched to
// @Description every node, not that every node has finished applying it.
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200 {object} TrafficBillingSyncResponse
// @Failure 403 {object} TrafficBillingErrorResponse "PERMISSION_DENIED: admin permission required"
// @Failure 500 {object} TrafficBillingErrorResponse "INTERNAL_ERROR: DB read or broadcast dispatch failed"
// @Router /traffic-billing/sync [post]
func (a *TrafficBillingAPI) Sync(c *gin.Context) {
	ctx := c.Request.Context()
	if err := trafficBillingAdminAPI.BroadcastSync(ctx); err != nil {
		logger.Errorf("Failed to broadcast traffic billing sync: %v", err)
		status, code := errorCodeFor(err)
		c.JSON(status, TrafficBillingErrorResponse{Status: "error", Error: err.Error(), Code: code})
		return
	}
	c.JSON(http.StatusOK, TrafficBillingSyncResponse{
		Status:  "success",
		Message: "sync broadcast to all compute nodes",
	})
}
