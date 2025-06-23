/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

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
	Add("create_image", CreateImage)
}

func CreateImage(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| create_image.sh '5' 'available' 'qcow2' '1024000' 'volume_ID' 'storage_ID'
	db := DB()
	argn := len(args)
	if argn < 5 {
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
	image.Status = args[2]
	image.Format = args[3]
	imageSize, err := strconv.Atoi(args[4])
	if err != nil {
		logger.Error("Invalid image size", err)
		return
	}
	image.Size = int64(imageSize)
	err = db.Save(image).Error
	if err != nil {
		logger.Error("Update image failed", err)
		return
	}
	if args[6] != "0" {
		storageID := 0
		storageID, err = strconv.Atoi(args[6])
		if err != nil {
			logger.Error("Invalid storage ID", err)
			return
		}
		storage := &model.ImageStorage{Model: model.Model{ID: int64(storageID)}}
		err = db.Take(storage).Error
		if err != nil {
			logger.Error("Invalid storage ID", err)
			return
		}
		storage.ImageID = image.ID
		storage.VolumeID = args[5]
		storage.Status = model.StorageStatus(args[2])
		err = db.Save(storage).Error
		if err != nil {
			logger.Error("Update image storage failed", err)
			return
		}

		// clone
		var storages []*model.ImageStorage
		err = db.Where("status = ? AND image_id = ?", model.StorageStatusSyncing, image.ID).Find(&storages).Error
		if err == nil && storage.VolumeID != "" {
			cloneFrom := "snap"
			prefix := strings.Split(image.UUID, "-")[0]
			control := "inter=0"
			for _, item := range storages {
				command := fmt.Sprintf("/opt/cloudland/scripts/backend/clone_image.sh '%d' '%s' '%s' '%s' '%s' '%d'", image.ID, prefix, storage.VolumeID, item.PoolID, cloneFrom, item.ID)
				err = HyperExecute(ctx, control, command)
				if err != nil {
					logger.Error("Command execution failed", err)
				}
			}
		}
	}
	return
}
