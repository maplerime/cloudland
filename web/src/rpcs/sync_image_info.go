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
	Add("sync_image_info", SyncImageInfo)
}

// SyncImageInfo
// If one record is synchronized each time
// it is impossible to detect whether it has been deleted from a certain pool
// which may easily lead to orphan processes
// Therefore, all records are returned at once here
func SyncImageInfo(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| sync_image_info.sh '5' 'pool_id_1,volume_id_1;pool_id_2,volume_id_2'
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

	pairStr := args[2] // e.g. "id1,pool1;id2,pool2"
	mapping := make(map[string]string)
	for _, pair := range strings.Split(pairStr, ";") {
		parts := strings.Split(pair, ",")
		if len(parts) == 2 {
			mapping[parts[0]] = parts[1]
		}
	}

	// get all storages
	var localStorages []model.ImageStorage
	if err = db.Where("image_id = ?", image.ID).Find(&localStorages).Error; err != nil {
		return
	}

	for _, local := range localStorages {
		newVolumeID, found := mapping[local.PoolID]
		if found {
			// already exists in remote, update it
			local.VolumeID = newVolumeID
			local.Status = "synced"
			if err = db.Save(&local).Error; err != nil {
				logger.Error("Update image storage failed", err)
				return
			}
			delete(mapping, local.PoolID)
		} else {
			// does not exist in remote, delete it
			if err = db.Delete(&local).Error; err != nil {
				logger.Error("Delete image storage failed", err)
				return
			}
		}
	}

	// create new records for remaining mappings
	for poolID, volumeID := range mapping {
		newRecord := model.ImageStorage{
			ImageID:  image.ID,
			PoolID:   poolID,
			VolumeID: volumeID,
			Status:   "synced",
		}
		if err = db.Create(&newRecord).Error; err != nil {
			logger.Error("Create new image storage failed", err)
			return
		}
	}
	return
}
