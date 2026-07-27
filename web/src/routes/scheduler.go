/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package routes

import (
	"context"
	"fmt"
	"sync"
	"time"

	. "web/src/common"
	"web/src/model"

	"github.com/go-co-op/gocron"
	"github.com/jinzhu/gorm"
	"github.com/robfig/cron/v3"
)

// RunScheduler starts the background scheduler service for automated task execution.
// It runs a periodic checker every minute to find and execute enabled scheduled tasks.
// Returns an error if the scheduler cannot be started.
func RunScheduler() (err error) {
	logger.Info("[Scheduler] Starting scheduler service - function entry")

	// Create a new scheduler instance with UTC timezone
	s := gocron.NewScheduler(time.UTC)

	// Schedule the task checker to run every minute
	s.Every(1).Minute().Do(func() {
		logger.Info("[Scheduler] Running scheduled tasks checker")
		checkAndRunTasks()
	})

	// Register export watchdog: cleans up stuck exports every 30 minutes
	RegisterExportWatchdog(s)

	// Start the scheduler (blocking — keeps the goroutine alive)
	s.StartBlocking()
	return
}

// cronScheduleCache caches parsed cron schedules keyed by expression.
var cronScheduleCache = sync.Map{} // map[string]cron.Schedule

// shouldRun checks whether a scheduled task is due for execution based on its
// schedule_type, execution_time (one-time), and cron_expression (recurring).
// This function is pure — no DB side effects.
func shouldRun(task *model.ScheduledTask) bool {
	now := time.Now().UTC()

	switch task.ScheduleType {
	case "one-time":
		if task.ExecutionTime.IsZero() {
			return false
		}
		return !now.Before(task.ExecutionTime.UTC())

	case "daily", "weekly", "monthly":
		expr := task.CronExpression
		if expr == "" {
			return false
		}
		if v, ok := cronScheduleCache.Load(expr); ok {
			return v.(cron.Schedule).Next(now.Add(-1 * time.Minute)).Before(now)
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(expr)
		if err != nil {
			logger.Warningf("[Scheduler] Invalid cron expression for task %d: %q: %v", task.ID, expr, err)
			return false
		}
		cronScheduleCache.Store(expr, sched)
		return sched.Next(now.Add(-1 * time.Minute)).Before(now)

	default:
		logger.Warningf("[Scheduler] Unknown schedule type for task %d: %q", task.ID, task.ScheduleType)
		return false
	}
}

// checkAndRunTasks retrieves all enabled scheduled tasks and executes them if due.
// Uses locking mechanism to prevent concurrent execution of the same task.
func checkAndRunTasks() {
	logger.Debug("[Scheduler] Checking for enabled tasks - function entry")

	taskAdmin := &ScheduledTaskAdmin{}
	tasks, err := taskAdmin.ListEnabledTasks(context.Background())
	if err != nil {
		logger.Errorf("[Scheduler] Failed to list enabled tasks: %v", err)
		return
	}

	for _, task := range tasks {
		if !shouldRun(task) {
			continue
		}

		logger.Debugf("[Scheduler] Processing task %d: %s", task.ID, task.Name)

		lock, err := tryLock(task.ID)
		if err != nil {
			logger.Errorf("[Scheduler] Failed to acquire lock for task %d: %v", task.ID, err)
			continue
		}
		if !lock {
			logger.Infof("[Scheduler] Task %d is already running, skipping", task.ID)
			continue
		}

		logger.Infof("[Scheduler] Starting execution of task %d: %s", task.ID, task.Name)
		go runTask(task)
	}

	logger.Debug("[Scheduler] Task checking completed - function exit")
}

const taskTimeout = 30 * time.Minute

// runTask executes a single scheduled task and records its execution history.
// Handles different task types (instance operations, volume backups) and manages cleanup.
func runTask(task *model.ScheduledTask) {
	defer unlock(task.ID)

	logger.Infof("[Scheduler] Running task %d: %s - function entry", task.ID, task.Name)
	adminCtx, err := CreateAdminContext(context.Background())
	if err != nil {
		recordTaskHistory(task.ID, "failed", fmt.Sprintf("failed to create admin context: %v", err), time.Now(), 0)
		logger.Errorf("[Scheduler] Failed to create admin context for task %d: %v", task.ID, err)
		return
	}
	ctx, cancel := context.WithTimeout(adminCtx, taskTimeout)
	defer cancel()
	startTime := time.Now()
	var status string
	var message string

	// Ensure task history is recorded and one-time tasks are disabled
	defer func() {
		duration := time.Since(startTime).Seconds()
		recordTaskHistory(task.ID, status, message, time.Now(), int64(duration))
		logger.Infof("[Scheduler] Finished running task %d: %s (duration: %.2fs) - function exit", task.ID, task.Name, duration)
	}()

	// Disable one-time tasks after execution
	if task.ScheduleType == "one-time" {
		defer func() {
			db := DB()
			if err := db.Model(task).Update("status", "disabled").Error; err != nil {
				logger.Warningf("[Scheduler] Failed to disable one-time task %d: %v", task.ID, err)
			}
		}()
	}

	// Execute the appropriate task type
	logger.Debugf("[Scheduler] Executing task type: %s", task.TaskType)
	switch task.TaskType {
	case "instance_op":
		logger.Debug("[Scheduler] Processing instance operation")
		err = runInstanceOperation(ctx, task)
	case "volume_backup":
		logger.Debug("[Scheduler] Processing volume backup operation")
		err = runVolumeBackup(ctx, task)
	default:
		err = fmt.Errorf("unknown task type: %s", task.TaskType)
		logger.Errorf("[Scheduler] Unknown task type encountered: %s", task.TaskType)
	}

	// Determine execution status and message
	if err != nil {
		status = "failed"
		message = err.Error()
		logger.Errorf("[Scheduler] Task %d failed with error: %v", task.ID, err)
	} else {
		status = "success"
		message = "Task executed successfully"
		logger.Infof("[Scheduler] Task %d completed successfully", task.ID)
	}
}

// runInstanceOperation performs lifecycle operations on virtual machine instances.
// Supports start, stop, hard_stop, restart, and hard_restart operations.
func runInstanceOperation(ctx context.Context, task *model.ScheduledTask) error {
	logger.Debugf("[Scheduler] Running instance operation for task %d - function entry", task.ID)

	// Get the target instance
	instanceAdmin := &InstanceAdmin{}
	instance, err := instanceAdmin.Get(ctx, task.ResourceID)
	if err != nil {
		logger.Errorf("[Scheduler] Failed to retrieve instance %d for task %d: %v", task.ResourceID, task.ID, err)
		return fmt.Errorf("failed to get instance %d for task %d: %v", task.ResourceID, task.ID, err)
	}
	if instance.Owner != task.Owner {
		return fmt.Errorf("instance %d does not belong to task owner %d", instance.ID, task.Owner)
	}

	logger.Infof("[Scheduler] Performing operation '%s' on instance %d (hostname: %s)", task.Operation, task.ResourceID, instance.Hostname)

	// Execute the requested operation
	switch task.Operation {
	case "start":
		logger.Debug("[Scheduler] Executing start operation")
		err = instanceAdmin.Update(ctx, instance, instance.Hostname, Start, int(instance.Hyper))
	case "stop":
		logger.Debug("[Scheduler] Executing stop operation")
		err = instanceAdmin.Update(ctx, instance, instance.Hostname, Stop, int(instance.Hyper))
	case "hard_stop":
		logger.Debug("[Scheduler] Executing hard_stop operation")
		err = instanceAdmin.Update(ctx, instance, instance.Hostname, HardStop, int(instance.Hyper))
	case "restart":
		logger.Debug("[Scheduler] Executing restart operation")
		err = instanceAdmin.Update(ctx, instance, instance.Hostname, Restart, int(instance.Hyper))
	case "hard_restart":
		logger.Debug("[Scheduler] Executing hard_restart operation")
		err = instanceAdmin.Update(ctx, instance, instance.Hostname, HardRestart, int(instance.Hyper))
	default:
		logger.Errorf("[Scheduler] Unknown instance operation: %s", task.Operation)
		return fmt.Errorf("unknown instance operation: %s", task.Operation)
	}

	if err != nil {
		logger.Errorf("[Scheduler] Instance operation '%s' failed: %v", task.Operation, err)
		return fmt.Errorf("instance operation '%s' failed: %v", task.Operation, err)
	}

	logger.Debugf("[Scheduler] Instance operation for task %d completed successfully - function exit", task.ID)
	return nil
}

// runVolumeBackup performs backup or snapshot operations on storage volumes.
// Supports snapshot and backup operations with automatic retention management.
func runVolumeBackup(ctx context.Context, task *model.ScheduledTask) error {
	logger.Debugf("[Scheduler] Running volume backup for task %d - function entry", task.ID)

	// Get the backup admin for volume operations
	backupAdmin := &BackupAdmin{}
	var err error

	logger.Infof("[Scheduler] Performing operation '%s' on volume %d", task.Operation, task.ResourceID)

	volume, err := volumeAdmin.Get(ctx, task.ResourceID)
	if err != nil {
		return fmt.Errorf("failed to get volume %d for task %d: %v", task.ResourceID, task.ID, err)
	}
	if volume.Owner != task.Owner {
		return fmt.Errorf("volume %d does not belong to task owner %d", volume.ID, task.Owner)
	}

	// Generate a timestamp-based name for the backup/snapshot
	timestamp := time.Now().Format("20060102150405")
	backupName := fmt.Sprintf("%s-%s", task.Name, timestamp)

	// Execute the requested backup operation
	if task.Operation == "snapshot" {
		logger.Debug("[Scheduler] Creating volume snapshot")
		_, err = backupAdmin.CreateSnapshotByIDForScheduler(ctx, volume.ID, backupName)
	} else if task.Operation == "backup" {
		logger.Debug("[Scheduler] Creating volume backup")
		_, err = backupAdmin.CreateBackupByIDForScheduler(ctx, volume.ID, volume.GetVolumePoolID(), backupName)
	} else {
		logger.Errorf("[Scheduler] Unknown volume backup operation: %s", task.Operation)
		return fmt.Errorf("unknown volume backup operation: %s", task.Operation)
	}

	if err != nil {
		logger.Errorf("[Scheduler] Volume backup operation '%s' failed: %v", task.Operation, err)
		return fmt.Errorf("volume backup operation '%s' failed: %v", task.Operation, err)
	}

	logger.Debugf("[Scheduler] Volume backup for task %d completed, handling retention policy", task.ID)

	// Handle retention policy for old backups/snapshots
	handleRetention(ctx, task)

	logger.Debugf("[Scheduler] Volume backup and retention for task %d finished - function exit", task.ID)
	return nil
}

// handleRetention manages backup/snapshot retention by deleting old records.
// Implements the retention policy defined in the scheduled task configuration.
func handleRetention(ctx context.Context, task *model.ScheduledTask) {
	// Skip retention if no limit is set
	if task.RetentionCount <= 0 {
		logger.Debug("[Scheduler] No retention policy set, skipping cleanup")
		return
	}

	logger.Debugf("[Scheduler] Handling retention for task %d - function entry (retention count: %d)", task.ID, task.RetentionCount)

	// Get all backups/snapshots for this volume and operation type
	backupAdmin := &BackupAdmin{}
	total, backups, err := backupAdmin.List(ctx, 0, -1, "created_at", "", task.ResourceID, string(task.Operation))
	if err != nil {
		logger.Errorf("[Scheduler] Failed to list backups for retention cleanup (task %d): %v", task.ID, err)
		return
	}

	logger.Debugf("[Scheduler] Found %d total backups for volume %d", total, task.ResourceID)

	// Check if we need to delete old backups
	if total > int64(task.RetentionCount) {
		excessCount := total - int64(task.RetentionCount)
		logger.Infof("[Scheduler] Need to delete %d old backups to maintain retention policy", excessCount)

		// Delete oldest backups (the list is sorted by created_at)
		for i := 0; i < int(excessCount); i++ {
			logger.Infof("[Scheduler] Deleting backup %d for task %d due to retention policy", backups[i].ID, task.ID)
			err = backupAdmin.Delete(ctx, backups[i])
			if err != nil {
				logger.Errorf("[Scheduler] Failed to delete backup %d for task %d: %v", backups[i].ID, task.ID, err)
			} else {
				logger.Debugf("[Scheduler] Successfully deleted backup %d", backups[i].ID)
			}
		}
	} else {
		logger.Debugf("[Scheduler] No backups to delete for task %d (current: %d, limit: %d)", task.ID, total, task.RetentionCount)
	}

	logger.Debugf("[Scheduler] Retention handling completed for task %d - function exit", task.ID)
}

const lockTTL = taskTimeout + 5*time.Minute // stale lock threshold

type legacyLock struct {
	ID        int64
	CreatedAt time.Time
	DeletedAt *time.Time
	Name      string
}

// tryLock attempts to acquire an exclusive lock for a scheduled task.
// Uses database records to implement distributed locking mechanism.
// Stale locks (> lockTTL) are automatically cleaned up to prevent permanent blocking
// after a process crash.
// Returns true if lock was acquired, false if task is already locked.
func tryLock(taskID int64) (bool, error) {
	logger.Debugf("[Scheduler] Attempting to acquire lock for task %d", taskID)

	db := DB()
	lockName := fmt.Sprintf("scheduled_task_%d", taskID)

	// Check for existing lock, including legacy soft-deleted rows that would
	// still block the unique index on name.
	var existing legacyLock
	err := db.Unscoped().Table("locks").Where("name = ?", lockName).First(&existing).Error
	if err == nil {
		if existing.DeletedAt != nil {
			logger.Warningf("[Scheduler] Cleaning up soft-deleted lock residue for task %d", taskID)
			if delErr := db.Unscoped().Table("locks").Where("id = ?", existing.ID).Delete(&model.Lock{}).Error; delErr != nil {
				return false, delErr
			}
		} else if time.Since(existing.CreatedAt) > lockTTL {
			logger.Warningf("[Scheduler] Cleaning up stale lock for task %d (age: %s)", taskID, time.Since(existing.CreatedAt))
			if delErr := db.Unscoped().Table("locks").Where("id = ?", existing.ID).Delete(&model.Lock{}).Error; delErr != nil {
				return false, delErr
			}
		} else {
			logger.Debugf("[Scheduler] Task %d is already locked (age: %s), skipping", taskID, time.Since(existing.CreatedAt))
			return false, nil
		}
	} else if err != gorm.ErrRecordNotFound {
		logger.Errorf("[Scheduler] Failed to check lock for task %d: %v", taskID, err)
		return false, err
	}

	// Create the lock record
	lock := &model.Lock{Name: lockName}
	err = db.Create(lock).Error
	if err != nil {
		logger.Debugf("[Scheduler] Failed to acquire lock for task %d (race condition)", taskID)
		return false, nil
	}

	logger.Debugf("[Scheduler] Successfully acquired lock for task %d", taskID)
	return true, nil
}

// unlock releases the lock for a scheduled task.
// Allows other scheduler instances to execute the same task.
func unlock(taskID int64) {
	logger.Debugf("[Scheduler] Releasing lock for task %d", taskID)

	// Remove the lock record from the database
	db := DB()
	lockName := fmt.Sprintf("scheduled_task_%d", taskID)
	lock := &model.Lock{Name: lockName}

	// Delete the lock record and release the unique index on name.
	result := db.Where("name = ?", lockName).Delete(lock)
	if result.Error != nil {
		logger.Errorf("[Scheduler] Failed to release lock for task %d: %v", taskID, result.Error)
	} else {
		logger.Debugf("[Scheduler] Successfully released lock for task %d", taskID)
	}
}

// recordTaskHistory saves the execution result of a scheduled task to the database.
// Creates a historical record for monitoring and auditing purposes.
func recordTaskHistory(taskID int64, status, message string, executionTime time.Time, duration int64) {
	logger.Debugf("[Scheduler] Recording task history for task %d: status=%s, duration=%ds", taskID, status, duration)

	// Create the history record
	db := DB()
	history := &model.ScheduledTaskHistory{
		ScheduledTaskID: taskID,
		Status:          status,
		Message:         message,
		ExecutionTime:   executionTime,
		Duration:        duration,
	}

	// Save to database
	err := db.Create(history).Error
	if err != nil {
		logger.Errorf("[Scheduler] Failed to record task history for task %d: %v", taskID, err)
	} else {
		logger.Debugf("[Scheduler] Successfully recorded task history for task %d", taskID)
	}
}
