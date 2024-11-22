/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpcs

import (
	"context"
	"fmt"
	"log"
	"strconv"

	. "web/src/common"
	"web/src/model"
)

func init() {
	Add("create_volume", CreateVolume)
}

func CreateVolume(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| create_volume.sh 5 /volume-12.disk available
	db := DB()
	argn := len(args)
	if argn < 4 {
		err = fmt.Errorf("Wrong params")
		log.Println("Invalid args", err)
		return
	}
	volID, err := strconv.Atoi(args[1])
	if err != nil {
		log.Println("Invalid volume ID", err)
		return
	}
	volume := &model.Volume{Model: model.Model{ID: int64(volID)}}
	err = db.Where(volume).Take(volume).Error
	if err != nil {
		log.Println("Invalid instance ID", err)
		return
	}
	path := args[2]
	status = args[3]
	err = db.Model(&volume).Updates(map[string]interface{}{"path": path, "status": status}).Error
	if err != nil {
		log.Println("Update volume status failed", err)
		return
	}
	return
}
