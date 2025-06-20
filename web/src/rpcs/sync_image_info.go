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
	Add("sync_image_info", SyncImageInfo)
}

func SyncImageInfo(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| sync_image_info.sh 'image_id' 'pool_id' 'volume_id' 'error'
	db := DB()
	argn := len(args)
	if argn < 4 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	imgID, err := strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid image ID", err)
		return
	}
	image := &model.Image{Model: model.Model{ID: int64(imgID)}}
	err = db.Take(image).Error
	if err != nil {
		logger.Error("Invalid image ID", err)
		return
	}
	poolID := args[2]
	volumeID := args[3]
	state := args[4]
	if state == "error" {
		db.Where("image_id = ? AND pool_id = ?", image.ID, poolID).Delete(model.ImageStorage{})
		return
	}
	storage := &model.ImageStorage{
		ImageID: image.ID,
		PoolID:  poolID,
	}
	err = db.Take(storage).Error
	storage.VolumeID = volumeID
	storage.Status = state
	if err != nil {
		if err = db.Create(storage).Error; err != nil {
			logger.Error("Create new image storage failed", err)
			return
		}
	} else {
		if err = db.Save(storage).Error; err != nil {
			logger.Error("Update image storage failed", err)
			return
		}
	}
	return
}
