/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"net/http"
	"strconv"
	"web/src/model"

	. "web/src/common"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var hyperAPI = &HyperAPI{}
var hyperAdmin = &routes.HyperAdmin{}

type HyperAPI struct{}

type HyperResponse struct {
	*BaseReference
	Cpu    int64 `json:"cpu"`
	Memory int64 `json:"memory"`
	Disk   int64 `json:"disk"`
	Hostid int32 `json:"hostid"`
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
}

// @Summary get a hypervisor
// @Description get a hypervisor
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} HyperResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /hypers/{name} [get]
func (v *HyperAPI) Get(c *gin.Context) {
	hyperResp := &HyperResponse{}
	c.JSON(http.StatusOK, hyperResp)
}

// @Summary list hypervisors
// @Description list hypervisors
// @tags Administration
// @Accept  json
// @Produce json
// @Success 200 {object} HyperListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /hypers [get]
func (v *HyperAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	orderStr := c.DefaultQuery("order", "-created_at")
	statusStr := c.DefaultQuery("status", "")
	logger.Debugf("List hyper with offset %s, limit %s, query %s, order %s", offsetStr, limitStr, queryStr, orderStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		logger.Errorf("Invalid query offset %s, %+v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("Invalid query limit %s, %+v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		logger.Errorf("Invalid query offset or limit %d, %d", offset, limit)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	total, images, err := hyperAdmin.List(int64(offset), int64(limit), orderStr, queryStr, statusStr)
	if err != nil {
		logger.Errorf("Failed to list hypers %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list hypers", err)
		return
	}
	hyperListResp := &HyperListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(images),
	}
	hyperListResp.Hypers = make([]*HyperResponse, hyperListResp.Limit)
	for i, image := range images {
		hyperListResp.Hypers[i], err = v.getHyperResponse(ctx, image)
		if err != nil {
			logger.Errorf("Failed to create hyper response %+v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List hyper success, response: %+v", hyperListResp)
	c.JSON(http.StatusOK, hyperListResp)
}

func (v *HyperAPI) getHyperResponse(ctx context.Context, hyper *model.Hyper) (hyperResp *HyperResponse, err error) {
	hyperResp = &HyperResponse{
		BaseReference: &BaseReference{
			ID:   strconv.FormatInt(hyper.ID, 10),
			Name: hyper.Hostname,
		},
		Hostid: hyper.Hostid,
		Cpu:    hyper.Resource.Cpu,
		Memory: hyper.Resource.Memory,
		Disk:   hyper.Resource.Disk,
	}
	return
}
