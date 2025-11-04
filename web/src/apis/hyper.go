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
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var hyperAPI = &HyperAPI{}
var hyperAdmin = &routes.HyperAdmin{}

type HyperAPI struct{}

type HyperResponse struct {
	Hostid       int32   `json:"hostid"`
	Hostname     string  `json:"hostname"`
	Status       int32   `json:"status"`
	StatusName   string  `json:"status_name"`
	Parentid     int32   `json:"parentid"`
	Children     int32   `json:"children"`
	HostIP       string  `json:"host_ip"`
	RouteIP      string  `json:"route_ip"`
	VirtType     string  `json:"virt_type"`
	CpuOverRate  float32 `json:"cpu_over_rate"`
	MemOverRate  float32 `json:"mem_over_rate"`
	DiskOverRate float32 `json:"disk_over_rate"`
	ZoneID       int64   `json:"zone_id"`
	ZoneName     string  `json:"zone_name"`
	Remark       string  `json:"remark"`
	Cpu          int64   `json:"cpu"`
	CpuTotal     int64   `json:"cpu_total"`
	Memory       int64   `json:"memory"`
	MemoryTotal  int64   `json:"memory_total"`
	Disk         int64   `json:"disk"`
	DiskTotal    int64   `json:"disk_total"`
}

type HyperListResponse struct {
	Offset int              `json:"offset"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Hypers []*HyperResponse `json:"hypers"`
}

type HyperPayload struct {
}

type HyperPatchPayload struct {
	Status       *int32   `json:"status" binding:"omitempty,min=0,max=1"`
	ZoneID       *int64   `json:"zone_id" binding:"omitempty,min=1"`
	CpuOverRate  *float32 `json:"cpu_over_rate" binding:"omitempty,min=1"`
	MemOverRate  *float32 `json:"mem_over_rate" binding:"omitempty,min=1"`
	DiskOverRate *float32 `json:"disk_over_rate" binding:"omitempty,min=1"`
	Remark       *string  `json:"remark"`
}

// @Summary get a hypervisor
// @Description get a hypervisor
// @tags Administration
// @Accept  json
// @Produce json
// @Param hostid path string true "Hypervisor host ID"
// @Success 200 {object} HyperResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} common.APIError "Not found"
// @Router /hypers/{hostid} [get]
func (v *HyperAPI) Get(c *gin.Context) {
	hostidStr := c.Param("hostid")
	hostid, err := strconv.ParseInt(hostidStr, 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid hostid parameter", err)
		return
	}

	hyper, err := hyperAdmin.GetHyperByHostid(c.Request.Context(), int32(hostid))
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "IHypervisor not found", err)
		return
	}

	hyperResp := convertHyperToResponse(hyper)
	c.JSON(http.StatusOK, hyperResp)
}

// @Summary list hypervisors
// @Description list hypervisors
// @tags Administration
// @Accept  json
// @Produce json
// @Param offset query int false "Offset for pagination"
// @Param limit query int false "Limit for pagination"
// @Param order query string false "Order by field"
// @Param q query string false "Search query"
// @Success 200 {object} HyperListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /hypers [get]
func (v *HyperAPI) List(c *gin.Context) {
	offset := c.Query("offset")
	limit := c.Query("limit")
	order := c.Query("order")
	query := c.Query("q")

	var offsetInt, limitInt int64
	var err error

	if offset != "" {
		offsetInt, err = strconv.ParseInt(offset, 10, 64)
		if err != nil {
			offsetInt = 0
		}
	}

	if limit != "" {
		limitInt, err = strconv.ParseInt(limit, 10, 64)
		if err != nil {
			limitInt = 16
		}
	} else {
		limitInt = 16
	}

	total, hypers, err := hyperAdmin.List(c.Request.Context(), offsetInt, limitInt, order, query)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}

	hyperResponses := make([]*HyperResponse, len(hypers))
	for i, hyper := range hypers {
		hyperResponses[i] = convertHyperToResponse(hyper)
	}

	hyperListResp := &HyperListResponse{
		Offset: int(offsetInt),
		Total:  int(total),
		Limit:  int(limitInt),
		Hypers: hyperResponses,
	}
	c.JSON(http.StatusOK, hyperListResp)
}

// @Summary update a hypervisor
// @Description update hypervisor status, zone, over-commit rates, and remark
// @tags Administration
// @Accept  json
// @Produce json
// @Param hostid path string true "Hypervisor host ID"
// @Param body body HyperPatchPayload true "Hypervisor update payload"
// @Success 200 {object} HyperResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Failure 404 {object} common.APIError "Not found"
// @Failure 500 {object} common.APIError "Internal server error"
// @Router /hypers/{hostid} [patch]
func (v *HyperAPI) Patch(c *gin.Context) {
	hostidStr := c.Param("hostid")
	hostid64, err := strconv.ParseInt(hostidStr, 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid hostid parameter", err)
		return
	}

	hostid := int32(hostid64)

	var payload HyperPatchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid payload", err)
		return
	}

	// Get existing hypervisor
	hyper, err := hyperAdmin.GetHyperByHostid(c.Request.Context(), hostid)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "IHypervisor not found", err)
		return
	}

	// Update only the fields provided in the payload
	if payload.Status != nil {
		hyper.Status = *payload.Status
	}
	if payload.ZoneID != nil {
		hyper.ZoneID = *payload.ZoneID
	}
	if payload.CpuOverRate != nil {
		hyper.CpuOverRate = *payload.CpuOverRate
	}
	if payload.MemOverRate != nil {
		hyper.MemOverRate = *payload.MemOverRate
	}
	if payload.DiskOverRate != nil {
		hyper.DiskOverRate = *payload.DiskOverRate
	}
	if payload.Remark != nil {
		hyper.Remark = *payload.Remark
	}

	// Update the hypervisor
	if err := hyperAdmin.Update(c.Request.Context(), hyper); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}

	// Get updated hypervisor with Zone preloaded
	updatedHyper, err := hyperAdmin.GetHyperByHostid(c.Request.Context(), hostid)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}

	hyperResp := convertHyperToResponse(updatedHyper)
	c.JSON(http.StatusOK, hyperResp)
}

// convertHyperToResponse converts a model.Hyper to HyperResponse
func convertHyperToResponse(hyper *model.Hyper) *HyperResponse {
	resp := &HyperResponse{
		Hostid:       hyper.Hostid,
		Hostname:     hyper.Hostname,
		Status:       hyper.Status,
		StatusName:   hyper.GetStatus(),
		Parentid:     hyper.Parentid,
		Children:     hyper.Children,
		HostIP:       hyper.HostIP,
		RouteIP:      hyper.RouteIP,
		VirtType:     hyper.VirtType,
		CpuOverRate:  hyper.CpuOverRate,
		MemOverRate:  hyper.MemOverRate,
		DiskOverRate: hyper.DiskOverRate,
		ZoneID:       hyper.ZoneID,
		Remark:       hyper.Remark,
	}

	if hyper.Zone != nil {
		resp.ZoneName = hyper.Zone.Name
	}

	if hyper.Resource != nil {
		resp.Cpu = hyper.Resource.Cpu
		resp.CpuTotal = hyper.Resource.CpuTotal
		resp.Memory = hyper.Resource.Memory / 1024                       // Convert KB to MB
		resp.MemoryTotal = hyper.Resource.MemoryTotal / 1024             // Convert KB to MB
		resp.Disk = hyper.Resource.Disk / (1024 * 1024 * 1024)           // Convert B to GB
		resp.DiskTotal = hyper.Resource.DiskTotal / (1024 * 1024 * 1024) // Convert B to GB
	}

	return resp
}
