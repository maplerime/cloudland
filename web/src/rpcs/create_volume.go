/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"fmt"
	"strconv"
	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("create_volume_local", CreateVolumeLocal)
	Add("create_volume_wds_vhost", CreateVolumeWDSVhost)
}

func updateInstance(ctx context.Context, volume *model.Volume, status string, reason string) (err error) {
	ctx, db := GetContextDB(ctx)
	if volume.Booting && status == "error" {
		instance := &model.Instance{Model: model.Model{ID: volume.InstanceID}}
		if err = db.Take(&instance).Error; err != nil {
			logger.Error("Invalid instance ID", err)
			return err
		}

		instance.Status = model.InstanceStatus(status)
		instance.Reason = reason
		err = db.Model(&model.Instance{}).Where("id = ?", instance.ID).Updates(map[string]interface{}{
			"status": instance.Status,
			"reason": instance.Reason,
		}).Error
		if err != nil {
			logger.Error("Update instance status failed", err)
			return err
		}
	}
	return
}

func CreateVolumeLocal(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| create_volume.sh 5 /volume-12.disk available reason
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	logger.Debug("CreateVolumeLocal", args)
	argn := len(args)
	if argn < 5 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	volID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	volume := &model.Volume{Model: model.Model{ID: volID}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	path := args[2]
	status = args[3]
	err = db.Model(&volume).Updates(map[string]interface{}{"path": path, "status": status}).Error
	if err != nil {
		logger.Error("Update volume status failed", err)
		return
	}
	if err = updateInstance(ctx, volume, status, args[4]); err != nil {
		logger.Error("Update instance status failed", err)
		return
	}
	return
}

func CreateVolumeWDSVhost(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| create_volume_wds_vhost.sh 5 available wds_vhost://1/2 reason
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	logger.Debug("CreateVolumeWDSVhost", args)
	argn := len(args)
	if argn < 5 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	volID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	volume := &model.Volume{Model: model.Model{ID: volID}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	status = args[2]
	path := args[3]
	err = db.Model(&volume).Updates(map[string]interface{}{"path": path, "status": status}).Error
	if err != nil {
		logger.Error("Update volume status failed", err)
		return
	}
	poolID := volume.GetVolumePoolID()
	if poolID != "" {
		err = db.Model(&volume).Updates(map[string]interface{}{"pool_id": poolID}).Error
		if err != nil {
			logger.Error("Update volume pool ID failed", err)
			return
		}
	}
	if err = updateInstance(ctx, volume, status, args[4]); err != nil {
		logger.Error("Update instance status failed", err)
		return
	}
	return
}
