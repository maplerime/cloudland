/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import "time"

// ResourceType enum.
type ResourceType string

const (
	// Resource types.
	ResourceTypeInstance      ResourceType = "instance"
	ResourceTypeVolume        ResourceType = "volume"
	ResourceTypeImage         ResourceType = "image"
	ResourceTypeInterface     ResourceType = "interface"
	ResourceTypeFloatingIP    ResourceType = "floating_ip"
	ResourceTypeRouter        ResourceType = "router"
	ResourceTypeSubnet        ResourceType = "subnet"
	ResourceTypeSecurityGroup ResourceType = "security_group"
	ResourceTypeHyper         ResourceType = "hyper"

	// Resource action types.
	ActionStateChanged string = "state_changed"
	ActionCreated      string = "created"
	ActionAttached     string = "attached"
	ActionDetached     string = "detached"
	ActionResized      string = "resized"
	ActionDeleted      string = "deleted"
	ActionUpdated      string = "updated"
	ActionMigrated     string = "migrated"
	ActionCaptured     string = "snapshot_created"
)

// String returns ResourceType as string.
func (r ResourceType) String() string {
	return string(r)
}

// ResourceChangeEvent describes one resource change.
type ResourceChangeEvent struct {
	ResourceType ResourceType           `json:"resource_type"`
	ResourceUUID string                 `json:"resource_uuid"`
	TenantID     string                 `json:"tenant_id"`
	Timestamp    time.Time              `json:"timestamp"`
	Data         map[string]interface{} `json:"data"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type Resource struct {
	Type   string            `json:"type"`
	ID     string            `json:"id"`
	Region string            `json:"region"`
	Name   string            `json:"name,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

// Cloudland event structure to be sent to callback URL.
type Event struct {
	EventType  string                 `json:"event_type"`  // Event type (e.g., "instance_created")
	Source     string                 `json:"source"`      // Source system (e.g., "Cloudland", "monitoring")
	OccurredAt time.Time              `json:"occurred_at"` // When the event occurred
	TenantID   string                 `json:"tenant_id"`   // The tenantID in Cloudland
	Resource   Resource               `json:"resource"`
	Data       map[string]interface{} `json:"data"`               // Event data payload as JSON
	Metadata   map[string]interface{} `json:"metadata,omitempty"` // Additional metadata (optional)
	RetryCount int                    `json:"-"`                  // Internal retry counter (not serialized)
}
