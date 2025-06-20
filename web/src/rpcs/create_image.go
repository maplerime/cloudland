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
	//|:-COMMAND-:| create_image.sh '5' 'available' 'qcow2' '1024000' 'pool_ID' 'volume_ID'
	db := DB()
	argn := len(args)
	if argn < 6 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	imgID, err := strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid gateway ID", err)
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

	poolID := args[5]
	volumeID := args[6]
	storage := &model.ImageStorage{
		ImageID: image.ID,
		PoolID:  poolID,
	}
	err = db.Take(storage).Error
	if err != nil {
		logger.Error("Invalid storage", err)
		return
	}
	storage.VolumeID = volumeID
	storage.Status = "synced"
	err = db.Save(storage).Error
	if err != nil {
		logger.Error("Update image storage failed", err)
		return
	}

	var storages []*model.ImageStorage
	err = db.Where("status = ? AND image_id = ?", "syncing", image.ID).Find(&storages).Error
	if err == nil {
		// run copy clone task from snap
		cloneFrom := "snap"
		prefix := strings.Split(image.UUID, "-")[0]
		control := "inter="
		for _, item := range storages {
			command := fmt.Sprintf("/opt/cloudland/scripts/backend/clone_image.sh '%d' '%d' '%s' '%s' '%s' '%s' '%s'", item.ID, image.ID, prefix, poolID, volumeID, item.PoolID, cloneFrom)
			err = HyperExecute(ctx, control, command)
			if err != nil {
				logger.Error("Command execution failed", err)
			}
		}
	}

	return
}
