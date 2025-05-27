/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package common

type InstanceStatuses string

const (
	InstanceStatusPending      InstanceStatuses = "pending"
	InstanceStatusRunning      InstanceStatuses = "running"
	InstanceStatusShutoff      InstanceStatuses = "shut_off"
	InstanceStatusPaused       InstanceStatuses = "paused"
	InstanceStatusMigrating    InstanceStatuses = "migrating"
	InstanceStatusReinstalling InstanceStatuses = "reinstalling"
	InstanceStatusResizing     InstanceStatuses = "resizing"
	InstanceStatusDeleting     InstanceStatuses = "deleting"
	InstanceStatusDeleted      InstanceStatuses = "deleted"
)
