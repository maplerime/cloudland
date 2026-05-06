/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"web/src/dbs"
)

type Resource struct {
	ID          int64 `gorm:"primary_key"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Hostid      int32 `gorm:"unique_index"`
	Cpu         int64
	CpuTotal    int64
	Memory      int64
	MemoryTotal int64
	Disk        int64
	DiskTotal   int64

	// Hugepage memory (free page count from /sys/kernel/mm/hugepages/)
	Hugepages2MFree int64 `gorm:"default:0"`
	Hugepages1GFree int64 `gorm:"default:0"`
	HugepageSizeKB  int64 `gorm:"default:0"` // 0=not enabled, 2048=2MB, 1048576=1GB

	// CPU load (from /proc/loadavg and /proc/stat)
	LoadAvg1m  float64 `gorm:"default:0"`
	LoadAvg5m  float64 `gorm:"default:0"`
	LoadAvg15m float64 `gorm:"default:0"`
	CpuIdlePct float64 `gorm:"default:100"` // 0~100, default 100 so old hypers pass CPU filter
}

func init() {
	dbs.AutoMigrate(&Resource{})
}
