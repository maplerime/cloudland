package model

import (
	"time"
	"web/src/dbs"
)

// STaskAction represents the type of operation a scheduled task can perform
type STaskAction string

// Constants for scheduled task actions
const (
	// Instance operations
	STaskActionStop        STaskAction = "stop"         // Stop instance gracefully
	STaskActionHardStop    STaskAction = "hard_stop"    // Force stop instance
	STaskActionStart       STaskAction = "start"        // Start instance
	STaskActionRestart     STaskAction = "restart"      // Restart instance gracefully
	STaskActionHardRestart STaskAction = "hard_restart" // Force restart instance
	// Volume operations
	STaskActionSnapshot    STaskAction = "snapshot"     // Create volume snapshot
	STaskActionBackup      STaskAction = "backup"       // Create volume backup
)

// ScheduledTask represents a scheduled task for automating operations on resources
// such as instances and volumes. It supports various schedule types and operations.
type ScheduledTask struct {
	Model
	Owner          int64       `gorm:"default:1;index"`                // Organization ID that owns this task
	Name           string      `gorm:"type:varchar(128)"`               // Human-readable name for the task
	TaskType       string      `gorm:"type:varchar(32)"`                // Type of task: instance_op, volume_backup
	ResourceType   string      `gorm:"type:varchar(32)"`                // Type of resource: instance, volume
	ResourceID     int64                                                // ID of the target resource
	Operation      STaskAction `gorm:"type:varchar(32)"`                // Operation to perform
	ScheduleType   string      `gorm:"type:varchar(32)"`                // Schedule type: one-time, daily, weekly, monthly
	ExecutionTime  time.Time                                            // Next execution time for one-time tasks
	CronExpression string      `gorm:"type:varchar(128)"`               // Cron expression for recurring tasks
	RetentionCount int                                                  // Number of backups/snapshots to retain (0 = unlimited)
	Status         string      `gorm:"type:varchar(32)"`                // Task status: enabled, disabled
}

// ScheduledTaskHistory records the execution history of scheduled tasks
// including execution status, duration, and any error messages.
type ScheduledTaskHistory struct {
	Model
	ScheduledTaskID int64          // ID of the associated scheduled task
	ScheduledTask   *ScheduledTask `gorm:"foreignkey:ScheduledTaskID"` // Reference to the scheduled task
	Status          string         `gorm:"type:varchar(32)"`           // Execution status: success, failed
	Message         string         `gorm:"type:text"`                  // Success message or error details
	ExecutionTime   time.Time                                          // When the task was executed
	Duration        int64                                              // Execution duration in seconds
}

func init() {
	dbs.AutoMigrate(&ScheduledTask{}, &ScheduledTaskHistory{})
}
