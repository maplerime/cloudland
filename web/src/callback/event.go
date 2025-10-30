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
	// ResourceID 资源的数据库 ID
	ResourceID int64 `json:"resource_id"`
	// Status 新状态
	Status string `json:"status"`
	// PreviousStatus 旧状态 (可选)
	PreviousStatus string `json:"previous_status,omitempty"`
	// Timestamp 事件时间戳
	Timestamp time.Time `json:"timestamp"`
	// Metadata 额外的元数据
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	// RetryCount 重试次数 (内部使用，不序列化)
	RetryCount int `json:"-"`
}
