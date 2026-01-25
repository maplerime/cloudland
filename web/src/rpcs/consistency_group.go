package rpcs

import (
	"context"
	"fmt"
	"strconv"
	. "web/src/common"
	"web/src/model"
)

func init() {
	// Phase 1 & 2 callbacks
	Add("create_cg_wds", CreateCGWDS)
	Add("delete_cg_wds", DeleteCGWDS)
	Add("add_volumes_to_cg_wds", AddVolumesToCGWDS)
	Add("remove_volumes_from_cg_wds", RemoveVolumesFromCGWDS)

	// Phase 3 callbacks (snapshots)
	Add("create_cg_snapshot_wds", CreateCGSnapshotWDS)
	Add("delete_cg_snapshot_wds", DeleteCGSnapshotWDS)
	Add("restore_cg_snapshot_wds", RestoreCGSnapshotWDS)
}

// CreateCGWDS handles the callback from create_cg_wds.sh script
// 处理创建一致性组脚本的回调
// |:-COMMAND-:| create_cg_wds.sh '<cg_id>' '<status>' '<wds_cg_id>' 'message'
func CreateCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("CreateCGWDS", args)
	if len(args) < 5 {
		logger.Errorf("Invalid args for create_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	cgID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[1])
		return
	}
	status = args[2]
	wdsCgID := args[3]
	message := args[4]

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
	cg := &model.ConsistencyGroup{Model: model.Model{ID: cgID}}
	err = db.Where(cg).Take(cg).Error
	if err != nil {
		logger.Error("Invalid CG ID", err)
		return
	}

	if status == "available" {
		// Update to available status with WDS CG ID
		// 更新为可用状态并设置 WDS CG ID
		err = db.Model(cg).Updates(map[string]interface{}{
			"status":    model.CGStatusAvailable,
			"wds_cg_id": wdsCgID,
		}).Error
	} else {
		// Update to error status
		// 更新为错误状态
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusError,
		}).Error
		logger.Errorf("CG creation failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update consistency group %d: %v", cgID, err)
		return
	}

	logger.Debugf("Successfully updated consistency group %d to status %s", cgID, status)
	return
}

// DeleteCGWDS handles the callback from delete_cg_wds.sh script
// 处理删除一致性组脚本的回调
// |:-COMMAND-:| delete_cg_wds.sh '<cg_id>' '<status>' 'message'
func DeleteCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("DeleteCGWDS", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for delete_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	cgID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[1])
		return
	}
	status = args[2]
	message := args[3]

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	cg := &model.ConsistencyGroup{Model: model.Model{ID: cgID}}

	if status == "deleted" {
		// Delete volume associations
		// 删除卷关联
		err = db.Where("cg_id = ?", cgID).Delete(&model.ConsistencyGroupVolume{}).Error
		if err != nil {
			logger.Errorf("Failed to delete CG volume associations: %v", err)
			return
		}

		// Delete consistency group
		// 删除一致性组
		err = db.Delete(cg).Error
		if err != nil {
			logger.Errorf("Failed to delete consistency group %d: %v", cgID, err)
			return
		}
		logger.Debugf("Successfully deleted consistency group %d", cgID)
	} else {
		// Update status to error
		// 更新状态为错误
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusError,
		}).Error
		if err != nil {
			logger.Errorf("Failed to update consistency group %d: %v", cgID, err)
			return
		}
		logger.Errorf("CG deletion failed: %s", message)
	}

	return
}

// AddVolumesToCGWDS handles the callback from add_volumes_to_cg_wds.sh script
// 处理向一致性组添加卷脚本的回调
// |:-COMMAND-:| add_volumes_to_cg_wds.sh '<cg_id>' '<status>' 'message'
func AddVolumesToCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("AddVolumesToCGWDS", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for add_volumes_to_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	cgID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[1])
		return
	}
	status = args[2]
	message := args[3]

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
	cg := &model.ConsistencyGroup{Model: model.Model{ID: cgID}}
	err = db.Where(cg).Take(cg).Error
	if err != nil {
		logger.Error("Invalid CG ID", err)
		return
	}

	if status == "available" {
		// Update to available status
		// 更新为可用状态
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusAvailable,
		}).Error
	} else {
		// Update to error status
		// 更新为错误状态
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusError,
		}).Error
		logger.Errorf("Add volumes to CG failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update consistency group %d: %v", cgID, err)
		return
	}

	logger.Debugf("Successfully updated consistency group %d to status %s", cgID, status)
	return
}

// RemoveVolumesFromCGWDS handles the callback from remove_volumes_from_cg_wds.sh script
// 处理从一致性组删除卷脚本的回调
// |:-COMMAND-:| remove_volumes_from_cg_wds.sh '<cg_id>' '<status>' 'message'
func RemoveVolumesFromCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("RemoveVolumesFromCGWDS", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for remove_volumes_from_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	cgID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[1])
		return
	}
	status = args[2]
	message := args[3]

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
	cg := &model.ConsistencyGroup{Model: model.Model{ID: cgID}}
	err = db.Where(cg).Take(cg).Error
	if err != nil {
		logger.Error("Invalid CG ID", err)
		return
	}

	if status == "available" {
		// Update to available status
		// 更新为可用状态
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusAvailable,
		}).Error
	} else {
		// Update to error status
		// 更新为错误状态
		err = db.Model(cg).Updates(map[string]interface{}{
			"status": model.CGStatusError,
		}).Error
		logger.Errorf("Remove volumes from CG failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update consistency group %d: %v", cgID, err)
		return
	}

	logger.Debugf("Successfully updated consistency group %d to status %s", cgID, status)
	return
}

// CreateCGSnapshotWDS handles the callback from create_cg_snapshot_wds.sh script
// 处理创建一致性组快照脚本的回调
// |:-COMMAND-:| create_cg_snapshot_wds.sh '<snapshot_ID>' '<status>' '<wds_snap_id>' '<size>' 'message'
func CreateCGSnapshotWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("CreateCGSnapshotWDS", args)
	if len(args) < 6 {
		logger.Errorf("Invalid args for create_cg_snapshot_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	snapshotID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid snapshot ID: %v", args[1])
		return
	}
	status = args[2]
	wdsSnapID := args[3]

	size, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil {
		logger.Errorf("Invalid snapshot size: %v, defaulting to 0", args[4])
		size = 0
		err = nil
	}
	// Convert from bytes to GB
	if size > 0 {
		size = size / 1024 / 1024 / 1024
	}

	message := args[5]

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Get CG snapshot
	// 获取一致性组快照
	snapshot := &model.ConsistencyGroupSnapshot{Model: model.Model{ID: snapshotID}}
	err = db.Preload("CG").Where(snapshot).Take(snapshot).Error
	if err != nil {
		logger.Error("Invalid CG snapshot ID", err)
		return
	}

	if status == "available" {
		// Update to available status with WDS snapshot ID
		// 更新为可用状态并设置 WDS 快照 ID
		err = db.Model(snapshot).Updates(map[string]interface{}{
			"status":      model.CGSnapshotStatusAvailable,
			"wds_snap_id": wdsSnapID,
			"size":        size,
			"task_id":     int64(0),
		}).Error
	} else {
		// Update to error status
		// 更新为错误状态
		err = db.Model(snapshot).Updates(map[string]interface{}{
			"status":  model.CGSnapshotStatusError,
			"task_id": int64(0),
		}).Error
		logger.Errorf("CG snapshot creation failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update CG snapshot %d: %v", snapshotID, err)
		return
	}

	// Restore all volumes' status to available (or attached)
	// 恢复所有卷的状态为 available（或 attached）
	var cgVolumes []*model.ConsistencyGroupVolume
	err = db.Preload("Volume").Where("cg_id = ?", snapshot.CGID).Find(&cgVolumes).Error
	if err != nil {
		logger.Errorf("Failed to get CG volumes: %v", err)
		return
	}

	for _, cgv := range cgVolumes {
		volStatus := model.VolumeStatusAvailable
		if cgv.Volume.InstanceID > 0 {
			volStatus = model.VolumeStatusAttached
		}
		err = db.Model(&model.Volume{}).Where("id = ?", cgv.VolumeID).
			Update("status", volStatus).Error
		if err != nil {
			logger.Errorf("Failed to update volume %d status: %v", cgv.VolumeID, err)
			return
		}
	}

	logger.Debugf("Successfully updated CG snapshot %d to status %s", snapshotID, status)
	return
}

// DeleteCGSnapshotWDS handles the callback from delete_cg_snapshot_wds.sh script
// 处理删除一致性组快照脚本的回调
// |:-COMMAND-:| delete_cg_snapshot_wds.sh '<snapshot_ID>' '<status>' 'message'
func DeleteCGSnapshotWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("DeleteCGSnapshotWDS", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for delete_cg_snapshot_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	snapshotID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid snapshot ID: %v", args[1])
		return
	}
	status = args[2]
	message := args[3]

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	if status == "deleted" {
		// Delete the snapshot record from database
		// 从数据库删除快照记录
		err = db.Delete(&model.ConsistencyGroupSnapshot{}, snapshotID).Error
		if err != nil {
			logger.Errorf("Failed to delete CG snapshot %d: %v", snapshotID, err)
			return
		}
		logger.Debugf("Successfully deleted CG snapshot %d", snapshotID)
	} else {
		// Update status to error
		// 更新状态为错误
		err = db.Model(&model.ConsistencyGroupSnapshot{}).
			Where("id = ?", snapshotID).
			Updates(map[string]interface{}{
				"status": model.CGSnapshotStatusError,
			}).Error
		if err != nil {
			logger.Errorf("Failed to update CG snapshot %d: %v", snapshotID, err)
			return
		}
		logger.Errorf("CG snapshot deletion failed: %s", message)
	}

	return
}

// RestoreCGSnapshotWDS handles the callback from restore_cg_snapshot_wds.sh script
// 处理恢复一致性组快照脚本的回调
// |:-COMMAND-:| restore_cg_snapshot_wds.sh '<snapshot_ID>' '<cg_ID>' '<status>' 'message'
func RestoreCGSnapshotWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("RestoreCGSnapshotWDS", args)
	if len(args) < 5 {
		logger.Errorf("Invalid args for restore_cg_snapshot_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	snapshotID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid snapshot ID: %v", args[1])
		return
	}

	cgID, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[2])
		return
	}

	status = args[3]
	message := args[4]

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// Update CG snapshot status
	// 更新一致性组快照状态
	snapshot := &model.ConsistencyGroupSnapshot{Model: model.Model{ID: snapshotID}}
	err = db.Where(snapshot).Take(snapshot).Error
	if err != nil {
		logger.Error("Invalid CG snapshot ID", err)
		return
	}

	if status == "available" {
		// Update snapshot status to available
		// 更新快照状态为可用
		err = db.Model(snapshot).Updates(map[string]interface{}{
			"status":  model.CGSnapshotStatusAvailable,
			"task_id": int64(0),
		}).Error
	} else {
		// Update status to error
		// 更新状态为错误
		err = db.Model(snapshot).Updates(map[string]interface{}{
			"status":  model.CGSnapshotStatusError,
			"task_id": int64(0),
		}).Error
		logger.Errorf("CG snapshot restore failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update CG snapshot %d: %v", snapshotID, err)
		return
	}

	// Restore all volumes' status to available (or attached)
	// 恢复所有卷的状态为 available（或 attached）
	var cgVolumes []*model.ConsistencyGroupVolume
	err = db.Preload("Volume").Where("cg_id = ?", cgID).Find(&cgVolumes).Error
	if err != nil {
		logger.Errorf("Failed to get CG volumes: %v", err)
		return
	}

	for _, cgv := range cgVolumes {
		volStatus := model.VolumeStatusAvailable
		if cgv.Volume.InstanceID > 0 {
			volStatus = model.VolumeStatusAttached
		}
		err = db.Model(&model.Volume{}).Where("id = ?", cgv.VolumeID).
			Update("status", volStatus).Error
		if err != nil {
			logger.Errorf("Failed to update volume %d status: %v", cgv.VolumeID, err)
			return
		}
	}

	logger.Debugf("Successfully restored CG %d from snapshot %d with status %s", cgID, snapshotID, status)
	return
}
