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
	"web/src/dbs"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var ipgroupAPI = &IpGroupAPI{}
var ipgroupAdmin = &routes.IpGroupAdmin{}

type IpGroupAPI struct{}

type IpGroupType struct {
	Type int `json:"type" binding:"omitempty"`
}

type IpGroupResponse struct {
	*ResourceReference
	Type     int    `json:"type"`
	TypeName string `json:"type_name"`
}

type IpGroupListResponse struct {
	Offset   int                `json:"offset"`
	Total    int                `json:"total"`
	Limit    int                `json:"limit"`
	IpGroups []*IpGroupResponse `json:"subnets"`
}

type IpGroupPayload struct {
	Name string `json:"name" binding:"required,min=2,max=32"`
	IpGroupType
}

type IpGroupPatchPayload struct {
	Name string `json:"name" binding:"required,min=2,max=32"`
	IpGroupType
}

// @Summary get a ipgroup
// @Description get a ipgroup
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} IpGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ipgroup/{id} [get]
func (v *IpGroupAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	ipgroup, err := ipgroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid ip group query", err)
		return
	}
	ipgroupResp, err := v.getIpGroupResponse(ctx, ipgroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, ipgroupResp)
}

// @Summary patch a ipgroup
// @Description patch a ipgroup
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   IpGroupPatchPayload  true   "Subnet patch payload"
// @Success 200 {object} IpGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ipgroups/{id} [patch]
func (v *IpGroupAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	payload := &IpGroupPatchPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("Failed to bind json: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	ipgroup, err := ipgroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid subnet query", err)
		return
	}

	// 校验 TypeID 是否有效
	var dictionaryEntry model.Dictionary
	db := dbs.DB()
	if err := db.Where("id = ? AND type = ?", payload.Type, "ipgroup").First(&dictionaryEntry).Error; err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid type ID", err)
		return
	}

	name := ipgroup.Name
	if payload.Name != "" {
		name = payload.Name
		logger.Debugf("Update name to %s", name)
	}

	ipgrouptype := int(ipgroup.Type.ID)
	if payload.Type != 0 && ipgrouptype != payload.Type {
		ipgrouptype = payload.Type
		logger.Debugf("Update type to %d", ipgrouptype)
	}

	err = ipgroupAdmin.Update(ctx, ipgroup, name, ipgrouptype)
	if err != nil {
		logger.Errorf("Failed to patch ipgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Patch ipgroup failed", err)
		return
	}
	ipgroupResp, err := v.getIpGroupResponse(ctx, ipgroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch ipgroup successfully, %s, %+v", uuID, ipgroupResp)
	c.JSON(http.StatusOK, ipgroupResp)
}

// @Summary delete a ipgroup
// @Description delete a ipgroup
// @tags Network
// @Accept  json
// @Produce json
// @Success 204
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ipgroups/{id} [delete]
func (v *IpGroupAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Delete ipgroup %s", uuID)
	ipgroup, err := ipgroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get ipgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = ipgroupAdmin.Delete(ctx, ipgroup)
	if err != nil {
		logger.Errorf("Failed to delete ipgroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a ipgroup
// @Description create a ipgroup
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   IpGroupPayload  true   "IpGroup create payload"
// @Success 200 {object} IpGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /subnets [post]
func (v *IpGroupAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	payload := &IpGroupPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	// 校验 TypeID 是否有效
	var dictionaryEntry model.Dictionary
	db := dbs.DB()
	if err := db.Where("id = ? AND type = ?", payload.Type, "ipgroup").First(&dictionaryEntry).Error; err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid type ID", err)
		return
	}

	subnet, err := ipgroupAdmin.Create(ctx, payload.Name, payload.Type)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to create ipgroup", err)
		return
	}
	ipgroupResp, err := v.getIpGroupResponse(ctx, subnet)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, ipgroupResp)
}

func (v *IpGroupAPI) getIpGroupResponse(ctx context.Context, ipgroup *model.IpGroup) (ipgroupResp *IpGroupResponse, err error) {
	owner := orgAdmin.GetOrgName(ipgroup.Owner)
	ipgroupResp = &IpGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        ipgroup.UUID,
			Name:      ipgroup.Name,
			Owner:     owner,
			CreatedAt: ipgroup.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: ipgroup.UpdatedAt.Format(TimeStringForMat),
		},
		Type:     int(ipgroup.Type.ID),
		TypeName: ipgroup.Type.Name,
	}
	return
}

// @Summary list ipgroup
// @Description list ipgroup
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} IpGroupListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ipgroups [get]
func (v *IpGroupAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	total, ipgroups, err := ipgroupAdmin.List(ctx, int64(offset), int64(limit), "-created_at", "")
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list ipgroups", err)
		return
	}
	ipgroupListResp := &IpGroupListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(ipgroups),
	}
	ipgroupListResp.IpGroups = make([]*IpGroupResponse, ipgroupListResp.Limit)
	for i, subnet := range ipgroups {
		ipgroupListResp.IpGroups[i], err = v.getIpGroupResponse(ctx, subnet)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
		ipgroupListResp.IpGroups[i].TypeName = subnet.Type.Name
	}
	c.JSON(http.StatusOK, ipgroupListResp)
}
