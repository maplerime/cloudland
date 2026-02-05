package rpcs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

// getArgSafe returns the arg at the given index, or the default value if the index is out of range
// 安全地获取参数，超出范围时返回默认值
func getArgSafe(args []string, index int, defaultVal string) string {
	if index < len(args) {
		return args[index]
	}
	return defaultVal
}

// joinRemainingArgs joins the remaining args from the given index as the message
// 将指定索引之后的参数合并为消息
func joinRemainingArgs(args []string, index int) string {
	if index < len(args) {
		return strings.Join(args[index:], " ")
	}
	return ""
}

// CreateCGWDS handles the callback from create_cg_wds.sh script
// 处理创建一致性组脚本的回调
// |:-COMMAND-:| create_cg_wds.sh '<task_id>' '<cg_id>' '<status>' '<wds_cg_id>' 'message'
func CreateCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("CreateCGWDS", args)
	// Minimum: basename + task_id + cg_id + status
	if len(args) < 4 {
		logger.Errorf("Invalid args for create_cg_wds: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}

	// Parse arguments
	// 解析参数
	taskID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid task ID: %v", args[1])
		return
	}
	cgID, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		logger.Errorf("Invalid CG ID: %v", args[2])
		return
	}
	status = getArgSafe(args, 3, "error")
	wdsCgID := getArgSafe(args, 4, "")
	message := joinRemainingArgs(args, 5)

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

	// Update task status and message
	// 更新任务状态和消息
	if taskID > 0 {
		if status == "available" {
			err = db.Model(&model.Task{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{"status": model.TaskStatusSuccess}).Error
		} else {
			err = db.Model(&model.Task{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{"status": model.TaskStatusFailed, "message": message}).Error
		}
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", taskID, err)
		}
	}

	logger.Debugf("Successfully updated consistency group %d to status %s", cgID, status)
	return
}

// DeleteCGWDS handles the callback from delete_cg_wds.sh script
// 处理删除一致性组脚本的回调
// |:-COMMAND-:| delete_cg_wds.sh '<cg_id>' '<status>' 'message'
func DeleteCGWDS(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("DeleteCGWDS", args)
	// Minimum: basename + cg_id + status
	if len(args) < 3 {
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
	status = getArgSafe(args, 2, "error")
	message := joinRemainingArgs(args, 3)

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
	// Minimum: basename + cg_id + status
	if len(args) < 3 {
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
	status = getArgSafe(args, 2, "error")
	message := joinRemainingArgs(args, 3)

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
	// Minimum: basename + cg_id + status
	if len(args) < 3 {
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
	status = getArgSafe(args, 2, "error")
	message := joinRemainingArgs(args, 3)

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
	// Minimum: basename + snapshot_ID + status
	if len(args) < 3 {
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
	status = getArgSafe(args, 2, "error")
	wdsSnapID := getArgSafe(args, 3, "")

	var size int64
	sizeStr := getArgSafe(args, 4, "0")
	size, err = strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		logger.Errorf("Invalid snapshot size: %v, defaulting to 0", sizeStr)
		size = 0
		err = nil
	}
	// Convert from bytes to GB
	if size > 0 {
		size = size / 1024 / 1024 / 1024
	}

	message := joinRemainingArgs(args, 5)

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
		}).Error
	} else {
		// Update to error status
		// 更新为错误状态
		err = db.Model(snapshot).Updates(map[string]interface{}{
			"status": model.CGSnapshotStatusError,
		}).Error
		logger.Errorf("CG snapshot creation failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update CG snapshot %d: %v", snapshotID, err)
		return
	}

	// Update task status and message
	// 更新任务状态和消息
	if snapshot.TaskID > 0 {
		if status == "available" {
			err = db.Model(&model.Task{}).Where("id = ?", snapshot.TaskID).
				Updates(map[string]interface{}{"status": model.TaskStatusSuccess}).Error
		} else {
			err = db.Model(&model.Task{}).Where("id = ?", snapshot.TaskID).
				Updates(map[string]interface{}{"status": model.TaskStatusFailed, "message": message}).Error
		}
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", snapshot.TaskID, err)
		}
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
	// Minimum: basename + snapshot_ID + status
	if len(args) < 3 {
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
	status = getArgSafe(args, 2, "error")
	message := joinRemainingArgs(args, 3)

	// Start transaction
	// 开始事务
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	if status == "deleted" {
		// Get snapshot to retrieve task ID before deletion
		// 删除前获取快照记录以获取任务 ID
		snapshot := &model.ConsistencyGroupSnapshot{Model: model.Model{ID: snapshotID}}
		if dbErr := db.Take(snapshot).Error; dbErr != nil {
			logger.Errorf("Failed to get CG snapshot %d before deletion: %v", snapshotID, dbErr)
		}
		taskID := snapshot.TaskID

		// Delete the snapshot record from database
		// 从数据库删除快照记录
		err = db.Delete(&model.ConsistencyGroupSnapshot{}, snapshotID).Error
		if err != nil {
			logger.Errorf("Failed to delete CG snapshot %d: %v", snapshotID, err)
			return
		}

		// Update task status to success
		// 更新任务状态为成功
		if taskID > 0 {
			if taskErr := db.Model(&model.Task{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{"status": model.TaskStatusSuccess}).Error; taskErr != nil {
				logger.Errorf("Failed to update task %d: %v", taskID, taskErr)
			}
		}
		logger.Debugf("Successfully deleted CG snapshot %d", snapshotID)
	} else {
		// Get snapshot to retrieve task ID
		// 获取快照记录以获取任务 ID
		snapshot := &model.ConsistencyGroupSnapshot{Model: model.Model{ID: snapshotID}}
		if dbErr := db.Take(snapshot).Error; dbErr != nil {
			logger.Errorf("Failed to get CG snapshot %d: %v", snapshotID, dbErr)
		}
		taskID := snapshot.TaskID

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

		// Update task status to failed
		// 更新任务状态为失败
		if taskID > 0 {
			if taskErr := db.Model(&model.Task{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{"status": model.TaskStatusFailed, "message": message}).Error; taskErr != nil {
				logger.Errorf("Failed to update task %d: %v", taskID, taskErr)
			}
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
	// Minimum: basename + snapshot_ID + cg_ID + status
	if len(args) < 4 {
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

	status = getArgSafe(args, 3, "error")
	message := joinRemainingArgs(args, 4)

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

	// Save task ID before updating snapshot (GORM may reflect changes to struct)
	// 更新快照前先保存 task_id（GORM 可能将变更反映到 struct）
	taskID := snapshot.TaskID

	// Always restore snapshot status to available (restore failure doesn't mean snapshot is broken)
	// 无论恢复成功或失败，快照状态都恢复为 available（恢复失败不代表快照本身有问题）
	err = db.Model(snapshot).Updates(map[string]interface{}{
		"status": model.CGSnapshotStatusAvailable,
	}).Error
	if status != "available" {
		logger.Errorf("CG snapshot restore failed: %s", message)
	}

	if err != nil {
		logger.Errorf("Failed to update CG snapshot %d: %v", snapshotID, err)
		return
	}

	// Update task status and message
	// 更新任务状态和消息
	if taskID > 0 {
		if status == "available" {
			err = db.Model(&model.Task{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{"status": model.TaskStatusSuccess}).Error
		} else {
			err = db.Model(&model.Task{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{"status": model.TaskStatusFailed, "message": message}).Error
		}
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", taskID, err)
		}
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
