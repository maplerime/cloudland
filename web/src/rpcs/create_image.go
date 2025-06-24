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
	state := args[2]
	image.Status = state
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
	storageID := 0
	storageID, err = strconv.Atoi(args[6])
	if err != nil {
		logger.Error("Invalid storage ID", err)
		return
	}
	if storageID > 0 {
		storage := &model.ImageStorage{Model: model.Model{ID: int64(storageID)}}
		err = db.Take(storage).Error
		if err != nil {
			logger.Error("Invalid storage ID", err)
			return
		}
		storage.VolumeID = args[5]
		if state == "available" {
			storage.Status = model.StorageStatusSynced
		} else {
			storage.Status = model.StorageStatusError
		}
		err = db.Save(storage).Error
		if err != nil {
			logger.Error("Update image storage failed", err)
			return
		}
	}
	return
}
