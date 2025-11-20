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
	//|:-COMMAND-:| create_snapshot_wds_vhost.sh '$backup_ID' '$state' 'wds_vhost://$wdsPoolID/$snapshot_id' '$snapshot_size' 'success'
	logger.Debug("BackupVolumeWDSVhost", args)
	if len(args) < 6 {
		logger.Errorf("Invalid args for create_snapshot_wds_vhost: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}
	backupID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid backup ID: %v", args[1])
		return
	}
	size, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil {
		logger.Errorf("Invalid backup/snapshot size: %v", args[4])
		size = 0
	}
	if size > 0 {
		size = size / 1024 / 1024 / 1024 // convert to GB
	}
	status = args[2]
	path := args[3]
	// message := args[4]
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	backup := &model.VolumeBackup{Model: model.Model{ID: int64(backupID)}}
	err = db.Preload("Volume").Where(backup).Take(backup).Error
	if err != nil {
		logger.Error("Invalid backup ID", err)
		return
	}
	err = db.Model(backup).Updates(map[string]interface{}{"path": path, "status": status, "size": size}).Error
	if err != nil {
		logger.Errorf("Failed to update backup %d: %v", backupID, err)
		return "", err
	}
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
	//|:-COMMAND-:| create_snapshot_wds_vhost.sh 5 /volume-12.disk available reason
	logger.Debug("restore_snapshot_wds_vhost", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for restore_snapshot_wds_vhost: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}
	volumeID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid volume ID: %v", args[1])
		return
	}
	status = args[2]
	path := args[3]
	// message := args[4]
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	volume := &model.Volume{Model: model.Model{ID: int64(volumeID)}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	if status == string(model.VolumeStatusAvailable) {
		if volume.InstanceID > 0 {
			status = string(model.VolumeStatusAttached)
		}
	}
	err = db.Model(&volume).Updates(map[string]interface{}{"path": path, "status": status}).Error
	if err != nil {
		logger.Errorf("Failed to update volume %d: %v", volumeID, err)
		return "", err
	}
	return
}
