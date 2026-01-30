/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import "time"

// ResourceType 资源类型枚举
type ResourceType string

const (
	// ResourceTypeInstance 虚拟机实例
	ResourceTypeInstance ResourceType = "instance"
	// ResourceTypeVolume 存储卷
	ResourceTypeVolume ResourceType = "volume"
	// ResourceTypeImage 镜像
	ResourceTypeImage ResourceType = "image"
	// ResourceTypeInterface 网络接口
	ResourceTypeInterface ResourceType = "interface"
	// ResourceTypeFloatingIP 浮动IP
	ResourceTypeFloatingIP ResourceType = "floating_ip"
	// ResourceTypeRouter 路由器
	ResourceTypeRouter ResourceType = "router"
	// ResourceTypeSubnet 子网
	ResourceTypeSubnet ResourceType = "subnet"
	// ResourceTypeSecurityGroup 安全组
	ResourceTypeSecurityGroup ResourceType = "security_group"
	// ResourceTypeHyper 计算节点
	ResourceTypeHyper ResourceType = "hyper"

	// ResourceState 资源状态变更
	ActionStateChanged string = "state_changed"    // 状态变更
	ActionCreated      string = "created"          // 资源创建
	ActionAttached     string = "attached"         // 附加操作
	ActionDetached     string = "detached"         // 分离操作
	ActionResized      string = "resized"          // 调整大小
	ActionDeleted      string = "deleted"          // 资源删除
	ActionUpdated      string = "updated"          // 资源更新
	ActionMigrated     string = "migrated"         // 实例迁移
	ActionCaptured     string = "snapshot_created" // 资源捕获
)

// String 返回资源类型的字符串表示
func (r ResourceType) String() string {
	return string(r)
}

// ResourceChangeEvent 资源变化事件
type ResourceChangeEvent struct {
	// ResourceType 资源类型
	ResourceType ResourceType `json:"resource_type"`
	// ResourceUUID 资源的 UUID
	ResourceUUID string `json:"resource_uuid"`
	// TenantID 所属租户 ID
	TenantID int64 `json:"tenant_id"`
	// Timestamp 事件时间戳
	Timestamp time.Time `json:"timestamp"`
	// Data 事件数据负载
	Data map[string]interface{} `json:"data"`
	// Metadata 额外的元数据 (可选)
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type Resource struct {
	Type   string            `json:"type"`           // 资源类型
	ID     string            `json:"id"`             // 资源 UUID
	Region string            `json:"region"`         // 资源所属区域
	Name   string            `json:"name,omitempty"` // 资源名称
	Tags   map[string]string `json:"tags,omitempty"` // 资源标签
}

// Cloudland event structure to be sent to callback URL
type Event struct {
	EventType  string                 `json:"event_type"`  // Event type (e.g., "instance_created")
	Source     string                 `json:"source"`      // Source system (e.g., "Cloudland", "monitoring")
	OccurredAt time.Time              `json:"occurred_at"` // When the event occurred
	TenantID   string                 `json:"tenant_id"`   // The tenantID in Cloudland
	Resource   Resource               `json:"resource"`
	Data       map[string]interface{} `json:"data"`               // Event data payload as JSON
	Metadata   map[string]interface{} `json:"metadata,omitempty"` // Additional metadata (optional)
	// RetryCount 重试次数 (内部使用，不序列化)
	RetryCount int `json:"-"`
}
