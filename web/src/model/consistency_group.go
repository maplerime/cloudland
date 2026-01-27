/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

// CGStatus represents the status of a consistency group
type CGStatus string

const (
	CGStatusPending    CGStatus = "pending"    // 等待中
	CGStatusProcessing CGStatus = "processing" // 处理中
	CGStatusAvailable  CGStatus = "available"  // 可用
	CGStatusError      CGStatus = "error"      // 错误
	CGStatusUpdating   CGStatus = "updating"   // 更新中
	CGStatusDeleting   CGStatus = "deleting"   // 删除中
)

// String returns the string representation of CGStatus
func (s CGStatus) String() string {
	return string(s)
}

// ConsistencyGroup represents a consistency group model
// 一致性组模型，用于管理多个卷的一致性快照
type ConsistencyGroup struct {
	Model
	Owner       int64    `gorm:"default:1;index"` // 组织 ID
	Name        string   `gorm:"type:varchar(128)"`
	Description string   `gorm:"type:varchar(512)"`
	Status      CGStatus `gorm:"type:varchar(32)"`
	WdsCgID     string   `gorm:"type:varchar(128)"` // WDS 一致性组 ID
}

// IsBusy checks if the consistency group is in a busy state
// 检查一致性组是否处于繁忙状态
func (cg *ConsistencyGroup) IsBusy() bool {
	if cg.Status == CGStatusPending ||
		cg.Status == CGStatusProcessing ||
		cg.Status == CGStatusUpdating ||
		cg.Status == CGStatusDeleting {
		return true
	}
	return false
}

// IsAvailable checks if the consistency group is available
// 检查一致性组是否可用
func (cg *ConsistencyGroup) IsAvailable() bool {
	return cg.Status == CGStatusAvailable
}

// IsError checks if the consistency group is in error state
// 检查一致性组是否处于错误状态
func (cg *ConsistencyGroup) IsError() bool {
	return cg.Status == CGStatusError
}

// CanDelete checks if the consistency group can be deleted
// 检查一致性组是否可以删除
func (cg *ConsistencyGroup) CanDelete() bool {
	return !cg.IsBusy()
}

// CanUpdate checks if the consistency group can be updated
// 检查一致性组是否可以更新
func (cg *ConsistencyGroup) CanUpdate() bool {
	return cg.IsAvailable()
}

// ConsistencyGroupVolume represents the join table between consistency groups and volumes
// 一致性组与卷的关联表
type ConsistencyGroupVolume struct {
	Model
	CGID     int64             `gorm:"index"` // 一致性组 ID
	CG       *ConsistencyGroup `gorm:"foreignkey:CGID"`
	VolumeID int64             `gorm:"index"` // 卷 ID
	Volume   *Volume           `gorm:"foreignkey:VolumeID"`
}

// CGSnapshotStatus represents the status of a consistency group snapshot
type CGSnapshotStatus string

const (
	CGSnapshotStatusPending   CGSnapshotStatus = "pending"   // 创建中
	CGSnapshotStatusAvailable CGSnapshotStatus = "available" // 可用
	CGSnapshotStatusError     CGSnapshotStatus = "error"     // 错误
	CGSnapshotStatusRestoring CGSnapshotStatus = "restoring" // 恢复中
	CGSnapshotStatusDeleting  CGSnapshotStatus = "deleting"  // 删除中
)

// String returns the string representation of CGSnapshotStatus
func (s CGSnapshotStatus) String() string {
	return string(s)
}

// ConsistencyGroupSnapshot represents a consistency group snapshot model
// 一致性组快照模型
type ConsistencyGroupSnapshot struct {
	Model
	Owner       int64                `gorm:"default:1;index"` // 组织 ID
	Name        string               `gorm:"type:varchar(128)"`
	Description string               `gorm:"type:varchar(512)"`
	Status      CGSnapshotStatus     `gorm:"type:varchar(32)"`
	CGID        int64                `gorm:"index"` // 一致性组 ID
	CG          *ConsistencyGroup    `gorm:"foreignkey:CGID"`
	Size        int64                // 快照总大小（所有卷快照大小之和）
	WdsSnapID   string               `gorm:"type:varchar(128)"` // WDS 一致性组快照 ID
	TaskID      int64                `gorm:"index"`             // 关联的任务 ID
	Task        *Task                `gorm:"foreignkey:TaskID"`
}

// CanDelete checks if the consistency group snapshot can be deleted
// 检查一致性组快照是否可以删除
func (cgs *ConsistencyGroupSnapshot) CanDelete() bool {
	return cgs.Status != CGSnapshotStatusRestoring &&
		cgs.Status != CGSnapshotStatusPending &&
		cgs.Status != CGSnapshotStatusDeleting
}

// CanRestore checks if the consistency group snapshot can be restored
// 检查一致性组快照是否可以恢复
func (cgs *ConsistencyGroupSnapshot) CanRestore() bool {
	return cgs.Status == CGSnapshotStatusAvailable
}

// IsBusy checks if the consistency group snapshot is in a busy state
// 检查一致性组快照是否处于繁忙状态
func (cgs *ConsistencyGroupSnapshot) IsBusy() bool {
	if cgs.Status == CGSnapshotStatusPending ||
		cgs.Status == CGSnapshotStatusRestoring ||
		cgs.Status == CGSnapshotStatusDeleting {
		return true
	}
	return false
}

func init() {
	dbs.AutoMigrate(&ConsistencyGroup{}, &ConsistencyGroupVolume{}, &ConsistencyGroupSnapshot{})
}
