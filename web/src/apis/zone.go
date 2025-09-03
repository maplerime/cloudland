/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var zoneAPI = &ZoneAPI{}
var zoneAdmin = &routes.ZoneAdmin{}

type ZoneAPI struct{}

type ZoneResponse struct {
	*ResourceReference
	Default bool   `json:"default"`
	Remark  string `json:"remark"`
}

type ZoneListResponse struct {
	Offset int             `json:"offset"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Zones  []*ZoneResponse `json:"zones"`
}

type ZonePayload struct {
	Name    string `json:"name" binding:"required,min=2,max=32"`
	Default bool   `json:"default"`
	Remark  string `json:"remark" binding:"max=512"`
}

type ZonePatchPayload struct {
	Default bool   `json:"default"`
	Remark  string `json:"remark" binding:"max=512"`
}

// @Summary get a zone
// @Description get a zone
// @tags Zone
// @Accept  json
// @Produce json
// @Success 200 {object} ZoneResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /zones/{name} [get]
func (v *ZoneAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")
	logger.Debugf("Get zone %s", name)
	zone, err := zoneAdmin.GetZoneByName(ctx, name)
	if err != nil {
		logger.Errorf("Failed to get zone %s, %+v", name, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid zone query", err)
		return
	}
	zoneResp, err := v.getZoneResponse(ctx, zone)
	if err != nil {
		logger.Errorf("Failed to create zone response %s, %+v", name, err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Get zone %s success, response: %+v", name, zoneResp)
	c.JSON(http.StatusOK, zoneResp)
}

// @Summary list zones
// @Description list zones
// @tags Zone
// @Accept  json
// @Produce json
// @Success 200 {object} ZoneListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /zones [get]
func (v *ZoneAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	queryStr := c.DefaultQuery("query", "")
	logger.Debugf("List zones with offset %s, limit %s, query %s", offsetStr, limitStr, queryStr)
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
	total, zones, err := zoneAdmin.List(ctx, int64(offset), int64(limit), "name", queryStr)
	if err != nil {
		logger.Errorf("Failed to list zones %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list zones", err)
		return
	}
	zoneListResp := &ZoneListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(zones),
	}
	zoneListResp.Zones = make([]*ZoneResponse, zoneListResp.Limit)
	for i, zone := range zones {
		zoneListResp.Zones[i], err = v.getZoneResponse(ctx, zone)
		if err != nil {
			logger.Errorf("Failed to create zone response %+v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("List zones success, response: %+v", zoneListResp)
	c.JSON(http.StatusOK, zoneListResp)
}

// @Summary create a zone
// @Description create a zone
// @tags Zone
// @Accept  json
// @Produce json
// @Param   message	body   ZonePayload  true   "Zone create payload"
// @Success 200 {object} ZoneResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /zones [post]
func (v *ZoneAPI) Create(c *gin.Context) {
	logger.Debugf("Create zone")
	ctx := c.Request.Context()
	payload := &ZonePayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Invalid input JSON %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	logger.Debugf("Creating zone with payload %+v", payload)
	zone, err := zoneAdmin.Create(ctx, payload.Name, payload.Default, payload.Remark)
	if err != nil {
		logger.Errorf("Not able to create zone %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to create", err)
		return
	}
	zoneResp, err := v.getZoneResponse(ctx, zone)
	if err != nil {
		logger.Errorf("Failed to create zone response %+v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Create zone success, response: %+v", zoneResp)
	c.JSON(http.StatusOK, zoneResp)
}

// @Summary delete a zone
// @Description delete a zone
// @tags Zone
// @Accept  json
// @Produce json
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /zones/{name} [delete]
func (v *ZoneAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")
	logger.Debugf("Delete zone %s", name)
	zone, err := zoneAdmin.GetZoneByName(ctx, name)
	if err != nil {
		logger.Errorf("Failed to get zone %s, %+v", name, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = zoneAdmin.Delete(ctx, zone)
	if err != nil {
		logger.Errorf("Failed to delete zone %s, %+v", name, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (v *ZoneAPI) getZoneResponse(ctx context.Context, zone *model.Zone) (zoneResp *ZoneResponse, err error) {
	zoneResp = &ZoneResponse{
		ResourceReference: &ResourceReference{
			ID:        strconv.FormatInt(zone.ID, 10),
			Name:      zone.Name,
			CreatedAt: zone.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: zone.UpdatedAt.Format(TimeStringForMat),
		},
		Default: zone.Default,
		Remark:  zone.Remark,
	}
	return
}
