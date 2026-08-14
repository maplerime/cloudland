/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"strings"
	"time"

	. "web/src/common"
	"web/src/model"

	"github.com/go-co-op/gocron"
	"github.com/spf13/viper"
)

const defaultExportTimeoutHours = 4

func RegisterExportWatchdog(s *gocron.Scheduler) {
	s.Every(30).Minutes().Do(cleanupStuckExports)
}

func cleanupStuckExports() {
	db := DB()
	timeout := viper.GetInt("export.timeout_hours")
	if timeout <= 0 {
		timeout = defaultExportTimeoutHours
	}
	cutoff := time.Now().Add(-time.Duration(timeout) * time.Hour)

	var stuck []model.Task
	if err := db.Where("action = ? AND status = ? AND updated_at < ?",
		model.TaskActionExport, model.TaskStatusRunning, cutoff).
		Find(&stuck).Error; err != nil {
		logger.Errorf("[ExportWatchdog] Failed to query stuck export tasks: %v", err)
		return
	}

	for i := range stuck {
		task := &stuck[i]
		logger.Warningf("[ExportWatchdog] Export task %s stuck since %v, marking failed", task.UUID, task.UpdatedAt)

		db.Model(task).Updates(map[string]interface{}{
			"status":  model.TaskStatusFailed,
			"message": "export timed out (watchdog cleanup)",
		})

		// Restore volume status for stuck volume exports
		if strings.HasPrefix(task.Resources, "volume:") {
			volumeUUID := strings.TrimPrefix(task.Resources, "volume:")
			vol := &model.Volume{}
			if db.Where("uuid = ?", volumeUUID).Take(vol).Error == nil {
				volStatus := model.VolumeStatusAvailable
				if vol.InstanceID > 0 {
					volStatus = model.VolumeStatusAttached
				}
				db.Model(vol).Update("status", volStatus)
			}
		}
	}
}
