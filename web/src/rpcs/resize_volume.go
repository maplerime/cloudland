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
	Add("resize_volume", ResizeVolume)
}

func ResizeVolume(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| resize_volume.sh 5 error
	logger.Debug("ResizeVolumeLocal", args)
	db := DB()
	argn := len(args)
	if argn < 3 {
		err = fmt.Errorf("Wrong params")
		logger.Error("Invalid args", err)
		return
	}
	volID, err := strconv.Atoi(args[1])
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	volume := &model.Volume{Model: model.Model{ID: int64(volID)}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		logger.Error("Invalid volume ID", err)
		return
	}
	status = args[2]
	if status != "error" {
		if volume.InstanceID != 0 {
			status = model.VolumeStatusAttached.String()
		} else {
			status = model.VolumeStatusAvailable.String()
		}
	}
	err = db.Model(&volume).Updates(map[string]interface{}{"status": status}).Error
	if err != nil {
		logger.Error("Update volume status failed", err)
		return
	}
	return
}
