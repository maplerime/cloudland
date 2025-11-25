/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"strings"
	"web/src/dbs"
)

type VolumeStatus string
type BackupStatus string

const (
	VolumeStatusResizing  VolumeStatus = "resizing"
	VolumeStatusAvailable VolumeStatus = "available"
	VolumeStatusAttached  VolumeStatus = "attached"
	VolumeStatusAttaching VolumeStatus = "attaching"
	VolumeStatusDetaching VolumeStatus = "detaching"
	VolumeStatusRestoring VolumeStatus = "restoring"
	VolumeStatusBackuping VolumeStatus = "backuping"
	VolumeStatusError     VolumeStatus = "error"
)

func (s VolumeStatus) String() string {
	return string(s)
}

const (
	BackupStatusPending   BackupStatus = "pending"
	BackupStatusReady     BackupStatus = "available"
	BackupStatusError     BackupStatus = "error"
	BackupStatusRestoring BackupStatus = "restoring"
)

func (s BackupStatus) String() string {
	return string(s)
}

type Volume struct {
	Model
	Owner int64  `gorm:"default:1","index"` /* The organization ID of the resource */
	Name  string `gorm:"type:varchar(128)"`
	/*
		The path of the volume, format is:
		<volume driver>://<volume-path>
		for example:
		Local storage: local:///var/lib/cloudland/volumes/volume-1.qcow2
		WDS Vhost: wds_vhost://<pool-id>/<volume-id>
		WDS ISCSI: wds_iscsi://<pool-id>/<volume-id>
		The volume driver is the name of the driver that is used to create the volume.
	*/
	Path       string `gorm:"type:varchar(256)"`
	Size       int32
	Booting    bool
	Format     string       `gorm:"type:varchar(32)"`
	Status     VolumeStatus `gorm:"type:varchar(32)"`
	Target     string       `gorm:"type:varchar(32)"`
	Href       string       `gorm:"type:varchar(256)"`
	InstanceID int64
	Instance   *Instance `gorm:"foreignkey:InstanceID"`
	IopsLimit  int32
	IopsBurst  int32
	BpsLimit   int32
	BpsBurst   int32
	PoolID     string `gorm:"type:varchar(128)"`
}

func (v *Volume) IsBusy() bool {
	if v.Status == VolumeStatusPending ||
		v.Status == VolumeStatusResizing ||
		v.Status == VolumeStatusAttaching ||
		v.Status == VolumeStatusDetaching ||
		v.Status == VolumeStatusRestoring ||
		v.Status == VolumeStatusBackuping {
		return true
	}
	return false
}

func (v *Volume) IsError() bool {
	return v.Status == VolumeStatusError
}

func (v *Volume) IsAvailable() bool {
	return v.Status == VolumeStatusAvailable
}

func (v *Volume) IsAttached() bool {
	return v.Status == VolumeStatusAttached
}

func (v *Volume) ParsePath() []string {
	if v.Path != "" {
		parts := strings.SplitN(v.Path, "://", 2)
		if len(parts) == 2 {
			driver := parts[0]
			if driver == "local" {
				return []string{driver, parts[1]}
			} else {
				res := []string{driver}
				res = append(res, strings.Split(parts[1], "/")...)
				return res
			}
		}
	}
	return nil
}

func (v *Volume) GetVolumeDriver() string {
	return parseDriver(v.Path)
}

func (v *Volume) GetVolumePath() string {
	return parsePath(v.Path)
}

func (v *Volume) GetVolumePoolID() string {
	return parsePoolID(v.Path)
}

func (v *Volume) GetOriginVolumeID() string {
	return parseOriginID(v.Path, v.UUID)
}

type VolumeBackup struct {
	Model
	Owner      int64  `gorm:"default:1;index"` /* The organization ID of the resource */
	Name       string `gorm:"type:varchar(128)"`
	VolumeID   int64
	Volume     *Volume      `gorm:"foreignkey:VolumeID;index"`
	BackupType string       `gorm:"type:varchar(32);index"` // snapshot or backup
	Status     BackupStatus `gorm:"type:varchar(32)"`
	Size       int32
	Path       string `gorm:"type:varchar(256)"`
	SnapshotID string `gorm:"type:varchar(128)"` // for cross pool backup, the snapshot ID in the source pool
	TaskID     int64  `gorm:"index"`             // the task ID for the backup or restore
	Task       *Task  `gorm:"foreignkey:TaskID"`
}

func (v *VolumeBackup) CanDelete() bool {
	return v.Status != BackupStatusRestoring && v.Status != BackupStatusPending
}
func (v *VolumeBackup) CanRestore() bool {
	return v.Status == BackupStatusReady
}

func (v *VolumeBackup) GetBackupDriver() string {
	return parseDriver(v.Path)
}

func (v *VolumeBackup) GetBackupPath() string {
	return parsePath(v.Path)
}

func (v *VolumeBackup) GetBackupPoolID() string {
	return parsePoolID(v.Path)
}

func (v *VolumeBackup) GetOriginBackupID() string {
	return parseOriginID(v.Path, "")
}

func parse(path string) []string {
	if path != "" {
		parts := strings.SplitN(path, "://", 2)
		if len(parts) == 2 {
			driver := parts[0]
			if driver == "local" {
				return []string{driver, parts[1]}
			} else {
				res := []string{driver}
				res = append(res, strings.Split(parts[1], "/")...)
				return res
			}
		}
	}
	return nil
}

func parseDriver(path string) string {
	parts := parse(path)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func parsePath(path string) string {
	parts := parse(path)
	if (len(parts) > 1) && (parts[0] == "local") {
		return parts[1]
	}
	return path
}

func parsePoolID(path string) string {
	parts := parse(path)
	if len(parts) == 3 {
		return parts[1]
	}
	return ""
}

func parseOriginID(path string, id string) string {
	parts := parse(path)
	if len(parts) == 3 {
		return parts[2]
	}
	return id
}

func init() {
	dbs.AutoMigrate(&Volume{}, &VolumeBackup{})
}
