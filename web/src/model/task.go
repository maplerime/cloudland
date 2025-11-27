/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

type TaskSource string

const (
	TaskSourceManual    TaskSource = "manual"
	TaskSourceScheduler TaskSource = "scheduler"
	TaskSourceMigration TaskSource = "migration"
)

type TaskStatus string

const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusRunning TaskStatus = "running"
	TaskStatusSuccess TaskStatus = "success"
	TaskStatusFailed  TaskStatus = "failed"
)

type TaskAction string

const (
	TaskActionStop        TaskAction = "stop"
	TaskActionHardStop    TaskAction = "hard_stop"
	TaskActionStart       TaskAction = "start"
	TaskActionRestart     TaskAction = "restart"
	TaskActionHardRestart TaskAction = "hard_restart"
	TaskActionPause       TaskAction = "pause"
	TaskActionResume      TaskAction = "resume"

	TaskActionMigrate  TaskAction = "migrate"
	TaskActionBackup   TaskAction = "backup"
	TaskActionSnapshot TaskAction = "snapshot"
	TaskActionRestore  TaskAction = "restore"
)

type Task struct {
	Model
	Mission   int64
	Owner     int64      `gorm:"default:1;index"` /* The organization ID of the resource */
	Source    TaskSource `gorm:"type:varchar(32);default:'migration';index"`
	Name      string     `gorm:"type:varchar(64);index"`
	Summary   string     `gorm:"type:varchar(128)"`
	Status    TaskStatus `gorm:"type:varchar(32)"`
	Message   string     `gorm:"type:varchar(512)"`
	Cron      string     `gorm:"type:varchar(64)"`
	Action    TaskAction `gorm:"type:varchar(32)"`
	Resources string     `gorm:"type:text"` // JSON string array
}

func init() {
	dbs.AutoMigrate(&Task{})
}
