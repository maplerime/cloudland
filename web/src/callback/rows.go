/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

// 统一放这里，避免每个函数里重复定义 type， 这些字段都是事件的payload
type InstanceRow struct {
	ID         int64
	UUID       string
	Owner      int64
	TenantUUID string `gorm:"column:tenant_uuid"`

	Hostname string
	Status   string
	Hyper    int32 `gorm:"column:hyper"`
	ZoneID   int64 `gorm:"column:zone_id"`
	Cpu      int32 `gorm:"column:cpu"`
	Memory   int32 `gorm:"column:memory"`
	Disk     int32 `gorm:"column:disk"`
}

type VolumeRow struct {
	ID         int64
	UUID       string
	Owner      int64
	TenantUUID string `gorm:"column:tenant_uuid"`

	Name       string
	Status     string
	Size       int32
	InstanceID int64 `gorm:"column:instance_id"`
	Target     string
	Format     string
	Path       string
}

type ImageRow struct {
	ID         int64
	UUID       string
	Owner      int64
	TenantUUID string `gorm:"column:tenant_uuid"`

	Name         string
	Status       string
	Format       string
	OSCode       string `gorm:"column:os_code"`
	Size         int64
	Architecture string
}

type InterfaceRow struct {
	ID         int64
	UUID       string
	Owner      int64
	TenantUUID string `gorm:"column:tenant_uuid"`

	Name     string
	MacAddr  string `gorm:"column:mac_addr"`
	Instance int64  `gorm:"column:instance"`
	Hyper    int32
	Type     string
}
