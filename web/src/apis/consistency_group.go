/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
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

// ConsistencyGroupVolumesPayload represents the payload for adding volumes to a consistency group
type ConsistencyGroupVolumesPayload struct {
	Volumes []string `json:"volumes" binding:"required,min=1"` // Volume UUIDs
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
			ID:        cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: cg.UpdatedAt.Format(TimeStringForMat),
		},
		Description: cg.Description,
		Status:      cg.Status.String(),
		PoolID:      cg.PoolID,
		WdsCgID:     cg.WdsCgID,
	}

	// Get associated volumes
	// 获取关联的卷
	db := dbs.DB()
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
				ID:   cgVol.Volume.UUID,
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
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	order := c.DefaultQuery("order", "")
	name := c.DefaultQuery("name", "")

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		offset = 0
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 50
	}

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
				ID:        cg.UUID,
				Name:      cg.Name,
				CreatedAt: cg.CreatedAt.Format(TimeStringForMat),
				UpdatedAt: cg.UpdatedAt.Format(TimeStringForMat),
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
			ID:        cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: cg.UpdatedAt.Format(TimeStringForMat),
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
			ID:        cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: cg.UpdatedAt.Format(TimeStringForMat),
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

// @Summary Add volumes to a consistency group
// @Description Add volumes to an existing consistency group
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id       path    string                          true   "Consistency Group UUID"
// @Param   payload  body    ConsistencyGroupVolumesPayload  true   "Volumes Payload"
// @Success 200 {object} ConsistencyGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/volumes [post]
func (a *ConsistencyGroupAPI) AddVolumes(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("id")
	logger.Debugf("Add volumes to consistency group: %s", uuid)

	// Parse request body
	// 解析请求体
	var payload ConsistencyGroupVolumesPayload
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

	// Add volumes to consistency group
	// 向一致性组添加卷
	cg, err = consistencyGroupAdmin.AddVolumes(ctx, cg.ID, payload.Volumes)
	if err != nil {
		logger.Errorf("Failed to add volumes to consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to add volumes to consistency group", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: cg.UpdatedAt.Format(TimeStringForMat),
		},
		Description: cg.Description,
		Status:      cg.Status.String(),
		PoolID:      cg.PoolID,
		WdsCgID:     cg.WdsCgID,
	}

	logger.Debugf("Successfully added volumes to consistency group %s", uuid)
	c.JSON(http.StatusOK, response)
}

// @Summary Remove a volume from a consistency group
// @Description Remove a volume from an existing consistency group
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id         path    string     true  "Consistency Group UUID"
// @Param   volume_id  path    string     true  "Volume UUID"
// @Success 200 {object} ConsistencyGroupResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/volumes/{volume_id} [delete]
func (a *ConsistencyGroupAPI) RemoveVolume(c *gin.Context) {
	ctx := c.Request.Context()
	uuid := c.Param("id")
	volumeUUID := c.Param("volume_id")
	logger.Debugf("Remove volume %s from consistency group: %s", volumeUUID, uuid)

	// Get CG by UUID to get ID
	// 通过 UUID 获取 ID
	cg, err := consistencyGroupAdmin.GetByUUID(ctx, uuid)
	if err != nil {
		logger.Errorf("Failed to get consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Consistency group not found", err)
		return
	}

	// Remove volume from consistency group
	// 从一致性组删除卷
	cg, err = consistencyGroupAdmin.RemoveVolume(ctx, cg.ID, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to remove volume from consistency group %s: %+v", uuid, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to remove volume from consistency group", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupResponse{
		ResourceReference: &ResourceReference{
			ID:        cg.UUID,
			Name:      cg.Name,
			CreatedAt: cg.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: cg.UpdatedAt.Format(TimeStringForMat),
		},
		Description: cg.Description,
		Status:      cg.Status.String(),
		PoolID:      cg.PoolID,
		WdsCgID:     cg.WdsCgID,
	}

	logger.Debugf("Successfully removed volume from consistency group %s", uuid)
	c.JSON(http.StatusOK, response)
}

// ConsistencyGroupSnapshotPayload represents the payload for creating a CG snapshot
type ConsistencyGroupSnapshotPayload struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description" binding:"omitempty"`
}

// ConsistencyGroupSnapshotResponse represents a CG snapshot response
type ConsistencyGroupSnapshotResponse struct {
	*ResourceReference
	Description string `json:"description"`
	Status      string `json:"status"`
	CGID        string `json:"cg_id"`
	Size        int64  `json:"size"`
	WdsSnapID   string `json:"wds_snap_id,omitempty"`
}

// ConsistencyGroupSnapshotListResponse represents a list of CG snapshots
type ConsistencyGroupSnapshotListResponse struct {
	Offset    int                                 `json:"offset"`
	Total     int                                 `json:"total"`
	Limit     int                                 `json:"limit"`
	Snapshots []*ConsistencyGroupSnapshotResponse `json:"snapshots"`
}

// ConsistencyGroupRestoreResponse represents a restore operation response
type ConsistencyGroupRestoreResponse struct {
	TaskID   string `json:"task_id"`
	TaskUUID string `json:"task_uuid"`
	Status   string `json:"status"`
}

// @Summary List consistency group snapshots
// @Description List snapshots for a consistency group with pagination
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id      path    string  true   "Consistency Group UUID"
// @Param   offset  query   int     false  "Offset"
// @Param   limit   query   int     false  "Limit"
// @Param   order   query   string  false  "Order"
// @Success 200 {object} ConsistencyGroupSnapshotListResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/snapshots [get]
func (a *ConsistencyGroupAPI) ListSnapshots(c *gin.Context) {
	ctx := c.Request.Context()
	cgUUID := c.Param("id")
	logger.Debugf("List snapshots for consistency group: %s", cgUUID)

	// Get CG by UUID to get ID
	// 通过 UUID 获取 ID
	cg, err := consistencyGroupAdmin.GetByUUID(ctx, cgUUID)
	if err != nil {
		logger.Errorf("Failed to get consistency group %s: %+v", cgUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Consistency group not found", err)
		return
	}

	// Parse query parameters
	// 解析查询参数
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	order := c.DefaultQuery("order", "")

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		offset = 0
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 50
	}

	// List snapshots
	// 列出快照
	total, snapshots, err := consistencyGroupAdmin.ListSnapshots(ctx, cg.ID, int64(offset), int64(limit), order)
	if err != nil {
		logger.Errorf("Failed to list snapshots for CG %s: %+v", cgUUID, err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to list snapshots", err)
		return
	}

	// Build response
	// 构建响应
	var responses []*ConsistencyGroupSnapshotResponse
	for _, snapshot := range snapshots {
		response := &ConsistencyGroupSnapshotResponse{
			ResourceReference: &ResourceReference{
				ID:        snapshot.UUID,
				Name:      snapshot.Name,
				CreatedAt: snapshot.CreatedAt.Format(TimeStringForMat),
				UpdatedAt: snapshot.UpdatedAt.Format(TimeStringForMat),
			},
			Description: snapshot.Description,
			Status:      snapshot.Status.String(),
			CGID:        cgUUID,
			Size:        snapshot.Size,
			WdsSnapID:   snapshot.WdsSnapID,
		}
		responses = append(responses, response)
	}

	result := &ConsistencyGroupSnapshotListResponse{
		Offset:    offset,
		Total:     int(total),
		Limit:     limit,
		Snapshots: responses,
	}

	logger.Debugf("Successfully listed %d snapshots (total: %d)", len(responses), total)
	c.JSON(http.StatusOK, result)
}

// @Summary Get a consistency group snapshot
// @Description Get a consistency group snapshot by UUID
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id       path    string  true  "Consistency Group UUID"
// @Param   snap_id  path    string  true  "Snapshot UUID"
// @Success 200 {object} ConsistencyGroupSnapshotResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/snapshots/{snap_id} [get]
func (a *ConsistencyGroupAPI) GetSnapshot(c *gin.Context) {
	ctx := c.Request.Context()
	cgUUID := c.Param("id")
	snapUUID := c.Param("snap_id")
	logger.Debugf("Get snapshot %s for consistency group %s", snapUUID, cgUUID)

	// Get CG by UUID
	// 通过 UUID 获取一致性组
	cg, err := consistencyGroupAdmin.GetByUUID(ctx, cgUUID)
	if err != nil {
		logger.Errorf("Failed to get consistency group %s: %+v", cgUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Consistency group not found", err)
		return
	}

	// Get snapshot
	// 获取快照
	snapshot, err := consistencyGroupAdmin.GetSnapshotByUUID(ctx, snapUUID)
	if err != nil {
		logger.Errorf("Failed to get snapshot %s: %+v", snapUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Snapshot not found", err)
		return
	}

	// Verify snapshot belongs to the CG
	// 验证快照属于该一致性组
	if snapshot.CGID != cg.ID {
		logger.Errorf("Snapshot %s does not belong to CG %s", snapUUID, cgUUID)
		ErrorResponse(c, http.StatusBadRequest, "Snapshot does not belong to this consistency group", nil)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupSnapshotResponse{
		ResourceReference: &ResourceReference{
			ID:        snapshot.UUID,
			Name:      snapshot.Name,
			CreatedAt: snapshot.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: snapshot.UpdatedAt.Format(TimeStringForMat),
		},
		Description: snapshot.Description,
		Status:      snapshot.Status.String(),
		CGID:        cgUUID,
		Size:        snapshot.Size,
		WdsSnapID:   snapshot.WdsSnapID,
	}

	logger.Debugf("Successfully retrieved snapshot %s", snapUUID)
	c.JSON(http.StatusOK, response)
}

// @Summary Create a consistency group snapshot
// @Description Create a snapshot for a consistency group
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id       path    string                            true  "Consistency Group UUID"
// @Param   payload  body    ConsistencyGroupSnapshotPayload   true  "Snapshot Payload"
// @Success 200 {object} ConsistencyGroupSnapshotResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/snapshots [post]
func (a *ConsistencyGroupAPI) CreateSnapshot(c *gin.Context) {
	ctx := c.Request.Context()
	cgUUID := c.Param("id")
	logger.Debugf("Create snapshot for consistency group: %s", cgUUID)

	// Parse request body
	// 解析请求体
	var payload ConsistencyGroupSnapshotPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		logger.Errorf("Failed to parse request body: %+v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid request payload", err)
		return
	}

	// Create snapshot
	// 创建快照
	snapshot, err := consistencyGroupAdmin.CreateSnapshot(ctx, cgUUID, payload.Name, payload.Description)
	if err != nil {
		logger.Errorf("Failed to create snapshot for CG %s: %+v", cgUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create snapshot", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupSnapshotResponse{
		ResourceReference: &ResourceReference{
			ID:        snapshot.UUID,
			Name:      snapshot.Name,
			CreatedAt: snapshot.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: snapshot.UpdatedAt.Format(TimeStringForMat),
		},
		Description: snapshot.Description,
		Status:      snapshot.Status.String(),
		CGID:        cgUUID,
		Size:        snapshot.Size,
		WdsSnapID:   snapshot.WdsSnapID,
	}

	logger.Debugf("Successfully created snapshot %s for CG %s", snapshot.UUID, cgUUID)
	c.JSON(http.StatusOK, response)
}

// @Summary Delete a consistency group snapshot
// @Description Delete a snapshot from a consistency group
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id       path    string  true  "Consistency Group UUID"
// @Param   snap_id  path    string  true  "Snapshot UUID"
// @Success 204 "No Content"
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/snapshots/{snap_id} [delete]
func (a *ConsistencyGroupAPI) DeleteSnapshot(c *gin.Context) {
	ctx := c.Request.Context()
	cgUUID := c.Param("id")
	snapUUID := c.Param("snap_id")
	logger.Debugf("Delete snapshot %s from consistency group %s", snapUUID, cgUUID)

	// Delete snapshot
	// 删除快照
	err := consistencyGroupAdmin.DeleteSnapshot(ctx, cgUUID, snapUUID)
	if err != nil {
		logger.Errorf("Failed to delete snapshot %s from CG %s: %+v", snapUUID, cgUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to delete snapshot", err)
		return
	}

	logger.Debugf("Successfully initiated snapshot deletion for %s", snapUUID)
	c.Status(http.StatusNoContent)
}

// @Summary Restore a consistency group from snapshot
// @Description Restore all volumes in a consistency group from a snapshot
// @tags Storage
// @Accept  json
// @Produce json
// @Param   id       path    string  true  "Consistency Group UUID"
// @Param   snap_id  path    string  true  "Snapshot UUID"
// @Success 200 {object} ConsistencyGroupRestoreResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /consistency_groups/{id}/snapshots/{snap_id}/restore [post]
func (a *ConsistencyGroupAPI) RestoreSnapshot(c *gin.Context) {
	ctx := c.Request.Context()
	cgUUID := c.Param("id")
	snapUUID := c.Param("snap_id")
	logger.Debugf("Restore consistency group %s from snapshot %s", cgUUID, snapUUID)

	// Restore from snapshot
	// 从快照恢复
	task, err := consistencyGroupAdmin.RestoreSnapshot(ctx, cgUUID, snapUUID)
	if err != nil {
		logger.Errorf("Failed to restore CG %s from snapshot %s: %+v", cgUUID, snapUUID, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to restore from snapshot", err)
		return
	}

	// Build response
	// 构建响应
	response := &ConsistencyGroupRestoreResponse{
		TaskID:   strconv.FormatInt(task.ID, 10),
		TaskUUID: task.UUID,
		Status:   string(task.Status),
	}

	logger.Debugf("Successfully initiated restore for CG %s from snapshot %s, task ID: %d", cgUUID, snapUUID, task.ID)
	c.JSON(http.StatusOK, response)
}
