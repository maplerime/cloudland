/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	"gopkg.in/macaron.v1"
)

var (
	consistencyGroupAdmin = &ConsistencyGroupAdmin{}
	consistencyGroupView  = &ConsistencyGroupView{}
)

// ConsistencyGroupAdmin handles consistency group operations
type ConsistencyGroupAdmin struct{}

// ConsistencyGroupView handles consistency group web console views
// 一致性组 Web 控制台视图处理
type ConsistencyGroupView struct{}

// Get retrieves a consistency group by ID
// 通过 ID 获取一致性组
func (a *ConsistencyGroupAdmin) Get(ctx context.Context, id int64) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Get consistency group by ID: %d", id)
	ctx, db := GetContextDB(ctx)
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
	ctx, db := GetContextDB(ctx)
	cg = &model.ConsistencyGroup{}
	if err = db.Where("uuid = ?", uuid).Take(cg).Error; err != nil {
		logger.Errorf("Failed to get consistency group by UUID %s: %+v", uuid, err)
		err = NewCLError(ErrCGNotFound, "Consistency group not found", err)
		return
	}
	return
}

// IsVolumeInCG checks if a volume is in any consistency group
// 检查卷是否在任何一致性组中
func (a *ConsistencyGroupAdmin) IsVolumeInCG(ctx context.Context, volumeID int64) (bool, error) {
	ctx, db := GetContextDB(ctx)
	var count int64
	if err := db.Model(&model.ConsistencyGroupVolume{}).Where("volume_id = ?", volumeID).Count(&count).Error; err != nil {
		logger.Errorf("Failed to check if volume %d is in consistency group: %+v", volumeID, err)
		return false, NewCLError(ErrDatabaseError, "Failed to check if volume is in consistency group", err)
	}
	return count > 0, nil
}

// List retrieves a list of consistency groups with pagination
// 获取一致性组列表（分页）
func (a *ConsistencyGroupAdmin) List(ctx context.Context, offset, limit int64, order, name string) (total int64, cgs []*model.ConsistencyGroup, err error) {
	logger.Debugf("List consistency groups: offset=%d, limit=%d, order=%s, name=%s", offset, limit, order, name)
	ctx, db := GetContextDB(ctx)

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
	query = dbs.Sortby(query.Offset(int(offset)).Limit(int(limit)), order)
	if err = query.Find(&cgs).Error; err != nil {
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

	ctx, db := GetContextDB(ctx)

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
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Create consistency group record
	// 创建一致性组记录
	cg = &model.ConsistencyGroup{
		Owner:       memberShip.OrgID,
		Name:        name,
		Description: description,
		Status:      model.CGStatusProcessing,
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
	// 收集卷 WDS ID 用于 WDS API 调用
	var volumeWDSIDList []string
	for _, vol := range volumes {
		volumeWDSIDList = append(volumeWDSIDList, vol.GetOriginVolumeID())
	}
	volumeWDSIDJSONBytes, _ := json.Marshal(volumeWDSIDList)
	volumeWDSIDJSON := string(volumeWDSIDJSONBytes)

	// Execute WDS script to create consistency group
	// 执行 WDS 脚本创建一致性组
	cgName := fmt.Sprintf("cg_%s", cg.UUID)
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_cg_wds.sh %d %s '%s'",
		cg.ID, cgName, volumeWDSIDJSON)
	err = HyperExecute(ctx, control, command)
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

	ctx, db := GetContextDB(ctx)

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
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to update consistency group ID %d", id)
		err = NewCLError(ErrPermissionDenied, "Not authorized to update consistency group", nil)
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
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

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

	ctx, db := GetContextDB(ctx)

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
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to delete consistency group ID %d", id)
		err = NewCLError(ErrPermissionDenied, "Not authorized to delete consistency group", nil)
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
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Update status to deleting
	// 更新状态为删除中
	if err = db.Model(cg).Update("status", model.CGStatusDeleting).Error; err != nil {
		logger.Errorf("Failed to update CG status to deleting: %+v", err)
		err = NewCLError(ErrCGDeleteFailed, "Failed to update CG status", err)
		return
	}

	// Execute WDS script to delete consistency group
	// 执行 WDS 脚本删除一致性组
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/delete_cg_wds.sh %d %s",
		cg.ID, cg.WdsCgID)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Errorf("Failed to execute delete CG script: %+v", err)
		err = NewCLError(ErrCGDeleteFailed, "Failed to execute delete CG script", err)
		return
	}
	logger.Debugf("Executed delete CG script for CG ID: %d", cg.ID)

	return
}

// AddVolumes adds volumes to a consistency group
// 向一致性组添加卷
func (a *ConsistencyGroupAdmin) AddVolumes(ctx context.Context, id int64, volumeUUIDs []string) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Adding volumes to consistency group ID: %d, volumes=%v", id, volumeUUIDs)

	// Get membership for permission check
	// 获取成员信息进行权限检查
	memberShip := GetMemberShip(ctx)

	ctx, db := GetContextDB(ctx)

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
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to add volumes to consistency group ID %d", id)
		err = NewCLError(ErrPermissionDenied, "Not authorized to add volumes to consistency group", nil)
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
		logger.Errorf("Consistency group ID %d has %d snapshots, cannot add volumes", id, snapshotCount)
		err = NewCLError(ErrCGSnapshotExists, "Consistency group has snapshots, cannot add volumes", nil)
		return
	}

	// Validate input
	// 验证输入
	if len(volumeUUIDs) == 0 {
		logger.Errorf("No volumes provided")
		err = NewCLError(ErrInvalidParameter, "At least one volume is required", nil)
		return
	}

	// Retrieve and validate volumes
	// 获取并验证卷
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

	// Validate volume states
	// 验证卷状态
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

	// Check if volumes are already in the CG
	// 检查卷是否已经在一致性组中
	for _, vol := range volumes {
		var count int64
		if err = db.Model(&model.ConsistencyGroupVolume{}).Where("cg_id = ? AND volume_id = ?", cg.ID, vol.ID).Count(&count).Error; err != nil {
			logger.Errorf("Failed to check volume association: %+v", err)
			err = NewCLError(ErrDatabaseError, "Failed to check volume association", err)
			return
		}
		if count > 0 {
			logger.Errorf("Volume %s is already in consistency group", vol.UUID)
			err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Volume %s is already in consistency group", vol.UUID), nil)
			return
		}
	}

	// Check if volumes are in other CGs
	// 检查卷是否在其他一致性组中
	for _, vol := range volumes {
		var count int64
		if err = db.Model(&model.ConsistencyGroupVolume{}).Where("volume_id = ?", vol.ID).Count(&count).Error; err != nil {
			logger.Errorf("Failed to check volume in other CGs: %+v", err)
			err = NewCLError(ErrDatabaseError, "Failed to check volume in other CGs", err)
			return
		}
		if count > 0 {
			logger.Errorf("Volume %s is already in another consistency group", vol.UUID)
			err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Volume %s is already in another consistency group", vol.UUID), nil)
			return
		}
	}

	logger.Debugf("All volumes validated successfully")

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Update status to updating
	// 更新状态为更新中
	if err = db.Model(cg).Update("status", model.CGStatusUpdating).Error; err != nil {
		logger.Errorf("Failed to update CG status to updating: %+v", err)
		err = NewCLError(ErrCGUpdateFailed, "Failed to update CG status", err)
		return
	}

	// Create volume associations
	// 创建卷关联记录
	for _, vol := range volumes {
		cgVolume := &model.ConsistencyGroupVolume{
			CGID:     cg.ID,
			VolumeID: vol.ID,
		}
		if err = db.Create(cgVolume).Error; err != nil {
			logger.Errorf("Failed to create consistency group volume association: %+v", err)
			err = NewCLError(ErrCGUpdateFailed, "Failed to create volume association", err)
			return
		}
	}
	logger.Debugf("Created %d volume associations", len(volumes))

	// Collect volume WDS IDs for WDS API call
	// 收集卷 WDS ID 用于 WDS API 调用
	var volumeWDSIDList []string
	for _, vol := range volumes {
		volumeWDSIDList = append(volumeWDSIDList, vol.GetOriginVolumeID())
	}
	volumeWDSIDJSONBytes, _ := json.Marshal(volumeWDSIDList)
	volumeWDSIDJSON := string(volumeWDSIDJSONBytes)

	// Execute WDS script to add volumes to consistency group
	// 执行 WDS 脚本向一致性组添加卷
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/add_volumes_to_cg_wds.sh %d %s '%s'",
		cg.ID, cg.WdsCgID, volumeWDSIDJSON)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Errorf("Failed to execute add volumes to CG script: %+v", err)
		err = NewCLError(ErrCGUpdateFailed, "Failed to execute add volumes to CG script", err)
		return
	}
	logger.Debugf("Executed add volumes to CG script for CG ID: %d", cg.ID)

	return
}

// RemoveVolume removes a volume from a consistency group
// 从一致性组删除卷
func (a *ConsistencyGroupAdmin) RemoveVolume(ctx context.Context, id int64, volumeUUID string) (cg *model.ConsistencyGroup, err error) {
	logger.Debugf("Removing volume %s from consistency group ID: %d", volumeUUID, id)

	// Get membership for permission check
	// 获取成员信息进行权限检查
	memberShip := GetMemberShip(ctx)

	ctx, db := GetContextDB(ctx)

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
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to remove volume from consistency group ID %d", id)
		err = NewCLError(ErrPermissionDenied, "Not authorized to remove volume from consistency group", nil)
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
		logger.Errorf("Consistency group ID %d has %d snapshots, cannot remove volume", id, snapshotCount)
		err = NewCLError(ErrCGSnapshotExists, "Consistency group has snapshots, cannot remove volume", nil)
		return
	}

	// Retrieve volume
	// 获取卷
	volume := &model.Volume{}
	if err = db.Where("uuid = ?", volumeUUID).Take(volume).Error; err != nil {
		logger.Errorf("Volume not found: %s, %+v", volumeUUID, err)
		err = NewCLError(ErrVolumeNotFound, fmt.Sprintf("Volume %s not found", volumeUUID), err)
		return
	}

	// Check if volume is in the CG
	// 检查卷是否在一致性组中
	var cgVolume model.ConsistencyGroupVolume
	if err = db.Where("cg_id = ? AND volume_id = ?", cg.ID, volume.ID).Take(&cgVolume).Error; err != nil {
		logger.Errorf("Volume %s is not in consistency group %d: %+v", volumeUUID, id, err)
		err = NewCLError(ErrInvalidParameter, fmt.Sprintf("Volume %s is not in consistency group", volumeUUID), err)
		return
	}

	logger.Debugf("Volume validated successfully")

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Update status to updating
	// 更新状态为更新中
	if err = db.Model(cg).Update("status", model.CGStatusUpdating).Error; err != nil {
		logger.Errorf("Failed to update CG status to updating: %+v", err)
		err = NewCLError(ErrCGUpdateFailed, "Failed to update CG status", err)
		return
	}

	// Delete volume association
	// 删除卷关联记录
	if err = db.Delete(&cgVolume).Error; err != nil {
		logger.Errorf("Failed to delete consistency group volume association: %+v", err)
		err = NewCLError(ErrCGUpdateFailed, "Failed to delete volume association", err)
		return
	}
	logger.Debugf("Deleted volume association for volume %s", volumeUUID)

	// Execute WDS script to remove volume from consistency group
	// 执行 WDS 脚本从一致性组删除卷
	volumeWDSIDJSONBytes, _ := json.Marshal([]string{volume.GetOriginVolumeID()})
	volumeWDSIDJSON := string(volumeWDSIDJSONBytes)
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/remove_volumes_from_cg_wds.sh %d %s '%s'",
		cg.ID, cg.WdsCgID, volumeWDSIDJSON)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Errorf("Failed to execute remove volume from CG script: %+v", err)
		err = NewCLError(ErrCGUpdateFailed, "Failed to execute remove volume from CG script", err)
		return
	}
	logger.Debugf("Executed remove volume from CG script for CG ID: %d", cg.ID)

	return
}

// GetSnapshot retrieves a consistency group snapshot by ID
// 通过 ID 获取一致性组快照
func (a *ConsistencyGroupAdmin) GetSnapshot(ctx context.Context, id int64) (snapshot *model.ConsistencyGroupSnapshot, err error) {
	logger.Debugf("Get consistency group snapshot by ID: %d", id)
	ctx, db := GetContextDB(ctx)
	snapshot = &model.ConsistencyGroupSnapshot{Model: model.Model{ID: id}}
	if err = db.Take(snapshot).Error; err != nil {
		logger.Errorf("Failed to get snapshot by ID %d: %+v", id, err)
		err = NewCLError(ErrCGSnapshotNotFound, "Snapshot not found", err)
		return
	}
	return
}

// GetSnapshotByUUID retrieves a consistency group snapshot by UUID
// 通过 UUID 获取一致性组快照
func (a *ConsistencyGroupAdmin) GetSnapshotByUUID(ctx context.Context, uuid string) (snapshot *model.ConsistencyGroupSnapshot, err error) {
	logger.Debugf("Get consistency group snapshot by UUID: %s", uuid)
	ctx, db := GetContextDB(ctx)
	snapshot = &model.ConsistencyGroupSnapshot{}
	if err = db.Where("uuid = ?", uuid).Take(snapshot).Error; err != nil {
		logger.Errorf("Failed to get snapshot by UUID %s: %+v", uuid, err)
		err = NewCLError(ErrCGSnapshotNotFound, "Snapshot not found", err)
		return
	}
	return
}

// ListSnapshots retrieves a list of consistency group snapshots with pagination
// 获取一致性组快照列表（分页）
func (a *ConsistencyGroupAdmin) ListSnapshots(ctx context.Context, cgID int64, offset, limit int64, order string) (total int64, snapshots []*model.ConsistencyGroupSnapshot, err error) {
	logger.Debugf("List snapshots for CG ID: %d, offset=%d, limit=%d, order=%s", cgID, offset, limit, order)
	ctx, db := GetContextDB(ctx)

	// Build query
	// 构建查询
	query := db.Model(&model.ConsistencyGroupSnapshot{}).Where("cg_id = ?", cgID)

	// Get total count
	// 获取总数
	if err = query.Count(&total).Error; err != nil {
		logger.Errorf("Failed to count snapshots: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to count snapshots", err)
		return
	}

	// Get paginated results
	// 获取分页结果
	snapshots = []*model.ConsistencyGroupSnapshot{}
	if order == "" {
		order = "-created_at"
	}
	query = dbs.Sortby(query.Offset(int(offset)).Limit(int(limit)), order)
	if err = query.Find(&snapshots).Error; err != nil {
		logger.Errorf("Failed to list snapshots: %+v", err)
		err = NewCLError(ErrDatabaseError, "Failed to list snapshots", err)
		return
	}

	logger.Debugf("Found %d snapshots (total: %d)", len(snapshots), total)
	return
}

// CreateSnapshot creates a consistency group snapshot
// 创建一致性组快照
func (a *ConsistencyGroupAdmin) CreateSnapshot(ctx context.Context, cgUUID string, name, description string) (snapshot *model.ConsistencyGroupSnapshot, err error) {
	logger.Debugf("CreateSnapshot for CG %s, name: %s", cgUUID, name)

	// 1. 获取一致性组
	cg, err := a.GetByUUID(ctx, cgUUID)
	if err != nil {
		return nil, err
	}

	// 2. 权限检查
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to create snapshot for CG %s", cg.UUID)
		return nil, NewCLError(ErrPermissionDenied, "Not authorized to create snapshot for this consistency group", nil)
	}

	// 3. 检查一致性组状态
	if !cg.IsAvailable() {
		logger.Errorf("Consistency group %s is not available (status: %s)", cg.UUID, cg.Status)
		return nil, NewCLError(ErrCGInvalidState, fmt.Sprintf("Consistency group is not available (status: %s)", cg.Status), nil)
	}

	ctx, db := GetContextDB(ctx)

	// 4. 获取一致性组中的所有卷
	var cgVolumes []*model.ConsistencyGroupVolume
	if err = db.Preload("Volume").Where("cg_id = ?", cg.ID).Find(&cgVolumes).Error; err != nil {
		logger.Errorf("Failed to get volumes for CG %s: %v", cg.UUID, err)
		return nil, NewCLError(ErrDatabaseError, "Failed to get volumes for consistency group", err)
	}

	if len(cgVolumes) == 0 {
		logger.Errorf("Consistency group %s has no volumes", cg.UUID)
		return nil, NewCLError(ErrCGNoVolumes, "Consistency group has no volumes", nil)
	}

	// 5. 检查所有卷的状态
	for _, cgv := range cgVolumes {
		if cgv.Volume.IsBusy() {
			msg := fmt.Sprintf("Volume %s is busy (status: %s), cannot create snapshot now", cgv.Volume.UUID, cgv.Volume.Status)
			logger.Errorf(msg)
			return nil, NewCLError(ErrCGVolumeIsBusy, msg, nil)
		}
	}

	// 6. 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// 7. 创建任务记录
	task := &model.Task{
		Owner:   cg.Owner,
		Name:    fmt.Sprintf("create_cg_snapshot_%s", cg.UUID),
		Summary: fmt.Sprintf("Creating snapshot for consistency group %s", cg.Name),
		Status:  model.TaskStatusRunning,
		Source:  model.TaskSourceManual,
		Action:  model.TaskActionSnapshot,
	}
	if err = db.Create(task).Error; err != nil {
		logger.Errorf("Failed to create task: %v", err)
		return nil, NewCLError(ErrDatabaseError, "Failed to create task record", err)
	}

	// 8. 创建快照记录
	snapshot = &model.ConsistencyGroupSnapshot{
		Owner:       cg.Owner,
		Name:        name,
		Description: description,
		Status:      model.CGSnapshotStatusPending,
		CGID:        cg.ID,
		TaskID:      task.ID,
	}
	if err = db.Create(snapshot).Error; err != nil {
		logger.Errorf("Failed to create snapshot record: %v", err)
		return nil, NewCLError(ErrDatabaseError, "Failed to create snapshot record", err)
	}

	// 9. 更新所有卷的状态为 backuping
	for _, cgv := range cgVolumes {
		if err = db.Model(&model.Volume{}).Where("id = ?", cgv.Volume.ID).
			Update("status", model.VolumeStatusBackuping).Error; err != nil {
			logger.Errorf("Failed to update volume %d status: %v", cgv.Volume.ID, err)
			return nil, NewCLError(ErrDatabaseError, "Failed to update volume status", err)
		}
	}

	// 10. 调用 shell 脚本创建 WDS 快照
	// Parameters: cg_ID, cg_snapshot_ID, cg_snapshot_Name, wds_cg_id
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_cg_snapshot_wds.sh %d %d '%s' '%s'",
		cg.ID, snapshot.ID, snapshot.Name, cg.WdsCgID)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Errorf("Failed to execute create CG snapshot script: %v", err)
		// 更新任务状态为失败
		db.Model(task).Update("status", model.TaskStatusFailed)
		db.Model(snapshot).Update("status", model.CGSnapshotStatusError)
		return nil, NewCLError(ErrExecuteOnHyperFailed, "Failed to execute create CG snapshot script", err)
	}

	logger.Debugf("Successfully initiated CG snapshot creation for CG %s, snapshot ID: %d", cg.UUID, snapshot.ID)
	return snapshot, nil
}

// DeleteSnapshot deletes a consistency group snapshot
// 删除一致性组快照
func (a *ConsistencyGroupAdmin) DeleteSnapshot(ctx context.Context, cgUUID, snapUUID string) (err error) {
	logger.Debugf("DeleteSnapshot for CG %s, snapshot: %s", cgUUID, snapUUID)

	// 1. 获取一致性组
	cg, err := a.GetByUUID(ctx, cgUUID)
	if err != nil {
		return err
	}

	// 2. 权限检查
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to delete snapshot for CG %s", cg.UUID)
		return NewCLError(ErrPermissionDenied, "Not authorized to delete snapshot for this consistency group", nil)
	}

	ctx, db := GetContextDB(ctx)

	// 3. 获取快照
	snapshot := &model.ConsistencyGroupSnapshot{}
	if err = db.Where("uuid = ? AND cg_id = ?", snapUUID, cg.ID).First(snapshot).Error; err != nil {
		logger.Errorf("Snapshot %s not found for CG %s: %v", snapUUID, cg.UUID, err)
		return NewCLError(ErrCGSnapshotNotFound, fmt.Sprintf("Snapshot %s not found", snapUUID), err)
	}

	// 4. 检查快照状态
	if !snapshot.CanDelete() {
		logger.Errorf("Snapshot %s is busy (status: %s) and cannot be deleted", snapshot.UUID, snapshot.Status)
		return NewCLError(ErrCGSnapshotIsBusy, fmt.Sprintf("Snapshot is busy (status: %s) and cannot be deleted", snapshot.Status), nil)
	}

	// 5. 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// 6. 创建删除任务
	task := &model.Task{
		Owner:   cg.Owner,
		Name:    fmt.Sprintf("delete_cg_snapshot_%s", snapshot.UUID),
		Summary: fmt.Sprintf("Deleting snapshot %s from consistency group %s", snapshot.Name, cg.Name),
		Status:  model.TaskStatusRunning,
		Source:  model.TaskSourceManual,
		Action:  model.TaskActionSnapshot,
	}
	if err = db.Create(task).Error; err != nil {
		logger.Errorf("Failed to create task: %v", err)
		return NewCLError(ErrDatabaseError, "Failed to create task record", err)
	}

	// 7. 更新快照状态为 deleting
	if err = db.Model(snapshot).Updates(map[string]interface{}{
		"status":  model.CGSnapshotStatusDeleting,
		"task_id": task.ID,
	}).Error; err != nil {
		logger.Errorf("Failed to update snapshot status: %v", err)
		return NewCLError(ErrDatabaseError, "Failed to update snapshot status", err)
	}

	// 8. 调用 shell 脚本删除 WDS 快照
	// Parameters: cg_snapshot_ID, wds_snap_id
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/delete_cg_snapshot_wds.sh %d '%s'",
		snapshot.ID, snapshot.WdsSnapID)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Errorf("Failed to execute delete CG snapshot script: %v", err)
		// 更新任务状态为失败
		db.Model(task).Update("status", model.TaskStatusFailed)
		db.Model(snapshot).Update("status", model.CGSnapshotStatusError)
		return NewCLError(ErrExecuteOnHyperFailed, "Failed to execute delete CG snapshot script", err)
	}

	logger.Debugf("Successfully initiated CG snapshot deletion for snapshot %s", snapshot.UUID)
	return nil
}

// RestoreSnapshot restores a consistency group from a snapshot
// 从快照恢复一致性组
func (a *ConsistencyGroupAdmin) RestoreSnapshot(ctx context.Context, cgUUID, snapshotUUID string) (task *model.Task, err error) {
	logger.Debugf("RestoreSnapshot for CG %s from snapshot %s", cgUUID, snapshotUUID)

	// 1. 获取一致性组
	cg, err := a.GetByUUID(ctx, cgUUID)
	if err != nil {
		return nil, err
	}

	// 2. 权限检查
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Errorf("Not authorized to restore snapshot for CG %s", cg.UUID)
		return nil, NewCLError(ErrPermissionDenied, "Not authorized to restore snapshot for this consistency group", nil)
	}

	ctx, db := GetContextDB(ctx)

	// 3. 获取快照
	snapshot := &model.ConsistencyGroupSnapshot{}
	if err = db.Where("uuid = ? AND cg_id = ?", snapshotUUID, cg.ID).First(snapshot).Error; err != nil {
		logger.Errorf("Snapshot %s not found for CG %s: %v", snapshotUUID, cg.UUID, err)
		return nil, NewCLError(ErrCGSnapshotNotFound, fmt.Sprintf("Snapshot %s not found", snapshotUUID), err)
	}

	// 4. 检查快照状态
	if !snapshot.CanRestore() {
		logger.Errorf("Snapshot %s cannot be restored (status: %s)", snapshot.UUID, snapshot.Status)
		return nil, NewCLError(ErrCGSnapshotCannotRestore, fmt.Sprintf("Snapshot %s cannot be restored (status: %s)", snapshot.UUID, snapshot.Status), nil)
	}

	// 5. 获取一致性组中的所有卷
	var cgVolumes []*model.ConsistencyGroupVolume
	if err = db.Preload("Volume").Where("cg_id = ?", cg.ID).Find(&cgVolumes).Error; err != nil {
		logger.Errorf("Failed to get volumes for CG %s: %v", cg.UUID, err)
		return nil, NewCLError(ErrDatabaseError, "Failed to get volumes for consistency group", err)
	}

	if len(cgVolumes) == 0 {
		logger.Errorf("Consistency group %s has no volumes", cg.UUID)
		return nil, NewCLError(ErrCGNoVolumes, "Consistency group has no volumes", nil)
	}

	// 6. 收集实例 ID 并去重，用于检查关机状态
	instanceMap := make(map[int64]*model.Instance)

	// 7. 构建 volumes_json 格式的数据
	type VolumeInfo struct {
		VolID      int64  `json:"vol_id"`
		WdsID      string `json:"wds_id"`
		InstanceID int64  `json:"instance_id"`
	}
	volumeInfos := make([]VolumeInfo, 0, len(cgVolumes))

	for _, cgv := range cgVolumes {
		vol := cgv.Volume
		// 检查卷忙碌状态
		if vol.IsBusy() {
			msg := fmt.Sprintf("Volume %s is busy (status: %s), cannot restore now", vol.UUID, vol.Status)
			logger.Errorf(msg)
			return nil, NewCLError(ErrCGVolumeIsBusy, msg, nil)
		}

		volInfo := VolumeInfo{
			VolID:      vol.ID,
			WdsID:      vol.GetOriginVolumeID(),
			InstanceID: 0,
		}

		// 如果卷已挂载，记录实例并检查状态
		if vol.InstanceID > 0 {
			volInfo.InstanceID = vol.InstanceID

			// 检查实例是否已在 map 中
			if _, exists := instanceMap[vol.InstanceID]; !exists {
				instance := &model.Instance{Model: model.Model{ID: vol.InstanceID}}
				if err = db.Take(instance).Error; err != nil {
					logger.Errorf("Failed to get instance %d for volume %s: %v", vol.InstanceID, vol.UUID, err)
					return nil, NewCLError(ErrInstanceNotFound, fmt.Sprintf("Instance not found for volume %s", vol.UUID), err)
				}
				instanceMap[vol.InstanceID] = instance
			}
		} else if vol.Status == "attached" {
			// 状态是 attached 但没有 instanceID？异常情况
			logger.Errorf("Volume %s status is attached but has no instance ID", vol.UUID)
			return nil, NewCLError(ErrCGVolumeAttachedNoInstance, fmt.Sprintf("Volume %s status is attached but has no instance", vol.UUID), nil)
		}

		volumeInfos = append(volumeInfos, volInfo)
	}

	// 8. 检查所有相关实例是否都是关机状态
	for _, instance := range instanceMap {
		if instance.Status != model.InstanceStatusShutoff {
			msg := fmt.Sprintf("Instance %s (ID: %d) is not shutoff (status: %s), all instances must be stopped before restoring CG snapshot",
				instance.Hostname, instance.ID, instance.Status)
			logger.Errorf(msg)
			return nil, NewCLError(ErrCGInstanceNotShutoff, msg, nil)
		}
	}

	// 9. 序列化 volumes_json
	volumesJSON, err := json.Marshal(volumeInfos)
	if err != nil {
		logger.Errorf("Failed to serialize volumes info: %v", err)
		return nil, NewCLError(ErrJSONMarshalFailed, "Failed to serialize volumes info", err)
	}

	// 10. 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// 11. 创建恢复任务
	task = &model.Task{
		Owner:   cg.Owner,
		Name:    fmt.Sprintf("restore_cg_snapshot_%s", snapshot.UUID),
		Summary: fmt.Sprintf("Restoring consistency group %s from snapshot %s", cg.Name, snapshot.Name),
		Status:  model.TaskStatusRunning,
		Source:  model.TaskSourceManual,
		Action:  model.TaskActionRestore,
	}
	if err = db.Create(task).Error; err != nil {
		logger.Errorf("Failed to create restore task: %v", err)
		return nil, NewCLError(ErrDatabaseError, "Failed to create restore task", err)
	}

	// 12. 更新快照状态为 restoring
	if err = db.Model(snapshot).Updates(map[string]interface{}{
		"status":  model.CGSnapshotStatusRestoring,
		"task_id": task.ID,
	}).Error; err != nil {
		logger.Errorf("Failed to update snapshot status: %v", err)
		return nil, NewCLError(ErrDatabaseError, "Failed to update snapshot status", err)
	}

	// 13. 更新所有卷的状态为 restoring
	for _, cgv := range cgVolumes {
		if err = db.Model(&model.Volume{}).Where("id = ?", cgv.Volume.ID).
			Update("status", "restoring").Error; err != nil {
			logger.Errorf("Failed to update volume %d status: %v", cgv.Volume.ID, err)
			return nil, NewCLError(ErrDatabaseError, "Failed to update volume status", err)
		}
	}

	// 14. 调用 shell 脚本恢复快照
	// Parameters: cg_snapshot_ID, cg_ID, wds_cg_id, wds_snap_id, volumes_json
	control := fmt.Sprintf("inter=")
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/restore_cg_snapshot_wds.sh %d %d '%s' '%s' '%s'",
		snapshot.ID, cg.ID, cg.WdsCgID, snapshot.WdsSnapID, string(volumesJSON))
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Errorf("Failed to execute restore CG snapshot script: %v", err)
		// 更新任务状态为失败
		db.Model(task).Update("status", model.TaskStatusFailed)
		db.Model(snapshot).Update("status", model.CGSnapshotStatusError)
		return nil, NewCLError(ErrExecuteOnHyperFailed, "Failed to execute restore CG snapshot script", err)
	}

	logger.Debugf("Successfully initiated CG snapshot restore for CG %s from snapshot %s", cg.UUID, snapshot.UUID)
	return task, nil
}

// ========== ConsistencyGroupView Methods ==========

// List displays the consistency group list page
// 显示一致性组列表页面
func (v *ConsistencyGroupView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")

	total, cgs, err := consistencyGroupAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list consistency groups", err)
		c.Data["ErrorMsg"] = "Failed to list consistency groups"
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx

	// Get volume counts and snapshot counts for each CG
	// 获取每个一致性组的卷数量和快照数量
	type CGWithCounts struct {
		*model.ConsistencyGroup
		VolumeCount   int64
		SnapshotCount int64
		OwnerInfo     *model.Organization
	}
	cgsWithCounts := make([]*CGWithCounts, 0, len(cgs))
	for _, cg := range cgs {
		var volumeCount, snapshotCount int64
		db.Model(&model.ConsistencyGroupVolume{}).Where("cg_id = ?", cg.ID).Count(&volumeCount)
		db.Model(&model.ConsistencyGroupSnapshot{}).Where("cg_id = ?", cg.ID).Count(&snapshotCount)

		cgwc := &CGWithCounts{
			ConsistencyGroup: cg,
			VolumeCount:      volumeCount,
			SnapshotCount:    snapshotCount,
		}

		// Get owner info for admin
		// 为管理员获取所有者信息
		if memberShip.CheckPermission(model.Admin) {
			cgwc.OwnerInfo = &model.Organization{Model: model.Model{ID: cg.Owner}}
			db.Take(cgwc.OwnerInfo)
		}

		cgsWithCounts = append(cgsWithCounts, cgwc)
	}

	pages := GetPages(total, limit)
	c.Data["ConsistencyGroups"] = cgsWithCounts
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "cgroups")
}

// New displays the create consistency group page
// 显示创建一致性组页面
func (v *ConsistencyGroupView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx

	// Get available volumes (not in any CG)
	// 获取可用的卷（未加入任何一致性组）
	var volumes []*model.Volume
	where := memberShip.GetWhere()
	// Get volumes that are not in any CG
	// 获取未加入任何一致性组的卷
	if err := db.Model(&model.Volume{}).Where(where).Where("id NOT IN (SELECT volume_id FROM consistency_group_volumes WHERE deleted_at IS NULL)").Where("status IN (?)", []string{"available", "attached"}).Find(&volumes).Error; err != nil {
		logger.Error("Failed to query volumes", err)
		c.Data["ErrorMsg"] = "Failed to query volumes"
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	c.Data["Volumes"] = volumes
	c.HTML(200, "cgroups_new")
}

// Create handles creating a new consistency group
// 处理创建新一致性组
func (v *ConsistencyGroupView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	redirectTo := "../cgroups"
	name := c.QueryTrim("name")
	description := c.QueryTrim("description")
	volumeUUIDs := c.Req.Form["volumes"]

	if name == "" {
		logger.Error("Consistency group name is empty")
		c.Data["ErrorMsg"] = "Consistency group name is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	if len(volumeUUIDs) == 0 {
		logger.Error("No volumes selected")
		c.Data["ErrorMsg"] = "At least one volume must be selected"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	_, err := consistencyGroupAdmin.Create(c.Req.Context(), name, description, volumeUUIDs)
	if err != nil {
		logger.Error("Failed to create consistency group", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	c.Redirect(redirectTo)
}

// Get displays the consistency group detail page
// 显示一致性组详情页面
func (v *ConsistencyGroupView) Get(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "ID is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	// Permission check
	// 权限检查
	permit = memberShip.ValidateOwner(model.Reader, cg.Owner)
	if !permit {
		logger.Error("Not authorized to view this consistency group")
		c.Data["ErrorMsg"] = "Not authorized to view this consistency group"
		c.HTML(http.StatusForbidden, "error")
		return
	}

	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx

	// Get volumes in the CG
	// 获取一致性组中的卷
	var cgVolumes []*model.ConsistencyGroupVolume
	db.Preload("Volume").Where("cg_id = ?", cg.ID).Find(&cgVolumes)
	volumes := make([]*model.Volume, 0, len(cgVolumes))
	for _, cgv := range cgVolumes {
		volumes = append(volumes, cgv.Volume)
	}

	// Get snapshot count
	// 获取快照数量
	var snapshotCount int64
	db.Model(&model.ConsistencyGroupSnapshot{}).Where("cg_id = ?", cg.ID).Count(&snapshotCount)

	// Get recent snapshots (limit 5)
	// 获取最近的快照（限制5个）
	var recentSnapshots []*model.ConsistencyGroupSnapshot
	db.Where("cg_id = ?", cg.ID).Order("created_at DESC").Limit(5).Find(&recentSnapshots)

	c.Data["CG"] = cg
	c.Data["Volumes"] = volumes
	c.Data["SnapshotCount"] = snapshotCount
	c.Data["RecentSnapshots"] = recentSnapshots
	c.HTML(200, "cgroup")
}

// Edit displays the edit consistency group page
// 显示编辑一致性组页面
func (v *ConsistencyGroupView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	// Permission check
	// 权限检查
	permit = memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Error("Not authorized to edit this consistency group")
		c.Data["ErrorMsg"] = "Not authorized to edit this consistency group"
		c.HTML(http.StatusForbidden, "error")
		return
	}

	c.Data["CG"] = cg
	c.HTML(200, "cgroups_edit")
}

// Patch handles updating a consistency group
// 处理更新一致性组
func (v *ConsistencyGroupView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	name := c.QueryTrim("name")
	description := c.QueryTrim("description")

	_, err = consistencyGroupAdmin.Update(c.Req.Context(), cgID, name, description)
	if err != nil {
		logger.Error("Failed to update consistency group", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	c.Redirect(fmt.Sprintf("../cgroups/%d", cgID))
}

// Delete handles deleting a consistency group
// 处理删除一致性组
func (v *ConsistencyGroupView) Delete(c *macaron.Context, store session.Store) error {
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "ID is empty"
		c.Error(http.StatusBadRequest)
		return nil
	}

	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.Error(http.StatusBadRequest)
		return nil
	}

	err = consistencyGroupAdmin.Delete(c.Req.Context(), cgID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return nil
	}

	c.JSON(200, map[string]interface{}{
		"redirect": "/cgroups",
	})
	return nil
}

// Volumes displays the volume management page for a consistency group
// 显示一致性组的卷管理页面
func (v *ConsistencyGroupView) Volumes(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	// Permission check
	// 权限检查
	permit = memberShip.ValidateOwner(model.Reader, cg.Owner)
	if !permit {
		logger.Error("Not authorized to view this consistency group")
		c.Data["ErrorMsg"] = "Not authorized to view this consistency group"
		c.HTML(http.StatusForbidden, "error")
		return
	}

	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx

	// Get volumes in the CG
	// 获取一致性组中的卷
	var cgVolumes []*model.ConsistencyGroupVolume
	db.Preload("Volume").Where("cg_id = ?", cg.ID).Find(&cgVolumes)
	volumes := make([]*model.Volume, 0, len(cgVolumes))
	for _, cgv := range cgVolumes {
		volumes = append(volumes, cgv.Volume)
	}

	// Get snapshot count (to check if volumes can be modified)
	// 获取快照数量（用于检查是否可以修改卷）
	var snapshotCount int64
	db.Model(&model.ConsistencyGroupSnapshot{}).Where("cg_id = ?", cg.ID).Count(&snapshotCount)

	// Get available volumes that can be added (not in any CG)
	// 获取可添加的卷（未加入任何一致性组）
	var availableVolumes []*model.Volume
	where := memberShip.GetWhere()
	db.Model(&model.Volume{}).Where(where).Where("id NOT IN (SELECT volume_id FROM consistency_group_volumes WHERE deleted_at IS NULL)").
		Where("status IN (?)", []string{"available", "attached"}).
		Find(&availableVolumes)

	c.Data["CG"] = cg
	c.Data["Volumes"] = volumes
	c.Data["AvailableVolumes"] = availableVolumes
	c.Data["SnapshotCount"] = snapshotCount
	c.Data["CanModify"] = snapshotCount == 0 && cg.IsAvailable()
	c.HTML(200, "cgroups_volumes")
}

// AddVolumes handles adding volumes to a consistency group
// 处理向一致性组添加卷
func (v *ConsistencyGroupView) AddVolumes(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	volumeUUIDs := c.Req.Form["volumes"]
	if len(volumeUUIDs) == 0 {
		c.Data["ErrorMsg"] = "No volumes selected"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	_, err = consistencyGroupAdmin.AddVolumes(c.Req.Context(), cgID, volumeUUIDs)
	if err != nil {
		logger.Error("Failed to add volumes to consistency group", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	c.Redirect(fmt.Sprintf("/cgroups/%d/volumes", cgID))
}

// RemoveVolume handles removing a volume from a consistency group
// 处理从一致性组删除卷
func (v *ConsistencyGroupView) RemoveVolume(c *macaron.Context, store session.Store) error {
	id := c.Params("id")
	volumeID := c.Params("volumeid")
	logger.Debugf("RemoveVolume handler called with id=%s, volume_id=%s", id, volumeID)

	if id == "" || volumeID == "" {
		c.Data["ErrorMsg"] = "ID is empty"
		c.Error(http.StatusBadRequest)
		return nil
	}

	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid CG ID"
		c.Error(http.StatusBadRequest)
		return nil
	}

	// Get volume UUID from ID
	// 从 ID 获取卷 UUID
	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx
	volumeIDInt, err := strconv.ParseInt(volumeID, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid volume ID"
		c.Error(http.StatusBadRequest)
		return nil
	}
	volume := &model.Volume{Model: model.Model{ID: volumeIDInt}}
	if err = db.Take(volume).Error; err != nil {
		c.Data["ErrorMsg"] = "Volume not found"
		c.Error(http.StatusNotFound)
		return nil
	}

	_, err = consistencyGroupAdmin.RemoveVolume(c.Req.Context(), cgID, volume.UUID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return nil
	}

	c.JSON(200, map[string]interface{}{
		"redirect": fmt.Sprintf("/cgroups/%d/volumes", cgID),
	})
	return nil
}

// ListSnapshots displays the snapshot list page for a consistency group
// 显示一致性组的快照列表页面
func (v *ConsistencyGroupView) ListSnapshots(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	// Permission check
	// 权限检查
	permit = memberShip.ValidateOwner(model.Reader, cg.Owner)
	if !permit {
		logger.Error("Not authorized to view this consistency group")
		c.Data["ErrorMsg"] = "Not authorized to view this consistency group"
		c.HTML(http.StatusForbidden, "error")
		return
	}

	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}

	total, snapshots, err := consistencyGroupAdmin.ListSnapshots(c.Req.Context(), cgID, offset, limit, order)
	if err != nil {
		logger.Error("Failed to list snapshots", err)
		c.Data["ErrorMsg"] = "Failed to list snapshots"
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	// Preload tasks for each snapshot
	// 为每个快照预加载任务
	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx
	for _, snap := range snapshots {
		if snap.TaskID > 0 {
			snap.Task = &model.Task{Model: model.Model{ID: snap.TaskID}}
			db.Take(snap.Task)
		}
	}

	pages := GetPages(total, limit)
	c.Data["CG"] = cg
	c.Data["Snapshots"] = snapshots
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["CGLink"] = fmt.Sprintf("/cgroups/%d", cgID)
	c.HTML(200, "cg_snapshots")
}

// NewSnapshot displays the create snapshot page
// 显示创建快照页面
func (v *ConsistencyGroupView) NewSnapshot(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	// Permission check
	// 权限检查
	permit = memberShip.ValidateOwner(model.Writer, cg.Owner)
	if !permit {
		logger.Error("Not authorized to create snapshot for this consistency group")
		c.Data["ErrorMsg"] = "Not authorized to create snapshot for this consistency group"
		c.HTML(http.StatusForbidden, "error")
		return
	}

	// Get volume count for display
	// 获取卷数量用于显示
	_, db := GetContextDB(c.Req.Context())
	var volumeCount int64
	db.Model(&model.ConsistencyGroupVolume{}).Where("cg_id = ?", cgID).Count(&volumeCount)

	c.Data["CG"] = cg
	c.Data["VolumeCount"] = volumeCount
	c.HTML(200, "cg_snapshot_new")
}

// CreateSnapshot handles creating a snapshot for a consistency group
// 处理为一致性组创建快照
func (v *ConsistencyGroupView) CreateSnapshot(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	name := c.QueryTrim("name")
	description := c.QueryTrim("description")

	if name == "" {
		c.Data["ErrorMsg"] = "Snapshot name is required"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	_, err = consistencyGroupAdmin.CreateSnapshot(c.Req.Context(), cg.UUID, name, description)
	if err != nil {
		logger.Error("Failed to create snapshot", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	c.Redirect(fmt.Sprintf("../cgroups/%d/snapshots", cgID))
}

// DeleteSnapshot handles deleting a snapshot
// 处理删除快照
func (v *ConsistencyGroupView) DeleteSnapshot(c *macaron.Context, store session.Store) error {
	id := c.Params("id")
	snapID := c.Params("snap_id")

	if id == "" || snapID == "" {
		c.Data["ErrorMsg"] = "ID is empty"
		c.Error(http.StatusBadRequest)
		return nil
	}

	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid CG ID"
		c.Error(http.StatusBadRequest)
		return nil
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.Error(http.StatusNotFound)
		return nil
	}

	// Get snapshot UUID from ID
	// 从 ID 获取快照 UUID
	ctx, db := GetContextDB(c.Req.Context())
	_ = ctx
	snapIDInt, err := strconv.ParseInt(snapID, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid snapshot ID"
		c.Error(http.StatusBadRequest)
		return nil
	}
	snapshot := &model.ConsistencyGroupSnapshot{Model: model.Model{ID: snapIDInt}}
	if err = db.Take(snapshot).Error; err != nil {
		c.Data["ErrorMsg"] = "Snapshot not found"
		c.Error(http.StatusNotFound)
		return nil
	}

	err = consistencyGroupAdmin.DeleteSnapshot(c.Req.Context(), cg.UUID, snapshot.UUID)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return nil
	}

	c.JSON(200, map[string]interface{}{
		"redirect": fmt.Sprintf("cgroups/%d/snapshots", cgID),
	})
	return nil
}

// Restore handles restoring a consistency group from a snapshot
// 处理从快照恢复一致性组
func (v *ConsistencyGroupView) Restore(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	id := c.Params("id")
	cgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	cg, err := consistencyGroupAdmin.Get(c.Req.Context(), cgID)
	if err != nil {
		logger.Error("Failed to get consistency group", err)
		c.Data["ErrorMsg"] = "Consistency group not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	// Get snapshot ID from form
	// 从表单获取快照 ID
	snapshotIDStr := c.QueryTrim("snapshot_id")
	if snapshotIDStr == "" {
		c.Data["ErrorMsg"] = "Snapshot ID is required"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	snapshotID, err := strconv.ParseInt(snapshotIDStr, 10, 64)
	if err != nil {
		c.Data["ErrorMsg"] = "Invalid snapshot ID"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	// Get snapshot UUID
	// 获取快照 UUID
	snapshot, err := consistencyGroupAdmin.GetSnapshot(c.Req.Context(), snapshotID)
	if err != nil {
		c.Data["ErrorMsg"] = "Snapshot not found"
		c.HTML(http.StatusNotFound, "error")
		return
	}

	_, err = consistencyGroupAdmin.RestoreSnapshot(c.Req.Context(), cg.UUID, snapshot.UUID)
	if err != nil {
		logger.Error("Failed to restore snapshot", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "error")
		return
	}

	c.Redirect("/tasks")
}
