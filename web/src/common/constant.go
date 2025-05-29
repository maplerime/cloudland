/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package common

type PowerAction string
type SubnetType string
type InstanceStatus string
type VolumeStatus string

const (
	Stop        PowerAction = "stop"
	HardStop    PowerAction = "hard_stop"
	Start       PowerAction = "start"
	Restart     PowerAction = "restart"
	HardRestart PowerAction = "hard_restart"
	Pause       PowerAction = "pause"
	Resume      PowerAction = "resume"

	Public   SubnetType = "public"
	Internal SubnetType = "internal"

	SystemDefaultSGName = "system-default"
	TimeStringForMat    = "2006-01-02 15:04:05.000000"

	InstanceStatusPending      InstanceStatus = "pending"
	InstanceStatusRunning      InstanceStatus = "running"
	InstanceStatusShutoff      InstanceStatus = "shut_off"
	InstanceStatusPaused       InstanceStatus = "paused"
	InstanceStatusMigrating    InstanceStatus = "migrating"
	InstanceStatusReinstalling InstanceStatus = "reinstalling"
	InstanceStatusResizing     InstanceStatus = "resizing"
	InstanceStatusDeleting     InstanceStatus = "deleting"
	InstanceStatusDeleted      InstanceStatus = "deleted"

	VolumeStatusResizing  VolumeStatus = "resizing"
	VolumeStatusAvailable VolumeStatus = "available"
	VolumeStatusAttached  VolumeStatus = "attached"
)

var SignedSeret = []byte("Red B")
