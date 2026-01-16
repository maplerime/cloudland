/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"
	"strings"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"
)

var (
	consistencyGroupAdmin = &ConsistencyGroupAdmin{}
)

// ConsistencyGroupAdmin handles consistency group operations
type ConsistencyGroupAdmin struct{}

// Get retrieves a consistency group by ID
// 通过 ID 获取一致性组
func (a *ConsistencyGroupAdmin) Get(ctx context.Context, id int64) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Get consistency group by ID: %d", id)
	db := dbs.DB(ctx)
	cg = &model.ConsistencyGroup{Model: model.Model{ID: id}}
	if err = db.Take(cg).Error; err != nil {
		logger.Errorf("Failed to get consistency group by ID %d: %+v", id, err)
		err = NewCLError(ErrCGNotFound, "Consistency group not found", err)
		return
	}
	return
}

// GetByUUID retrieves a consistency group by UUID
// 通过 UUID 获取一致性组
func (a *ConsistencyGroupAdmin) GetByUUID(ctx context.Context, uuid string) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Get consistency group by UUID: %s", uuid)
	db := dbs.DB(ctx)
	cg = &model.ConsistencyGroup{}
	if err = db.Where("uuid = ?", uuid).Take(cg).Error; err != nil {
		logger.Errorf("Failed to get consistency group by UUID %s: %+v", uuid, err)
		err = NewCLError(ErrCGNotFound, "Consistency group not found", err)
		return
	}
	return
}

// List retrieves a list of consistency groups with pagination
// 获取一致性组列表（分页）
func (a *ConsistencyGroupAdmin) List(ctx context.Context, offset, limit int64, order, name string) (total int64, cgs []*model.ConsistencyGroup, err error) {
	logger.Debugf("List consistency groups: offset=%d, limit=%d, order=%s, name=%s", offset, limit, order, name)
	db := dbs.DB(ctx)

	// Get membership for permission filtering
	memberShip := GetMemberShip(ctx)

	// Build query
	query := db.Model(&model.ConsistencyGroup{})

	// Filter by owner if not admin
	if memberShip.OrgID > 0 {
		query = query.Where("owner = ?", memberShip.OrgID)
	}

	// Filter by name if provided
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	// Get total count
	if err = query.Count(&total).Error; err != nil {
		logger.Errorf("Failed to count consistency groups: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to count consistency groups", err)
		return
	}

	// Get paginated results
	cgs = []*model.ConsistencyGroup{}
	if err = query.Offset(int(offset)).Limit(int(limit)).Order(order).Find(&cgs).Error; err != nil {
		logger.Errorf("Failed to list consistency groups: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to list consistency groups", err)
		return
	}

	logger.Debugf("Found %d consistency groups (total: %d)", len(cgs), total)
	return
}

// Create creates a new consistency group with volumes
// 创建新的一致性组
func (a *ConsistencyGroupAdmin) Create(ctx context.Context, name, description string, volumeUUIDs []string) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Creating consistency group: name=%s, volumes=%v", name, volumeUUIDs)

	// Get membership for permission check
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Errorf("Not authorized to create consistency group")
		err = NewCLError(ErrPermissionDenied, "Not authorized to create consistency group", nil)
		return
	}

	// Validate input
	if len(volumeUUIDs) == 0 {
		logger.Errorf("No volumes provided for consistency group")
		err = NewCLError(ErrInvalidParameter, "At least one volume is required", nil)
		return
	}

	db := dbs.DB(ctx)

	// Retrieve and validate volumes
	volumes := []*model.Volume{}
	for _, uuid := range volumeUUIDs {
		volume := &model.Volume{}
		if err = db.Where("uuid = ?", uuid).Take(volume).Error; err != nil {
			logger.Errorf("Volume not found: %s, %+v", uuid, err)
			err = NewCLError(ErrVolumeNotFound, fmt.Sprintf("Volume %s not found", uuid), err)
			return
		}
		volumes = append(volumes, volume)
	}

	// Validate all volumes are in the same pool
	poolID := volumes[0].PoolID
	for _, vol := range volumes {
		if vol.PoolID != poolID {
			logger.Errorf("Volumes are not in the same pool: %s vs %s", vol.PoolID, poolID)
			err = NewCLError(ErrCGVolumeNotInSamePool, "All volumes must be in the same storage pool", nil)
			return
		}
	}

	// Validate volume states
	for _, vol := range volumes {
		if vol.IsError() {
			logger.Errorf("Volume %s is in error state", vol.UUID)
			err = NewCLError(ErrCGVolumeInvalidState, fmt.Sprintf("Volume %s is in error state", vol.UUID), nil)
			return
		}
		if vol.IsBusy() {
			logger.Errorf("Volume %s is busy", vol.UUID)
			err = NewCLError(ErrCGVolumeIsBusy, fmt.Sprintf("Volume %s is busy", vol.UUID), nil)
			return
		}
	}

	logger.Debugf("All volumes validated successfully")

	// Start transaction
	// 开始事务
	db, err = StartTransaction(ctx)
	if err != nil {
		logger.Errorf("Failed to start transaction: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to start transaction", err)
		return
	}
	defer EndTransaction(ctx, db, &err)

	// Create consistency group record
	// 创建一致性组记录
	cg = &model.ConsistencyGroup{
		Owner:       memberShip.OrgID,
		Name:        name,
		Description: description,
		Status:      model.CGStatusProcessing,
		PoolID:      poolID,
	}
	if err = db.Create(cg).Error; err != nil {
		logger.Errorf("Failed to create consistency group: %+v", err)
		err = NewCLError(ErrCGCreationFailed, "Failed to create consistency group", err)
		return
	}
	logger.Debugf("Created consistency group with ID: %d", cg.ID)

	// Create volume associations
	// 创建卷关联记录
	for _, vol := range volumes {
		cgVolume := &model.ConsistencyGroupVolume{
			CGID:     cg.ID,
			VolumeID: vol.ID,
		}
		if err = db.Create(cgVolume).Error; err != nil {
			logger.Errorf("Failed to create consistency group volume association: %+v", err)
			err = NewCLError(ErrCGCreationFailed, "Failed to create volume association", err)
			return
		}
	}
	logger.Debugf("Created %d volume associations", len(volumes))

	// Collect volume UUIDs for WDS API call
	// 收集卷 UUID 用于 WDS API 调用
	var volumeUUIDList []string
	for _, vol := range volumes {
		volumeUUIDList = append(volumeUUIDList, vol.UUID)
	}
	volumeUUIDsJSON := fmt.Sprintf("[%s]", strings.Join(volumeUUIDList, ","))

	// Execute WDS script to create consistency group
	// 执行 WDS 脚本创建一致性组
	cgName := fmt.Sprintf("cg_%s", cg.UUID)
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_cg_wds.sh %d %s '%s' '%s'",
		cg.ID, cgName, poolID, volumeUUIDsJSON)
	err = HyperExecute(ctx, command, "wds_vhost")
	if err != nil {
		logger.Errorf("Failed to execute create CG script: %+v", err)
		err = NewCLError(ErrCGCreationFailed, "Failed to execute create CG script", err)
		return
	}
	logger.Debugf("Executed create CG script for CG ID: %d", cg.ID)

	return
}

// Update updates a consistency group's name and description
// 更新一致性组的名称和描述
func (a *ConsistencyGroupAdmin) Update(ctx context.Context, id int64, name, description string) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Update consistency group: ID=%d, name=%s", id, name)

	// Get membership for permission check
	// 获取成员信息进行权限检查
	memberShip := GetMemberShip(ctx)

	db := dbs.DB(ctx)

	// Retrieve consistency group
	// 获取一致性组
	cg = &model.ConsistencyGroup{Model: model.Model{ID: id}}
	if err = db.Take(cg).Error; err != nil {
		logger.Errorf("Failed to get consistency group by ID %d: %+v", id, err)
		err = NewCLError(ErrCGNotFound, "Consistency group not found", err)
		return
	}

	// Permission check
	// 权限检查
	permit, err := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to update consistency group ID %d", id)
		err = NewCLError(ErrPermissionDenied, "Not authorized to update consistency group", err)
		return
	}

	// Check if CG can be updated
	// 检查一致性组是否可以更新
	if !cg.CanUpdate() {
		logger.Errorf("Consistency group ID %d is not available for update, status: %s", id, cg.Status)
		err = NewCLError(ErrCGInvalidState, fmt.Sprintf("Consistency group is not available for update, status: %s", cg.Status), nil)
		return
	}

	// Check if CG has snapshots
	// 检查一致性组是否有快照
	var snapshotCount int64
	if err = db.Model(&model.ConsistencyGroupSnapshot{}).Where("cg_id = ?", cg.ID).Count(&snapshotCount).Error; err != nil {
		logger.Errorf("Failed to count snapshots for CG ID %d: %+v", id, err)
		err = NewCLError(ErrDatabaseError, "Failed to count snapshots", err)
		return
	}
	if snapshotCount > 0 {
		logger.Errorf("Consistency group ID %d has %d snapshots, cannot update", id, snapshotCount)
		err = NewCLError(ErrCGSnapshotExists, "Consistency group has snapshots, cannot update", nil)
		return
	}

	// Start transaction
	// 开始事务
	db, err = StartTransaction(ctx)
	if err != nil {
		logger.Errorf("Failed to start transaction: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to start transaction", err)
		return
	}
	defer EndTransaction(ctx, db, &err)

	// Update consistency group
	// 更新一致性组
	updates := map[string]interface{}{}
	if name != "" {
		updates["name"] = name
	}
	if description != "" {
		updates["description"] = description
	}

	if len(updates) > 0 {
		if err = db.Model(cg).Updates(updates).Error; err != nil {
			logger.Errorf("Failed to update consistency group ID %d: %+v", id, err)
			err = NewCLError(ErrCGUpdateFailed, "Failed to update consistency group", err)
			return
		}
		logger.Debugf("Updated consistency group ID %d", id)
	}

	return
}

// Delete deletes a consistency group
// 删除一致性组
func (a *ConsistencyGroupAdmin) Delete(ctx context.Context, id int64) (err error) {
	logger.Debugf("Delete consistency group: ID=%d", id)

	// Get membership for permission check
	// 获取成员信息进行权限检查
	memberShip := GetMemberShip(ctx)

	db := dbs.DB(ctx)

	// Retrieve consistency group
	// 获取一致性组
	cg := &model.ConsistencyGroup{Model: model.Model{ID: id}}
	if err = db.Take(cg).Error; err != nil {
		logger.Errorf("Failed to get consistency group by ID %d: %+v", id, err)
		err = NewCLError(ErrCGNotFound, "Consistency group not found", err)
		return
	}

	// Permission check
	// 权限检查
	permit, err := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to delete consistency group ID %d", id)
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete consistency group", err)
		return
	}

	// Check if CG can be deleted
	// 检查一致性组是否可以删除
	if !cg.CanDelete() {
		logger.Errorf("Consistency group ID %d is busy, status: %s", id, cg.Status)
		err = NewCLError(ErrCGIsBusy, fmt.Sprintf("Consistency group is busy, status: %s", cg.Status), nil)
		return
	}

	// Check if CG has snapshots
	// 检查一致性组是否有快照
	var snapshotCount int64
	if err = db.Model(&model.ConsistencyGroupSnapshot{}).Where("cg_id = ?", cg.ID).Count(&snapshotCount).Error; err != nil {
		logger.Errorf("Failed to count snapshots for CG ID %d: %+v", id, err)
		err = NewCLError(ErrDatabaseError, "Failed to count snapshots", err)
		return
	}
	if snapshotCount > 0 {
		logger.Errorf("Consistency group ID %d has %d snapshots, cannot delete", id, snapshotCount)
		err = NewCLError(ErrCGSnapshotExists, "Consistency group has snapshots, cannot delete", nil)
		return
	}

	// Start transaction
	// 开始事务
	db, err = StartTransaction(ctx)
	if err != nil {
		logger.Errorf("Failed to start transaction: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to start transaction", err)
		return
	}
	defer EndTransaction(ctx, db, &err)

	// Update status to deleting
	// 更新状态为删除中
	if err = db.Model(cg).Update("status", model.CGStatusDeleting).Error; err != nil {
		logger.Errorf("Failed to update CG status to deleting: %+v", err)
		err = NewCLError(ErrCGDeleteFailed, "Failed to update CG status", err)
		return
	}

	// Execute WDS script to delete consistency group
	// 执行 WDS 脚本删除一致性组
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/delete_cg_wds.sh %d %s",
		cg.ID, cg.WdsCgID)
	err = HyperExecute(ctx, command, "wds_vhost")
	if err != nil {
		logger.Errorf("Failed to execute delete CG script: %+v", err)
		err = NewCLError(ErrCGDeleteFailed, "Failed to execute delete CG script", err)
		return
	}
	logger.Debugf("Executed delete CG script for CG ID: %d", cg.ID)

	return
}
