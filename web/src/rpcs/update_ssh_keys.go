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
	Add("update_ssh_keys", UpdateSSHKeys)
}

func UpdateSSHKeys(ctx context.Context, args []string) (status string, err error) {
	//|:-COMMAND-:| update_ssh_keys.sh '<task_ID>' '<success|failed>' '<message>'
	logger.Debug("UpdateSSHKeys", args)
	if len(args) < 4 {
		logger.Errorf("Invalid args for update_ssh_keys: %v", args)
		err = fmt.Errorf("wrong params")
		return
	}
	taskID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		logger.Errorf("Invalid task ID: %v", args[1])
		return
	}
	status = args[2]
	updates := map[string]interface{}{"status": model.TaskStatusSuccess}
	if status != "success" {
		updates = map[string]interface{}{"status": model.TaskStatusFailed, "message": args[3]}
	}
	err = DB().Model(&model.Task{Model: model.Model{ID: taskID}}).Updates(updates).Error
	if err != nil {
		logger.Errorf("Failed to update task %d: %v", taskID, err)
		return
	}
	return
}
