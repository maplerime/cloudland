/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"encoding/json"
	"testing"
	"time"
)

// TestResourceTypeString 测试 ResourceType 的 String 方法
func TestResourceTypeString(t *testing.T) {
	tests := []struct {
		name string
		rt   ResourceType
		want string
	}{
		{"Instance", ResourceTypeInstance, "instance"},
		{"Volume", ResourceTypeVolume, "volume"},
		{"Image", ResourceTypeImage, "image"},
		{"Interface", ResourceTypeInterface, "interface"},
		{"FloatingIP", ResourceTypeFloatingIP, "floating_ip"},
		{"Router", ResourceTypeRouter, "router"},
		{"Subnet", ResourceTypeSubnet, "subnet"},
		{"SecurityGroup", ResourceTypeSecurityGroup, "security_group"},
		{"Hyper", ResourceTypeHyper, "hyper"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rt.String(); got != tt.want {
				t.Errorf("ResourceType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResourceChangeEventSerialization 测试 ResourceChangeEvent 序列化
func TestResourceChangeEventSerialization(t *testing.T) {
	now := time.Now()
	event := &ResourceChangeEvent{
		ResourceType: ResourceTypeInstance,
		ResourceUUID: "550e8400-e29b-41d4-a716-446655440000",
		TenantID:     111,
		Timestamp:    now,
		Data: map[string]interface{}{
			"hostname": "test-vm-001",
			"status":   "running",
			"cpu":      4,
			"memory":   8192,
		},
		Metadata: map[string]interface{}{
			"region": "us-east-1",
		},
	}

	// 序列化
	jsonData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal ResourceChangeEvent: %v", err)
	}

	// 反序列化
	var decoded ResourceChangeEvent
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ResourceChangeEvent: %v", err)
	}

	// 验证字段
	if decoded.ResourceType != event.ResourceType {
		t.Errorf("ResourceType = %v, want %v", decoded.ResourceType, event.ResourceType)
	}

	if decoded.ResourceUUID != event.ResourceUUID {
		t.Errorf("ResourceUUID = %v, want %v", decoded.ResourceUUID, event.ResourceUUID)
	}

	if decoded.TenantID != event.TenantID {
		t.Errorf("TenantID = %v, want %v", decoded.TenantID, event.TenantID)
	}

	// 验证 Data 字段
	if decoded.Data["hostname"] != event.Data["hostname"] {
		t.Errorf("Data[hostname] = %v, want %v", decoded.Data["hostname"], event.Data["hostname"])
	}

	if decoded.Data["status"] != event.Data["status"] {
		t.Errorf("Data[status] = %v, want %v", decoded.Data["status"], event.Data["status"])
	}

	// 验证 Metadata 字段
	if decoded.Metadata["region"] != event.Metadata["region"] {
		t.Errorf("Metadata[region] = %v, want %v", decoded.Metadata["region"], event.Metadata["region"])
	}
}

// TestResourceSerialization 测试 Resource 序列化
func TestResourceSerialization(t *testing.T) {
	resource := &Resource{
		Type:   "instance",
		ID:     "550e8400-e29b-41d4-a716-446655440000",
		Name:   "test-instance",
		Region: "us-east-1",
		Tags: map[string]string{
			"environment": "test",
			"team":        "devops",
		},
	}

	// 序列化
	jsonData, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("Failed to marshal Resource: %v", err)
	}

	// 反序列化
	var decoded Resource
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal Resource: %v", err)
	}

	// 验证字段
	if decoded.Type != resource.Type {
		t.Errorf("Type = %v, want %v", decoded.Type, resource.Type)
	}

	if decoded.ID != resource.ID {
		t.Errorf("ID = %v, want %v", decoded.ID, resource.ID)
	}

	if decoded.Name != resource.Name {
		t.Errorf("Name = %v, want %v", decoded.Name, resource.Name)
	}

	if decoded.Region != resource.Region {
		t.Errorf("Region = %v, want %v", decoded.Region, resource.Region)
	}

	if decoded.Tags["environment"] != resource.Tags["environment"] {
		t.Errorf("Tags[environment] = %v, want %v", decoded.Tags["environment"], resource.Tags["environment"])
	}
}

// TestEventSerialization 测试 Event 序列化
func TestEventSerialization(t *testing.T) {
	now := time.Now()
	event := &Event{
		EventType:  "launch_vm",
		Source:     "Cloudland",
		OccurredAt: now,
		TenantID:   "111",
		Resource: Resource{
			Type: "instance",
			ID:   "550e8400-e29b-41d4-a716-446655440000",
			Name: "test-vm-001",
		},
		Data: map[string]interface{}{
			"hostname": "test-vm-001",
			"status":   "running",
			"cpu":      4,
			"memory":   8192,
		},
		Metadata: map[string]interface{}{
			"region": "us-east-1",
			"zone":   1,
		},
		RetryCount: 0,
	}

	// 序列化
	jsonData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal Event: %v", err)
	}

	// 反序列化
	var decoded Event
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal Event: %v", err)
	}

	// 验证字段
	if decoded.EventType != event.EventType {
		t.Errorf("EventType = %v, want %v", decoded.EventType, event.EventType)
	}

	if decoded.Source != event.Source {
		t.Errorf("Source = %v, want %v", decoded.Source, event.Source)
	}

	if decoded.TenantID != event.TenantID {
		t.Errorf("TenantID = %v, want %v", decoded.TenantID, event.TenantID)
	}

	// 验证 Resource 字段
	if decoded.Resource.Type != event.Resource.Type {
		t.Errorf("Resource.Type = %v, want %v", decoded.Resource.Type, event.Resource.Type)
	}

	if decoded.Resource.ID != event.Resource.ID {
		t.Errorf("Resource.ID = %v, want %v", decoded.Resource.ID, event.Resource.ID)
	}

	// 验证 Data 字段
	if decoded.Data["hostname"] != event.Data["hostname"] {
		t.Errorf("Data[hostname] = %v, want %v", decoded.Data["hostname"], event.Data["hostname"])
	}

	// 验证 Metadata 字段
	if decoded.Metadata["region"] != event.Metadata["region"] {
		t.Errorf("Metadata[region] = %v, want %v", decoded.Metadata["region"], event.Metadata["region"])
	}

	// RetryCount 不应该被序列化（因为有 `json:"-"` 标签）
	// 但反序列化后应该是 0
	if decoded.RetryCount != 0 {
		t.Errorf("RetryCount should not be serialized, got %v", decoded.RetryCount)
	}
}

// TestEventRetryCountNotSerialized 测试 RetryCount 不被序列化
func TestEventRetryCountNotSerialized(t *testing.T) {
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
		RetryCount: 3, // 设置重试次数
	}

	// 序列化
	jsonData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal Event: %v", err)
	}

	// 验证 RetryCount 不在 JSON 中
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if _, exists := decoded["retry_count"]; exists {
		t.Error("RetryCount should not be present in JSON output")
	}
}

// TestResourceChangeEventWithNilMetadata 测试 Metadata 为 nil 的情况
func TestResourceChangeEventWithNilMetadata(t *testing.T) {
	event := &ResourceChangeEvent{
		ResourceType: ResourceTypeInstance,
		ResourceUUID: "550e8400-e29b-41d4-a716-446655440000",
		TenantID:     111,
		Timestamp:    time.Now(),
		Data: map[string]interface{}{
			"status": "running",
		},
		Metadata: nil, // nil metadata
	}

	// 序列化
	jsonData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal ResourceChangeEvent: %v", err)
	}

	// 反序列化
	var decoded ResourceChangeEvent
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ResourceChangeEvent: %v", err)
	}

	// Metadata 应该为 nil 或空 map（因为 omitempty）
	if decoded.Metadata != nil && len(decoded.Metadata) > 0 {
		t.Errorf("Metadata should be nil or empty, got %v", decoded.Metadata)
	}
}

// TestEventWithNilMetadata 测试 Event 的 Metadata 为 nil 的情况
func TestEventWithNilMetadata(t *testing.T) {
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
		Metadata: nil, // nil metadata
	}

	// 序列化
	jsonData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal Event: %v", err)
	}

	// 反序列化
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Metadata 不应该在 JSON 中（因为 omitempty）
	if _, exists := decoded["metadata"]; exists {
		t.Error("Metadata should not be present in JSON output when nil")
	}
}

// TestEventTimestampFormat 测试时间戳格式
func TestEventTimestampFormat(t *testing.T) {
	now := time.Now()
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: now,
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
	}

	// 序列化
	jsonData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal Event: %v", err)
	}

	// 反序列化
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// 验证时间戳是 RFC3339 格式
	occurredAt, ok := decoded["occurred_at"].(string)
	if !ok {
		t.Fatal("occurred_at should be a string")
	}

	// 尝试解析为时间
	_, err = time.Parse(time.RFC3339, occurredAt)
	if err != nil {
		t.Errorf("occurred_at should be in RFC3339 format, got: %v", err)
	}
}

// TestResourceWithEmptyTags 测试 Tags 为空的情况
func TestResourceWithEmptyTags(t *testing.T) {
	resource := &Resource{
		Type: "instance",
		ID:   "test-uuid",
		Tags: nil, // nil tags
	}

	// 序列化
	jsonData, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("Failed to marshal Resource: %v", err)
	}

	// 反序列化
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Tags 不应该在 JSON 中（因为 omitempty）
	if _, exists := decoded["tags"]; exists {
		t.Error("Tags should not be present in JSON output when nil")
	}
}

// TestResourceWithEmptyOptionalFields 测试所有可选字段为空的情况
func TestResourceWithEmptyOptionalFields(t *testing.T) {
	resource := &Resource{
		Type: "instance",
		ID:   "test-uuid",
		// Name, Region, Tags 都为空/nil
	}

	// 序列化
	jsonData, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("Failed to marshal Resource: %v", err)
	}

	// 反序列化
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// 可选字段不应该在 JSON 中
	optionalFields := []string{"name", "region", "tags"}
	for _, field := range optionalFields {
		if _, exists := decoded[field]; exists {
			t.Errorf("Field %s should not be present in JSON output when empty", field)
		}
	}

	// 必填字段应该在 JSON 中
	requiredFields := []string{"type", "id"}
	for _, field := range requiredFields {
		if _, exists := decoded[field]; !exists {
			t.Errorf("Field %s should be present in JSON output", field)
		}
	}
}

// BenchmarkEventSerialization 性能测试
func BenchmarkEventSerialization(b *testing.B) {
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		TenantID:   "123",
		Resource: Resource{
			Type: "instance",
			ID:   "550e8400-e29b-41d4-a716-446655440000",
		},
		Data: map[string]interface{}{
			"hostname": "test-vm-001",
			"status":   "running",
			"cpu":      4,
			"memory":   8192,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(event)
	}
}

// BenchmarkEventDeserialization 性能测试
func BenchmarkEventDeserialization(b *testing.B) {
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		TenantID:   "123",
		Resource: Resource{
			Type: "instance",
			ID:   "550e8400-e29b-41d4-a716-446655440000",
		},
		Data: map[string]interface{}{
			"hostname": "test-vm-001",
			"status":   "running",
		},
	}

	jsonData, _ := json.Marshal(event)

	var decoded Event
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = json.Unmarshal(jsonData, &decoded)
	}
}
