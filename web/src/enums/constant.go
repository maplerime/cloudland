/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package enums

type InstanceStatus string

const (
	InstanceStatusPending      InstanceStatus = "pending"
	InstanceStatusRunning      InstanceStatus = "running"
	InstanceStatusShutoff      InstanceStatus = "shut_off"
	InstanceStatusPaused       InstanceStatus = "paused"
	InstanceStatusMigrating    InstanceStatus = "migrating"
	InstanceStatusReinstalling InstanceStatus = "reinstalling"
	InstanceStatusResizing     InstanceStatus = "resizing"
	InstanceStatusDeleting     InstanceStatus = "deleting"
	InstanceStatusDeleted      InstanceStatus = "deleted"
)

func (s InstanceStatus) String() string {
	return string(s)
}
