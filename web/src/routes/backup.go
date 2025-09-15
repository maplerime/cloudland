/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
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
	backupAdmin = &BackupAdmin{}
	backupView  = &BackupView{}
)

type BackupAdmin struct{}
type BackupView struct{}

// volume backup and snapshot functions
// backup volume to another pool, this is an async operation and will return the task ID
// if the poolID is empty, the backup will be done to the same pool with snapshot
func (a *BackupAdmin) CreateBackupByID(ctx context.Context, volumeID int64, poolID string, name string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Backup volume by ID %d to pool %s", volumeID, poolID)
	volume, err := volumeAdmin.Get(ctx, volumeID)
	if err != nil {
		logger.Error("Failed to get volume", err)
		err = NewCLError(ErrVolumeNotFound, "Volume not found", nil)
		return
	}
	// check the permission
	return a.createBackup(ctx, volume, poolID, name)
}

func (a *BackupAdmin) CreateBackupByUUID(ctx context.Context, uuid string, poolID string, name string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Backup volume by UUID %d to pool %s", uuid, poolID)
	volume, err := volumeAdmin.GetVolumeByUUID(ctx, uuid)
	if err != nil {
		logger.Error("Failed to get volume", err)
		err = NewCLError(ErrVolumeNotFound, "Volume not found", nil)
		return
	}
	// check the permission
	return a.createBackup(ctx, volume, poolID, name)
}

func (a *BackupAdmin) createBackup(ctx context.Context, volume *model.Volume, poolID string, name string) (backup *model.VolumeBackup, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, volume.Owner)
	if !permit {
		logger.Errorf("Not authorized to backup volume(%s)", volume.UUID)
		err = NewCLError(ErrPermissionDenied, "Not authorized to backup the volume", nil)
		return
	}
	if volume.Status == model.VolumeStatusAttaching || volume.Status == model.VolumeStatusDetaching || volume.Status == model.VolumeStatusResizing {
		msg := fmt.Sprintf("Volume %s is in %s state, cannot backup now", volume.UUID, volume.Status)
		logger.Errorf(msg)
		err = NewCLError(ErrVolumeIsBusy, msg, nil)
		return
	}
	backup, err = a.createBackupModel(ctx, name, "backup", volume, "")
	if err != nil {
		logger.Errorf("Failed to create backup record for volume(%s), %+v", volume.UUID, err)
		err = NewCLError(ErrDatabaseError, "Failed to create backup record", err)
		return
	}
	control := fmt.Sprintf("inter=")
	vol_driver := GetVolumeDriver()
	if vol_driver != "local" {
		wdsUUID := volume.GetOriginVolumeID()
		wdsOriginPoolID := volume.GetVolumePoolID()
		if poolID != "" && poolID != wdsOriginPoolID {
			logger.Debugf("Backup volume %s from pool %s to pool %s", volume.UUID, wdsOriginPoolID, poolID)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_snapshot_%s.sh '%s' '%s' '%s' '%s' '%s' '%s' '%s'", vol_driver, backup.ID, backup.UUID, backup.Name, volume.ID, wdsUUID, wdsOriginPoolID, poolID)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Backup volume execution failed", err)
				return
			}
			return
		} else {
			logger.Debugf("Backup volume %s to same pool %s, use snapshot", volume.UUID, poolID)
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/create_snapshot_%s.sh '%s' '%s' '%s' '%s' '%s' '%s'", vol_driver, backup.ID, backup.UUID, backup.Name, volume.ID, wdsUUID, wdsOriginPoolID)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Backup volume execution failed", err)
				return
			}
		}
	} else {
		logger.Error("Backup not supported for local volume")
		err = fmt.Errorf("Backup not supported for local volume")
		return
	}
	return
}

// snapshot volume, this is an async operation and will return the task ID
func (a *BackupAdmin) CreateSnapshotByID(ctx context.Context, volumeID int64, name string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Snapshot volume by ID %d", volumeID)
	volume, err := volumeAdmin.Get(ctx, volumeID)
	if err != nil {
		logger.Error("Failed to get volume", err)
		return
	}
	return a.createSnapshot(ctx, name, volume)
}

func (a *BackupAdmin) CreateSnapshotByUUID(ctx context.Context, uuid, name string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Snapshot volume by UUID %d", uuid)
	volume, err := volumeAdmin.GetVolumeByUUID(ctx, uuid)
	if err != nil {
		logger.Error("Failed to get volume", err)
		return
	}
	return a.createSnapshot(ctx, name, volume)
}

func (a *BackupAdmin) createSnapshot(ctx context.Context, name string, volume *model.Volume) (backup *model.VolumeBackup, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, volume.Owner)
	if !permit {
		logger.Error("Not authorized to snapshot volume")
		err = fmt.Errorf("Not authorized")
		return
	}
	snapshot, err := a.createBackupModel(ctx, name, "snapshot", volume, "")
	if err != nil {
		logger.Error("Failed to create snapshot", err)
		return
	}
	control := fmt.Sprintf("inter=")
	vol_driver := GetVolumeDriver()
	uuid := volume.UUID
	logger.Debugf("creating snapshot (%s) for volume %s", snapshot.UUID, uuid)
	if vol_driver != "local" {
		wdsUUID := volume.GetOriginVolumeID()
		wdsOriginPoolID := volume.GetVolumePoolID()
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/snapshot_volume_%s.sh '%s' '%s' '%s' '%s'", vol_driver, backup.ID, backup.UUID, wdsUUID, wdsOriginPoolID)
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Backup volume execution failed", err)
			return
		}
	} else {
		logger.Error("Snapshot not supported for local volume")
		err = fmt.Errorf("Snapshot not supported for local volume")
		return
	}

	return
}

func (a *BackupAdmin) createBackupModel(ctx context.Context, name, backupType string, volume *model.Volume, poolID string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Creating backup model for volume %s", volume.UUID)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	backup = &model.VolumeBackup{
		Owner:      memberShip.OrgID,
		Name:       name,
		VolumeID:   volume.ID,
		BackupType: backupType,
		Status:     "pending",
	}
	err = db.Create(backup).Error
	if err != nil {
		logger.Error("DB failed to create backup", err)
		return
	}
	return
}

func (a *BackupAdmin) GetBackupByID(ctx context.Context, backupID int64) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Get backup by ID %d", backupID)
	if backupID <= 0 {
		err = fmt.Errorf("Invalid backup ID: %d", backupID)
		logger.Error(err)
		return
	}
	db := DB()
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	backup = &model.VolumeBackup{Model: model.Model{ID: backupID}}
	if err = db.Preload("Volume").Where(where).Take(backup).Error; err != nil {
		logger.Error("Failed to query volume, %v", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, backup.Owner)
	if !permit {
		logger.Error("Not authorized to read the backup")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *BackupAdmin) GetBackupByUUID(ctx context.Context, uuID string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Get backup by UUID %d", uuID)
	db := DB()
	memberShip := GetMemberShip(ctx)
	backup = &model.VolumeBackup{}
	where := memberShip.GetWhere()
	err = db.Preload("Volume").Where(where).Where("uuid = ?", uuID).Take(backup).Error
	if err != nil {
		logger.Error("DB: query backup failed", err)
		return
	}
	permit := memberShip.ValidateOwner(model.Reader, backup.Owner)
	if !permit {
		logger.Error("Not authorized to read the backup")
		err = fmt.Errorf("Not authorized")
		return
	}
	return
}

func (a *BackupAdmin) Delete(ctx context.Context, backup *model.VolumeBackup) (err error) {
	logger.Debugf("Delete backup %s", backup.UUID)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, backup.Owner)
	if !permit {
		logger.Error("Not authorized to delete the backup")
		err = fmt.Errorf("Not authorized")
		return
	}
	err = db.Delete(backup).Error
	if err != nil {
		logger.Error("DB: delete backup failed", err)
		return
	}
	vol_driver := GetVolumeDriver()
	control := fmt.Sprintf("inter=")
	wdsUUID := backup.GetOriginBackupID()
	command := fmt.Sprintf("/opt/cloudland/scripts/backend/delete_snapshot_%s.sh '%s'", vol_driver, wdsUUID)
	err = HyperExecute(ctx, control, command)
	if err != nil {
		logger.Error("Delete snapshot execution failed", err)
		return
	}
	return
}

func (a *BackupAdmin) DeleteByID(ctx context.Context, backupID int64) (err error) {
	logger.Debugf("Delete backup by ID %d", backupID)
	backup, err := a.GetBackupByID(ctx, backupID)
	if err != nil {
		logger.Error("Failed to get backup", err)
		return
	}
	return a.Delete(ctx, backup)
}

func (a *BackupAdmin) DeleteByUUID(ctx context.Context, uuID string) (err error) {
	logger.Debugf("Delete backup by UUID %d", uuID)
	backup, err := a.GetBackupByUUID(ctx, uuID)
	if err != nil {
		logger.Error("Failed to get backup", err)
		return
	}
	return a.Delete(ctx, backup)
}

func (a *BackupAdmin) Restore(ctx context.Context, backupID int64) (err error) {
	logger.Debugf("Restore volume from backup %d", backupID)
	backup, err := a.GetBackupByID(ctx, backupID)
	if err != nil {
		logger.Error("Failed to get backup", err)
		return
	}
	volume, err := volumeAdmin.Get(ctx, backup.VolumeID)
	if err != nil {
		logger.Error("Failed to get volume", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, volume.Owner)
	if !permit {
		logger.Errorf("Not authorized to restore volume(%s)", volume.UUID)
		err = fmt.Errorf("not authorized")
		return
	}
	// check if the instance is running, if so, ask the user to stop it first
	if volume.InstanceID > 0 && volume.Instance.Status != "stopped" {
		logger.Errorf("Volume %s is attached to a running instance, please stop the instance %s first", volume.Name, volume.Instance.Hostname)
		err = fmt.Errorf("volume %s is attached to a running instance, please stop the instance %s first", volume.Name, volume.Instance.Hostname)
		return
	}
	// update volume status to restoring
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	err = db.Model(&volume).Updates(map[string]interface{}{"status": "restoring"}).Error
	if err != nil {
		logger.Error("Update volume status failed", err)
		return
	}
	control := "inter="
	if volume.InstanceID > 0 {
		control = fmt.Sprintf("inter=%d", volume.Instance.Hyper)
	}
	vol_driver := volume.GetVolumeDriver()
	if vol_driver != "local" {
		volume_wds_uuid := volume.GetOriginVolumeID()
		snapshot_wds_uuid := backup.GetOriginBackupID()
		volume_pool_id := volume.GetVolumePoolID()
		snapshot_pool_id := backup.GetBackupPoolID()
		command := fmt.Sprintf("/opt/cloudland/scripts/backend/restore_snapshot_%s.sh '%d' '%d' '%d' '%s' '%s' '%s' '%s'", vol_driver, backupID, volume.ID, volume.InstanceID, volume_wds_uuid, snapshot_wds_uuid, volume_pool_id, snapshot_pool_id)
		if volume_pool_id == snapshot_pool_id {
			logger.Debugf("Restoring volume %s from snapshot %s in the same pool %s", volume.UUID, backup.UUID, volume_pool_id)
		} else {
			logger.Debugf("Restoring volume %s from snapshot %s in different pool, from %s to %s", volume.UUID, backup.UUID, snapshot_pool_id, volume_pool_id)
		}
		err = HyperExecute(ctx, control, command)
		if err != nil {
			logger.Error("Restore volume execution failed", err)
			return
		}
	} else {
		logger.Error("Restore not supported for local volume")
		err = fmt.Errorf("Restore not supported for local volume")
		return
	}
	return
}

func (a *BackupAdmin) List(ctx context.Context, offset, limit int64, order, query string, volumeID int64, backupType string) (total int64, backups []*model.VolumeBackup, err error) {
	logger.Debugf("List backup, offset %d, limit %d, order %s, query %s, volumeID %d, backupType %s", offset, limit, order, query, volumeID, backupType)
	memberShip := GetMemberShip(ctx)
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}
	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	memberShipSQL := memberShip.GetWhere()
	whereSQL := ""
	if volumeID > 0 {
		whereSQL = fmt.Sprintf("volume_id = %d", volumeID)
	}
	if backupType != "" {
		if whereSQL != "" {
			whereSQL = fmt.Sprintf("%s and backup_type = '%s'", whereSQL, backupType)
		} else {
			whereSQL = fmt.Sprintf("backup_type = '%s'", backupType)
		}
	}
	if query != "" {
		if whereSQL != "" {
			whereSQL = fmt.Sprintf("%s and %s", whereSQL, query)
		} else {
			whereSQL = query
		}
	}
	if whereSQL != "" {
		if err = db.Model(&model.VolumeBackup{}).Where(memberShipSQL).Where(whereSQL).Count(&total).Error; err != nil {
			logger.Error("DB: query backup count failed", err)
			return
		}
	} else {
		if err = db.Model(&model.VolumeBackup{}).Where(memberShipSQL).Count(&total).Error; err != nil {
			logger.Error("DB: query backup count failed", err)
			return
		}
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Preload("Volume").Where(memberShipSQL).Where(whereSQL).Find(&backups).Error; err != nil {
		logger.Error("DB: query backup failed", err)
		return
	}
	permit := memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, backup := range backups {
			backup.OwnerInfo = &model.Organization{Model: model.Model{ID: backup.Owner}}
			if err = db.Take(backup.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				return
			}
		}
	}
	return
}

func (a *BackupAdmin) Update(ctx context.Context, id int64, name, path, status string) (backup *model.VolumeBackup, err error) {
	logger.Debugf("Update backup %d, name: %s, path: %s, status: %s", id, name, path, status)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	backup, err = a.GetBackupByID(ctx, id)
	if err != nil {
		logger.Error("Failed to get backup", err)
		return
	}
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, backup.Owner)
	if !permit {
		logger.Error("Not authorized to update the backup")
		err = fmt.Errorf("Not authorized")
		return
	}
	if name != "" && name != backup.Name {
		backup.Name = name
	}
	if path != "" && path != backup.Path {
		backup.Path = path
	}
	if status != "" && status != backup.Status {
		backup.Status = status
	}
	if err = db.Model(backup).Updates(backup).Error; err != nil {
		logger.Error("DB: update backup failed", err)
		return
	}
	return
}

func (v *BackupView) List(c *macaron.Context, store session.Store) {
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
	total, backups, err := backupAdmin.List(c.Req.Context(), offset, limit, order, query, 0, "")
	if err != nil {
		logger.Error("Failed to list backup", err)
		c.Data["ErrorMsg"] = "Failed to list backup"
		c.HTML(http.StatusInternalServerError, "error")
		return
	}
	db := DB()
	where := memberShip.GetWhere()
	volumes := []*model.Volume{}
	err = db.Where(where).Find(&volumes).Error
	if err != nil {
		logger.Error("Failed to query volumes %v", err)
		return
	}

	c.Data["Volumes"] = volumes
	pages := GetPages(total, limit)
	c.Data["Backups"] = backups
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "vol_backups")
}

func (v *BackupView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	where := memberShip.GetWhere()
	volumes := []*model.Volume{}
	err := db.Where(where).Find(&volumes).Error
	if err != nil {
		logger.Error("Failed to query volumes %v", err)
		return
	}

	c.Data["Volumes"] = volumes
	c.HTML(200, "vol_backup_new")
}

func (v *BackupView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../backups"
	backupType := c.QueryTrim("backup_type")
	volumeID := c.QueryInt64("volume")
	name := c.QueryTrim("name")
	if name == "" {
		logger.Error("Backup name is empty")
		c.Data["ErrorMsg"] = "Backup name is empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	var err error
	logger.Debugf("Creating %s %s for volume %d", backupType, name, volumeID)
	if backupType == "snapshot" {
		_, err = backupAdmin.CreateSnapshotByID(c.Req.Context(), volumeID, name)
	} else if backupType == "backup" {
		_, err = backupAdmin.CreateBackupByID(c.Req.Context(), volumeID, "", name)
	} else {
		err = fmt.Errorf("Unknown backup type %s", backupType)
	}
	if err != nil {
		logger.Error("Failed to create backup %v", err)
		c.Data["ErrorMsg"] = "Failed to create backup"
		c.HTML(http.StatusInternalServerError, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *BackupView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.Error(http.StatusBadRequest)
		return
	}
	backupID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	backup, err := backupAdmin.GetBackupByID(ctx, int64(backupID))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = backupAdmin.Delete(ctx, backup)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "backups",
	})
	return
}

func (v *BackupView) Restore(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../volumes"
	backupID := c.QueryInt64("backup")
	err := backupAdmin.Restore(c.Req.Context(), backupID)
	if err != nil {
		logger.Error("Failed to restore volume %v", err)
		c.Data["ErrorMsg"] = "Failed to restore volume"
		c.HTML(http.StatusInternalServerError, "error")
		return
	}
	c.Redirect(redirectTo)
}
