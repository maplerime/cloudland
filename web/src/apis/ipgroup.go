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
	Name        string `json:"name" binding:"required,min=2,max=32"`
	IpGroupType int    `json:"type" binding:"required"`
}

type IpGroupPatchPayload struct {
	Name        string `json:"name" binding:"required,min=2,max=32"`
	IpGroupType int    `json:"type" binding:"required"`
}

// @Summary get a ipGroup
// @Description get a ipGroup
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
	ipGroup, err := ipgroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid ip group query", err)
		return
	}
	ipGroupResp, err := v.getIpGroupResponse(ctx, ipGroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, ipGroupResp)
}

// @Summary patch a ipGroup
// @Description patch a ipGroup
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

	ipGroup, err := ipgroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid ipGroup query", err)
		return
	}

	// 校验 TypeID 是否有效
	var dictionaryEntry model.Dictionary
	db := dbs.DB()
	if err := db.Where("id = ? AND type = ?", payload.IpGroupType, "ipgroup").First(&dictionaryEntry).Error; err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid type ID", err)
		return
	}

	name := ipGroup.Name
	if payload.Name != "" {
		name = payload.Name
		logger.Debugf("Update name to %s", name)
	}

	ipGroupType := int(ipGroup.DictionaryType.ID)
	if payload.IpGroupType != 0 && ipGroupType != payload.IpGroupType {
		ipGroupType = payload.IpGroupType
		logger.Debugf("Update type to %d", ipGroupType)
	}

	err = ipgroupAdmin.Update(ctx, ipGroup, name, ipGroupType)
	if err != nil {
		logger.Errorf("Failed to patch ipGroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Patch ipGroup failed", err)
		return
	}
	ipGroupResp, err := v.getIpGroupResponse(ctx, ipGroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("Patch ipgroup successfully, %s, %+v", uuID, ipGroupResp)
	c.JSON(http.StatusOK, ipGroupResp)
}

// @Summary delete a ipGroup
// @Description delete a ipGroup
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
	logger.Debugf("Delete ipGroup %s", uuID)
	ipGroup, err := ipgroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get ipGroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = ipgroupAdmin.Delete(ctx, ipGroup)
	if err != nil {
		logger.Errorf("Failed to delete ipGroup %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// @Summary create a ipGroup
// @Description create a ipGroup
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   IpGroupPayload  true   "IpGroup create payload"
// @Success 200 {object} IpGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ipgroups [post]
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
	if err := db.Where("id = ? AND type = ?", payload.IpGroupType, "ipgroup").First(&dictionaryEntry).Error; err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid type ID", err)
		return
	}

	ipGroup, err := ipgroupAdmin.Create(ctx, payload.Name, payload.IpGroupType)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to create ipGroup", err)
		return
	}
	ipGroupResp, err := v.getIpGroupResponse(ctx, ipGroup)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, ipGroupResp)
}

func (v *IpGroupAPI) getIpGroupResponse(ctx context.Context, ipGroup *model.IpGroup) (ipGroupResp *IpGroupResponse, err error) {
	owner := orgAdmin.GetOrgName(ipGroup.Owner)
	ipGroupResp = &IpGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        ipGroup.UUID,
			Name:      ipGroup.Name,
			Owner:     owner,
			CreatedAt: ipGroup.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: ipGroup.UpdatedAt.Format(TimeStringForMat),
		},
		Type:     int(ipGroup.DictionaryType.ID),
		TypeName: ipGroup.DictionaryType.Name,
	}
	return
}

// @Summary list ipGroup
// @Description list ipGroup
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
		ipgroupListResp.IpGroups[i].TypeName = subnet.DictionaryType.Name
	}
	c.JSON(http.StatusOK, ipgroupListResp)
}
