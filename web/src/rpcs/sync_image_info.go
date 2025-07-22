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
	//|:-COMMAND-:| sync_image_info.sh 'storage_id' 'volume_id' 'error'
	db := DB()
	argn := len(args)
	if argn < 3 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	storageID, err := strconv.Atoi(args[1])
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
	if err = db.Model(storage).Updates(map[string]interface{}{
		"volume_id": args[2],
		"status":    model.StorageStatus(args[3]),
	}).Error; err != nil {
		logger.Error("Update image storage failed", err)
		return
	}

	return
}
