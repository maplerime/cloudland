/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package apis

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var ipGroupAPI = &IpGroupAPI{}
var ipGroupAdmin = &routes.IpGroupAdmin{}

type IpGroupAPI struct{}

type IpGroupResponse struct {
	*ResourceReference
	Type        string         `json:"type"`
	Dictionary  *BaseReference `json:"dictionaries,omitempty"`
	SubnetNames string         `json:"subnet_names"`
}

type IpGroupListResponse struct {
	Offset   int                `json:"offset"`
	Total    int                `json:"total"`
	Limit    int                `json:"limit"`
	IpGroups []*IpGroupResponse `json:"ipgroups"`
}

type IpGroupPayload struct {
	Name        string             `json:"name" binding:"required,min=2,max=32"`
	Type        string             `json:"type" binding:"required,oneof=system resource"`
	IpGroupType *ResourceReference `json:"dictionaries" binding:"omitempty"`
}
type IpGroupPatchPayload struct {
	Name        string             `json:"name" binding:"omitempty,min=2,max=32"`
	Type        string             `json:"type" binding:"omitempty,oneof=system resource"`
	IpGroupType *ResourceReference `json:"dictionaries" binding:"omitempty"`
}

// @Summary get a ipGroup
// @Description get a ipGroup
// @tags Network
// @Accept  json
// @Produce json
// @Success 200 {object} IpGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ip_groups/{id} [get]
func (v *IpGroupAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("IpGroupAPI.Get: uuID=%s", uuID)
	ipGroup, err := ipGroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("IpGroupAPI.Get: invalid ip group query, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid ip group query", err)
		return
	}
	ipGroupResp, err := v.getIpGroupResponse(ctx, ipGroup)
	if err != nil {
		logger.Errorf("IpGroupAPI.Get: getIpGroupResponse error, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("IpGroupAPI.Get: success, uuID=%s, resp=%+v", uuID, ipGroupResp)
	c.JSON(http.StatusOK, ipGroupResp)
}

// @Summary patch a ipGroup
// @Description patch a ipGroup
// @tags Network
// @Accept  json
// @Produce json
// @Param   message	body   IpGroupPatchPayload  true   "IpGroup patch payload"
// @Success 200 {object} IpGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /ip_groups/{id} [patch]
func (v *IpGroupAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	payload := &IpGroupPatchPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("IpGroupAPI.Patch: bind json error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	ipGroup, err := ipGroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("IpGroupAPI.Patch: invalid ipGroup query, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid ipGroup query", err)
		return
	}
	if payload.IpGroupType == nil && payload.Name == "" {
		logger.Errorf("IpGroupAPI.Patch: missing name and dictionaries id")
		ErrorResponse(c, http.StatusBadRequest, "Name or Dictionaries ID is required", err)
		return
	}
	var dictionaryEntry *model.Dictionary
	if payload.IpGroupType != nil {
		if payload.IpGroupType.ID == "" {
			logger.Errorf("IpGroupAPI.Patch: missing dictionaries id")
			ErrorResponse(c, http.StatusBadRequest, "Dictionaries ID is required", err)
			return
		}
		dictionary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, payload.IpGroupType.ID)
		if err != nil {
			logger.Errorf("IpGroupAPI.Patch: invalid dictionaries id, id=%s, err=%v", payload.IpGroupType.ID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid dictionaries ID", err)
			return
		}
		dictionaryEntry = dictionary
	}
	name := ipGroup.Name
	if payload.Name != "" {
		name = payload.Name
		logger.Debugf("IpGroupAPI.Patch: update name to %s", name)
	}
	ipGroupType := int(ipGroup.DictionaryType.ID)
	if dictionaryEntry != nil && ipGroupType != int(dictionaryEntry.ID) {
		ipGroupType = int(dictionaryEntry.ID)
		logger.Debugf("IpGroupAPI.Patch: update type to %d", ipGroupType)
	}
	ipGroup, err = ipGroupAdmin.Update(ctx, ipGroup, name, payload.Type, ipGroupType)
	if err != nil {
		logger.Errorf("IpGroupAPI.Patch: update error, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Patch ipGroup failed", err)
		return
	}
	ipGroupResp, err := v.getIpGroupResponse(ctx, ipGroup)
	if err != nil {
		logger.Errorf("IpGroupAPI.Patch: getIpGroupResponse error, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("IpGroupAPI.Patch: success, uuID=%s, resp=%+v", uuID, ipGroupResp)
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
// @Router /ip_groups/{id} [delete]
func (v *IpGroupAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("IpGroupAPI.Delete: delete ipGroup %s", uuID)
	ipGroup, err := ipGroupAdmin.GetIpGroupByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("IpGroupAPI.Delete: getIpGroupByUUID error, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = ipGroupAdmin.Delete(ctx, ipGroup)
	if err != nil {
		logger.Errorf("IpGroupAPI.Delete: delete error, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	logger.Debugf("IpGroupAPI.Delete: success, uuID=%s", uuID)
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
// @Router /ip_groups [post]
func (v *IpGroupAPI) Create(c *gin.Context) {
	logger.Debugf("Enter IpGroupAPI.Create")
	ctx := c.Request.Context()
	payload := &IpGroupPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("IpGroupAPI.Create: bind json error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	var dictionaryEntry *model.Dictionary
	if payload.IpGroupType != nil {
		if payload.IpGroupType.ID == "" {
			logger.Errorf("IpGroupAPI.Create: missing dictionaries id")
			ErrorResponse(c, http.StatusBadRequest, "Dictionaries ID is required", err)
			return
		}
		dictionary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, payload.IpGroupType.ID)
		if err != nil {
			logger.Errorf("IpGroupAPI.Create: invalid dictionaries id, id=%s, err=%v", payload.IpGroupType.ID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid dictionaries ID", err)
			return
		}
		dictionaryEntry = dictionary
	}

	var dictionaryID int
	if dictionaryEntry != nil {
		dictionaryID = int(dictionaryEntry.ID)
	} else {
		dictionaryID = 0
	}

	ipGroup, err := ipGroupAdmin.Create(ctx, payload.Name, payload.Type, dictionaryID)
	if err != nil {
		logger.Errorf("IpGroupAPI.Create: create error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create ipGroup", err)
		return
	}
	ipGroupResp, err := v.getIpGroupResponse(ctx, ipGroup)
	if err != nil {
		logger.Errorf("IpGroupAPI.Create: getIpGroupResponse error, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("IpGroupAPI.Create: success, resp=%+v", ipGroupResp)
	c.JSON(http.StatusOK, ipGroupResp)
}

func (v *IpGroupAPI) getIpGroupResponse(ctx context.Context, ipGroup *model.IpGroup) (ipGroupResp *IpGroupResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, ipGroup.Owner)

	var names []string
	for _, subnet := range ipGroup.Subnets {
		names = append(names, subnet.Name)
	}
	ipGroup.SubnetNames = strings.Join(names, ",")

	var dictInfo *BaseReference

	if ipGroup.DictionaryType != nil {
		dictInfo = &BaseReference{
			ID:   ipGroup.DictionaryType.UUID,
			Name: ipGroup.DictionaryType.Name,
		}
	}

	ipGroupResp = &IpGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        ipGroup.UUID,
			Name:      ipGroup.Name,
			Owner:     owner,
			CreatedAt: ipGroup.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: ipGroup.UpdatedAt.Format(TimeStringForMat),
		},
		Type:        ipGroup.Type,
		Dictionary:  dictInfo,
		SubnetNames: ipGroup.SubnetNames,
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
// @Router /ip_groups [get]
func (v *IpGroupAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
	queryStr := c.DefaultQuery("query", "")
	dicID := strings.TrimSpace(c.DefaultQuery("dic_id", ""))
	logger.Debugf("IpGroupAPI.List: offset=%s, limit=%s, query=%s, dic_id=%s", offsetStr, limitStr, queryStr, dicID)
	if err != nil {
		logger.Errorf("IpGroupAPI.List: invalid offset, offsetStr=%s, err=%v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("IpGroupAPI.List: invalid limit, limitStr=%s, err=%v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		logger.Errorf("IpGroupAPI.List: invalid offset or limit, offset=%d, limit=%d", offset, limit)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	if queryStr != "" {
		logger.Debugf("IpGroupAPI.List: filter by name like %%s%%", queryStr)
		queryStr = fmt.Sprintf("name like '%%%s%%'", queryStr)
	}
	if dicID != "" {
		logger.Debugf("IpGroupAPI.List: filter by dic_id=%s", dicID)
		var dictionary *model.Dictionary
		dictionary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, dicID)
		if err != nil {
			logger.Errorf("IpGroupAPI.List: invalid dic_id, dicID=%s, err=%v", dicID, err)
			ErrorResponse(c, http.StatusBadRequest, "Invalid query ipGroups by dic_id UUID: "+dicID, err)
			return
		}
		logger.Debugf("IpGroupAPI.List: dictionary found, %+v", dictionary)
		logger.Debugf("IpGroupAPI.List: dic_id in dictionary is %d", dictionary.ID)
		queryStr = fmt.Sprintf("type_id = %d AND type = %s", dictionary.ID, SystemIpGroupType)
	}
	total, ipGroups, err := ipGroupAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("IpGroupAPI.List: list error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list ipGroups", err)
		return
	}
	ipGroupListResp := &IpGroupListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(ipGroups),
	}
	ipGroupListResp.IpGroups = make([]*IpGroupResponse, ipGroupListResp.Limit)
	for i, ipGroup := range ipGroups {
		ipGroupListResp.IpGroups[i], err = v.getIpGroupResponse(ctx, ipGroup)
		if err != nil {
			logger.Errorf("IpGroupAPI.List: getIpGroupResponse error, err=%v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	logger.Debugf("IpGroupAPI.List: success, resp=%+v", ipGroupListResp)
	c.JSON(http.StatusOK, ipGroupListResp)
}
