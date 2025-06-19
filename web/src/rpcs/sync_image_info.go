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
	//|:-COMMAND-:| sync_image_info.sh '5' 'wds_volume_id' 'wds_pool_id'
	db := DB()
	argn := len(args)
	if argn < 3 {
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
	volumeID := args[2]
	poolID := args[3]
	imageStorage := &model.ImageStorage{
		ImageID:  image.ID,
		VolumeID: volumeID,
		PoolID:   poolID,
	}
	err = db.Where(imageStorage).First(&imageStorage).Error
	if err == nil {
		logger.Debugf("Image storage already exists: %+v", imageStorage)
		return
	}
	err = db.Create(imageStorage).Error
	if err != nil {
		logger.Error("Update image failed", err)
		return
	}
	return
}
