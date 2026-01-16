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

var consistencyGroupAPI = &ConsistencyGroupAPI{}
var consistencyGroupAdmin = &routes.ConsistencyGroupAdmin{}

// ConsistencyGroupAPI handles consistency group API operations
type ConsistencyGroupAPI struct{}

// ConsistencyGroupPayload represents the payload for creating a consistency group
type ConsistencyGroupPayload struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description" binding:"omitempty"`
	Volumes     []string `json:"volumes" binding:"required,min=1"` // Volume UUIDs
}

// ConsistencyGroupPatchPayload represents the payload for updating a consistency group
type ConsistencyGroupPatchPayload struct {
	Name        string `json:"name" binding:"omitempty"`
	Description string `json:"description" binding:"omitempty"`
}

// ConsistencyGroupResponse represents a consistency group response
type ConsistencyGroupResponse struct {
	*ResourceReference
	Description string              `json:"description"`
	Status      string              `json:"status"`
	PoolID      string              `json:"pool_id"`
	WdsCgID     string              `json:"wds_cg_id,omitempty"`
	Volumes     []*BaseReference    `json:"volumes,omitempty"`
}

// ConsistencyGroupListResponse represents a list of consistency groups
type ConsistencyGroupListResponse struct {
	Offset              int                             `json:"offset"`
	Total               int                             `json:"total"`
	Limit               int                             `json:"limit"`
	ConsistencyGroups   []*ConsistencyGroupResponse     `json:"consistency_groups"`
}

// @Summary Get a consistency group
// @Description Get a consistency group by UUID
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id     path    string     true  "Consistency Group UUID"
// @Success 200 {object} ConsistencyGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id} [get]
func (a *ConsistencyGroupAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("id")
	logger.Debugf("Get consistency group by UUID: %s", uuid)

	// Retrieve consistency group by UUID
	// 通过 UUID 获取一致性组
	cg, err := consistencyGroupAdmin.GetByUUID(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to get consistency group by UUID %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid consistency group query", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        cg.ID,
			UUID:      cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt,
			UpdatedAt: cg.UpdatedAt,
		},
		Description: cg.Description,
		Status:      cg.Status.String(),
		PoolID:      cg.PoolID,
		WdsCgID:     cg.WdsCgID,
	}

	// Get associated volumes
	// 获取关联的卷
	db := dbs.DB(ctx)
	var cgVolumes []*model.ConsistencyGroupVolume
	if err = db.Where("cg_id = ?", cg.ID).Preload("Volume").Find(&cgVolumes).Error; err != nil {
		logger.Errorf("Failed to get volumes for CG %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to get volumes", err)
		return
	}

	// Add volume references
	// 添加卷引用
	for _, cgVol := range cgVolumes {
		if cgVol.Volume != nil {
			response.Volumes = append(response.Volumes, &BaseReference{
				ID:   cgVol.Volume.ID,
				UUID: cgVol.Volume.UUID,
				Name: cgVol.Volume.Name,
			})
		}
	}

	logger.Debugf("Successfully retrieved consistency group %s", uuid)
	c.JSON(http.StatusOK, response)
}

// @Summary List consistency groups
// @Description List consistency groups with pagination
// @tags Storage
// @Accept  json
// @Produce json
// @Param   offset  query   int     false  "Offset"
// @Param   limit   query   int     false  "Limit"
// @Param   order   query   string  false  "Order"
// @Param   name    query   string  false  "Name filter"
// @Success 200 {object} ConsistencyGroupListResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups [get]
func (a *ConsistencyGroupAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Debugf("List consistency groups")

	// Parse query parameters
	// 解析查询参数
	offset, limit, order, name := parseListParameters(c)

	// List consistency groups
	// 列出一致性组
	total, cgs, err := consistencyGroupAdmin.List(ctx, int64(offset), int64(limit), order, name)
	if err != nil {
		logger.Errorf("Failed to list consistency groups: %+v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to list consistency groups", err)
		return
	}

	// Build response
	// 构建响应
	var responses []*ConsistencyGroupResponse
	for _, cg := range cgs {
		response := &ConsistencyGroupResponse{
			ResourceReference: &ResourceReference{
				ID:        cg.ID,
				UUID:      cg.UUID,
				Name:      cg.Name,
				CreatedAt: cg.CreatedAt,
				UpdatedAt: cg.UpdatedAt,
			},
			Description: cg.Description,
			Status:      cg.Status.String(),
			PoolID:      cg.PoolID,
			WdsCgID:     cg.WdsCgID,
		}
		responses = append(responses, response)
	}

	result := &ConsistencyGroupListResponse{
		Offset:            offset,
		Total:             int(total),
		Limit:             limit,
		ConsistencyGroups: responses,
	}

	logger.Debugf("Successfully listed %d consistency groups (total: %d)", len(responses), total)
	c.JSON(http.StatusOK, result)
}

// @Summary Create a consistency group
// @Description Create a new consistency group with volumes
// @tags Storage
// @Accept  json
// @Produce json
// @Param   payload  body    ConsistencyGroupPayload  true  "Consistency Group Payload"
// @Success 200 {object} ConsistencyGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups [post]
func (a *ConsistencyGroupAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Debugf("Create consistency group")

	// Parse request body
	// 解析请求体
	var payload ConsistencyGroupPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		logger.Errorf("Failed to parse request body: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid request payload", err)
		return
	}

	logger.Debugf("Creating consistency group: name=%s, volumes=%v", payload.Name, payload.Volumes)

	// Create consistency group
	// 创建一致性组
	cg, err := consistencyGroupAdmin.Create(ctx, payload.Name, payload.Description, payload.Volumes)
	if err != nil {
		logger.Errorf("Failed to create consistency group: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create consistency group", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        cg.ID,
			UUID:      cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt,
			UpdatedAt: cg.UpdatedAt,
		},
		Description: cg.Description,
		Status:      cg.Status.String(),
		PoolID:      cg.PoolID,
		WdsCgID:     cg.WdsCgID,
	}

	logger.Debugf("Successfully created consistency group %s", cg.UUID)
	c.JSON(http.StatusOK, response)
}

// @Summary Update a consistency group
// @Description Update a consistency group's name and description
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id       path    string                        true   "Consistency Group UUID"
// @Param   payload  body    ConsistencyGroupPatchPayload  true   "Update Payload"
// @Success 200 {object} ConsistencyGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id} [patch]
func (a *ConsistencyGroupAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("id")
	logger.Debugf("Update consistency group: %s", uuid)

	// Parse request body
	// 解析请求体
	var payload ConsistencyGroupPatchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		logger.Errorf("Failed to parse request body: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid request payload", err)
		return
	}

	// Get CG by UUID to get ID
	// 通过 UUID 获取 ID
	cg, err := consistencyGroupAdmin.GetByUUID(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to get consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Consistency group not found", err)
		return
	}

	// Update consistency group
	// 更新一致性组
	cg, err = consistencyGroupAdmin.Update(ctx, cg.ID, payload.Name, payload.Description)
	if err != nil {
		logger.Errorf("Failed to update consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to update consistency group", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        cg.ID,
			UUID:      cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt,
			UpdatedAt: cg.UpdatedAt,
		},
		Description: cg.Description,
		Status:      cg.Status.String(),
		PoolID:      cg.PoolID,
		WdsCgID:     cg.WdsCgID,
	}

	logger.Debugf("Successfully updated consistency group %s", uuid)
	c.JSON(http.StatusOK, response)
}

// @Summary Delete a consistency group
// @Description Delete a consistency group
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id     path    string     true  "Consistency Group UUID"
// @Success 204 "No Content"
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id} [delete]
func (a *ConsistencyGroupAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("id")
	logger.Debugf("Delete consistency group: %s", uuid)

	// Get CG by UUID to get ID
	// 通过 UUID 获取 ID
	cg, err := consistencyGroupAdmin.GetByUUID(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to get consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Consistency group not found", err)
		return
	}

	// Delete consistency group
	// 删除一致性组
	err = consistencyGroupAdmin.Delete(ctx, cg.ID)
	if err != nil {
		logger.Errorf("Failed to delete consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to delete consistency group", err)
		return
	}

	logger.Debugf("Successfully deleted consistency group %s", uuid)
	c.Status(http.StatusNoContent)
}
