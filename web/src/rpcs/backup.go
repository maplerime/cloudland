package rpcs

import (
	"context"
	"fmt"
	"strconv"
	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("create_snapshot_wds_vhost", BackupVolumeWDSVhost)
	Add("restore_snapshot_wds_vhost", RestoreVolumeWDSVhost)
}

func BackupVolumeWDSVhost(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| create_snapshot_wds_vhost.sh '$task_ID' '$backup_ID' '$state' 'wds_vhost://$wdsPoolID/$snapshot_id' '$snapshot_size' '$middle_snapshot_id' 'success'
	logger.Debug("BackupVolumeWDSVhost", args)
	if len(args) < 8 {
		logger.Errorf("Invalid args for create_snapshot_wds_vhost: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}
	taskID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid task ID: %v", args[1])
		return
	}
	backupID, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		logger.Errorf("Invalid backup ID: %v", args[1])
		return
	}
	size, err := strconv.ParseInt(args[5], 10, 64)
	if err != nil {
		logger.Errorf("Invalid backup/snapshot size: %v", args[5])
		size = 0
	}
	if size > 0 {
		size = size / 1024 / 1024 // convert to MB
	}
	status = args[3]
	path := args[4]
	middleSnapshotID := args[6]
	message := args[7]
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	// update task
	task := &model.Task{Model: model.Model{ID: taskID}}
	err = db.Where(task).Take(task).Error
	if err != nil {
		logger.Error("Invalid task ID", err)
		return
	}
	if status == "available" {
		err = db.Model(task).Updates(map[string]interface{}{"status": model.TaskStatusSuccess}).Error
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", taskID, err)
			return "", err
		}
	} else {
		err = db.Model(task).Updates(map[string]interface{}{"status": model.TaskStatusFailed, "message": message}).Error
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", taskID, err)
			return "", err
		}
	}

	// update backup
	backup := &model.VolumeBackup{Model: model.Model{ID: int64(backupID)}}
	err = db.Preload("Volume").Where(backup).Take(backup).Error
	if err != nil {
		logger.Error("Invalid backup ID", err)
		return
	}
	err = db.Model(backup).Updates(map[string]interface{}{"path": path, "status": status, "size": size, "snapshot_id": middleSnapshotID, "task_id": 0}).Error
	if err != nil {
		logger.Errorf("Failed to update backup %d: %v", backupID, err)
		return "", err
	}

	// update volume
	volume := backup.Volume
	volume.Status = model.VolumeStatusAvailable
	if volume.InstanceID > 0 {
		volume.Status = model.VolumeStatusAttached
	}
	err = db.Model(volume).Updates(map[string]interface{}{"status": volume.Status}).Error
	if err != nil {
		logger.Errorf("Failed to update volume %d: %v", volume.ID, err)
		return "", err
	}
	return
}

func RestoreVolumeWDSVhost(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| restore_snapshot_wds_vhost '$task_ID' '$backup_id' '$origin_vol_ID' '$state' '$vol_path' 'success'
	logger.Debug("restore_snapshot_wds_vhost", args)
	if len(args) < 6 {
		logger.Errorf("Invalid args for restore_snapshot_wds_vhost: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}
	taskID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid task ID: %v", args[1])
		return
	}
	backupID, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		logger.Errorf("Invalid backup ID: %v", args[2])
		return
	}
	volumeID, err := strconv.ParseInt(args[3], 10, 64)
	if err != nil {
		logger.Errorf("Invalid volume ID: %v", args[3])
		return
	}
	status = args[4]
	path := args[5]
	// message := args[4]
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	// update task
	task := &model.Task{Model: model.Model{ID: int64(taskID)}}
	err = db.Where(task).Take(task).Error
	if err != nil {
		logger.Error("Invalid task ID", err)
		return
	}
	if status == "failed_to_restore" {
		err = db.Model(task).Updates(map[string]interface{}{"status": model.TaskStatusFailed, "message": status}).Error
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", taskID, err)
			return "", err
		}
	} else {
		err = db.Model(task).Updates(map[string]interface{}{"status": model.TaskStatusSuccess}).Error
		if err != nil {
			logger.Errorf("Failed to update task %d: %v", taskID, err)
			return "", err
		}
	}

	// update backup
	backup := &model.VolumeBackup{Model: model.Model{ID: int64(backupID)}}
	err = db.Where(backup).Take(backup).Error
	if err != nil {
		logger.Error("Invalid backup ID", err)
		return
	}
	err = db.Model(&backup).Updates(map[string]interface{}{"task_id": 0}).Error
	if err != nil {
		logger.Errorf("Failed to update backup %d: %v", backupID, err)
		return "", err
	}

	// update volume
	volume := &model.Volume{Model: model.Model{ID: int64(volumeID)}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	volStatus := model.VolumeStatusAvailable
	if volume.InstanceID > 0 {
		volStatus = model.VolumeStatusAttached
	}
	err = db.Model(&volume).Updates(map[string]interface{}{"path": path, "status": volStatus}).Error
	if err != nil {
		logger.Errorf("Failed to update volume %d: %v", volumeID, err)
		return "", err
	}
	return
}
