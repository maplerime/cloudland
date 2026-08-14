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
	Add("export_wds_vhost", ExportWDSVhost)
}

// ExportWDSVhost handles the callback from export_wds_vhost.sh.
// Command format:
//
//	|:-COMMAND-:| export_wds_vhost.sh '$task_ID' '$resource_type' '$state' '$path' '$msg'
func ExportWDSVhost(ctx context.Context, args []string) (status string, err error) {
	logger.Debug("ExportWDSVhost", args)
	if len(args) < 6 {
		err = fmt.Errorf("wrong params")
		logger.Error("Invalid args for export_wds_vhost", args)
		return
	}
	taskID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid task ID: %v", args[1])
		return
	}
	resourceType := args[2] // "volume" or "image"
	state := args[3]
	path := args[4]
	msg := args[5]

	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	task := &model.Task{}
	if err = db.Where("id = ?", taskID).Take(task).Error; err != nil {
		logger.Errorf("Failed to find task %d: %v", taskID, err)
		return
	}

	taskStatus := model.TaskStatusSuccess
	taskMsg := msg
	if state != "done" {
		taskStatus = model.TaskStatusFailed
	} else if path != "" {
		taskMsg = fmt.Sprintf("exported to: %s", path)
	}
	if err = db.Model(task).Updates(map[string]interface{}{"status": taskStatus, "message": taskMsg}).Error; err != nil {
		logger.Errorf("Failed to update task %d: %v", taskID, err)
		return
	}

	// Restore volume status after export (success or failure)
	if resourceType == "volume" && strings.HasPrefix(task.Resources, "volume:") {
		volumeUUID := strings.TrimPrefix(task.Resources, "volume:")
		vol := &model.Volume{}
		if dbErr := db.Where("uuid = ?", volumeUUID).Take(vol).Error; dbErr != nil {
			logger.Errorf("Failed to find volume %s for status restore: %v", volumeUUID, dbErr)
			return
		}
		volStatus := model.VolumeStatusAvailable
		if vol.InstanceID > 0 {
			volStatus = model.VolumeStatusAttached
		}
		if dbErr := db.Model(vol).Updates(map[string]interface{}{"status": volStatus}).Error; dbErr != nil {
			logger.Errorf("Failed to restore volume %s status: %v", volumeUUID, dbErr)
		}
	}
	return
}
