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
	Add("detach_volume_local", DetachVolume)
	Add("detach_volume_wds_vhost", DetachVolume)
}

func DetachVolume(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| detach_volume.sh_local 5 7
	//|:-COMMAND-:| detach_volume.sh_wds_vhost 5 7
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	argn := len(args)
	if argn < 4 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	_, err = strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid instance ID", err)
		return
	}
	volID, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	volume := &model.Volume{Model: model.Model{ID: volID}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		logger.Error("Failed to query volume", err)
		return
	}
	//volume.InstanceID = 0
	//volume.Target = ""
	//volume.Status = model.VolumeStatusAvailable
	//err = db.Save(volume).Error

	volume.Status = model.VolumeStatus(args[3])
	if volume.Status == model.VolumeStatusAvailable {
		volume.InstanceID = 0
		volume.Target = ""
	}
	err = db.Model(&model.Volume{}).Where("id = ?", volume.ID).Updates(map[string]interface{}{
		"instance_id": volume.InstanceID,
		"target":      volume.Target,
		"status":      volume.Status,
	}).Error
	if err != nil {
		logger.Error("Update volume status failed", err)
		return
	}
	return
}
