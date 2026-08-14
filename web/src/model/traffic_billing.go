/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"web/src/dbs"
)

// TrafficBillingMapping is the authoritative record of which instances are
// marked as traffic-billing by the external billing system. Presence of a row
// for an instance_uuid means "this VM is traffic billing" -- nothing else
// decides that; see TrafficBillingAdmin in web/src/routes/traffic_billing.go.
type TrafficBillingMapping struct {
	Model
	InstanceUUID string `gorm:"type:varchar(64);not null;unique_index;column:instance_uuid"`
}

func init() {
	dbs.AutoMigrate(&TrafficBillingMapping{})
}
