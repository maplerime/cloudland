/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"context"
	"fmt"
	"testing"

	"web/src/dbs"

	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

// mockDB 模拟 DB 函数（用于测试）
func mockDB(db *gorm.DB) func() {
	originalDBFunc := dbFunc
	dbFunc = func() *gorm.DB { return db }
	return func() { dbFunc = originalDBFunc }
}

var dbFunc func() *gorm.DB = dbs.DB

// setupTestDB 创建测试数据库
func setupTestDB() (*gorm.DB, error) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open test database: %v", err)
	}
	return db, nil
}

// setupTestData 在测试数据库中插入测试数据
func setupTestData(t *testing.T, db *gorm.DB) {
	// 创建表结构
	db.Exec(`CREATE TABLE instances (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT UNIQUE,
		hostname TEXT,
		status TEXT,
		hyper INTEGER,
		zone_id INTEGER,
		cpu INTEGER,
		memory INTEGER,
		disk INTEGER,
		owner INTEGER
	)`)

	db.Exec(`CREATE TABLE volumes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT UNIQUE,
		name TEXT,
		status TEXT,
		size INTEGER,
		instance_id INTEGER,
		target TEXT,
		format TEXT,
		path TEXT,
		owner INTEGER
	)`)

	db.Exec(`CREATE TABLE images (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT UNIQUE,
		name TEXT,
		status TEXT,
		format TEXT,
		os_code TEXT,
		size INTEGER,
		architecture TEXT,
		owner INTEGER
	)`)

	db.Exec(`CREATE TABLE interfaces (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT UNIQUE,
		name TEXT,
		mac_addr TEXT,
		instance INTEGER,
		hyper INTEGER,
		type TEXT,
		owner INTEGER
	)`)

	// 插入测试数据
	// Instance
	db.Exec(`INSERT INTO instances (uuid, hostname, status, hyper, zone_id, cpu, memory, disk, owner)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"550e8400-e29b-41d4-a716-446655440000", "test-vm-001", "running", 5, 1, 4, 8192, 100, 111)

	// Volume
	db.Exec(`INSERT INTO volumes (uuid, name, status, size, instance_id, target, format, path, owner)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"660e8400-e29b-41d4-a716-446655440001", "test-volume-001", "available", 100, 0, "", "qcow2", "local:///var/lib/cloudland/volumes/volume-1.qcow2", 111)

	// Image
	db.Exec(`INSERT INTO images (uuid, name, status, format, os_code, size, architecture, owner)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"770e8400-e29b-41d4-a716-446655440002", "ubuntu-22.04", "active", "qcow2", "linux", 2147483648, "x86_64", 111)

	// Interface
	db.Exec(`INSERT INTO interfaces (uuid, name, mac_addr, instance, hyper, type, owner)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"880e8400-e29b-41d4-a716-446655440003", "eth0", "52:54:00:12:34:56", 1, 5, "vxlan", 111)
}

// TestDefaultExtractor 测试默认提取器
// 注意：需要 SQLite 驱动
func TestDefaultExtractor(t *testing.T) {
	// 检查是否可以连接 SQLite
	testDB, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("SQLite driver not available, skipping database tests. Install with: go get github.com/mattn/go-sqlite3")
	}
	testDB.Close()

	db, err := setupTestDB()
	if err != nil {
		t.Skipf("SQLite driver not available, skipping database tests. Install with: go get github.com/mattn/go-sqlite3")
		return
	}
	defer db.Close()

	setupTestData(t, db)
	defer mockDB(db)()

	tests := []struct {
		name        string
		metadata    *ResourceMetadata
		args        []string
		wantUUID    string
		wantTenant  int64
		wantDataLen int
		wantErr     bool
	}{
		{
			name: "Extract instance info",
			metadata: &ResourceMetadata{
				ResourceType: ResourceTypeInstance,
				IDArgIndex:   1,
			},
			args:        []string{"launch_vm", "1"},
			wantUUID:    "550e8400-e29b-41d4-a716-446655440000",
			wantTenant:  111,
			wantDataLen: 7,
			wantErr:     false,
		},
		{
			name: "Extract volume info",
			metadata: &ResourceMetadata{
				ResourceType: ResourceTypeVolume,
				IDArgIndex:   1,
			},
			args:        []string{"create_volume_local", "1"},
			wantUUID:    "660e8400-e29b-41d4-a716-446655440001",
			wantTenant:  111,
			wantDataLen: 7,
			wantErr:     false,
		},
		{
			name: "Extract image info",
			metadata: &ResourceMetadata{
				ResourceType: ResourceTypeImage,
				IDArgIndex:   1,
			},
			args:        []string{"create_image", "1"},
			wantUUID:    "770e8400-e29b-41d4-a716-446655440002",
			wantTenant:  111,
			wantDataLen: 6,
			wantErr:     false,
		},
		{
			name: "Extract interface info",
			metadata: &ResourceMetadata{
				ResourceType: ResourceTypeInterface,
				IDArgIndex:   1,
			},
			args:        []string{"attach_vm_nic", "1"},
			wantUUID:    "880e8400-e29b-41d4-a716-446655440003",
			wantTenant:  111,
			wantDataLen: 6,
			wantErr:     false,
		},
		{
			name: "Invalid arg index",
			metadata: &ResourceMetadata{
				ResourceType: ResourceTypeInstance,
				IDArgIndex:   10, // 超出范围
			},
			args:        []string{"launch_vm", "1"},
			wantUUID:    "",
			wantTenant:  0,
			wantDataLen: 0,
			wantErr:     false, // 不应该返回错误，而是返回 nil
		},
		{
			name: "Invalid resource ID",
			metadata: &ResourceMetadata{
				ResourceType: ResourceTypeInstance,
				IDArgIndex:   1,
			},
			args:        []string{"launch_vm", "invalid"},
			wantUUID:    "",
			wantTenant:  0,
			wantDataLen: 0,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := defaultExtractor(context.Background(), tt.metadata, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("defaultExtractor() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("defaultExtractor() unexpected error = %v", err)
				return
			}

			if event == nil {
				if tt.wantUUID != "" {
					t.Errorf("defaultExtractor() returned nil, expected event with UUID %s", tt.wantUUID)
				}
				return
			}

			if event.ResourceUUID != tt.wantUUID {
				t.Errorf("ResourceUUID = %s, want %s", event.ResourceUUID, tt.wantUUID)
			}

			if event.TenantID != tt.wantTenant {
				t.Errorf("TenantID = %d, want %d", event.TenantID, tt.wantTenant)
			}

			if len(event.Data) != tt.wantDataLen {
				t.Errorf("Data length = %d, want %d", len(event.Data), tt.wantDataLen)
			}
		})
	}
}

// TestExtractInstanceInfo 测试提取虚拟机信息
func TestExtractInstanceInfo(t *testing.T) {
	db, err := setupTestDB()
	if err != nil {
		t.Skipf("SQLite driver not available, skipping database tests. Error: %v", err)
		return
	}
	defer db.Close()

	setupTestData(t, db)

	tests := []struct {
		name       string
		resourceID int64
		wantUUID   string
		wantStatus string
		wantErr    bool
	}{
		{
			name:       "Existing instance",
			resourceID: 1,
			wantUUID:   "550e8400-e29b-41d4-a716-446655440000",
			wantStatus: "running",
			wantErr:    false,
		},
		{
			name:       "Non-existent instance",
			resourceID: 999,
			wantUUID:   "",
			wantStatus: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := extractInstanceInfo(db, tt.resourceID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("extractInstanceInfo() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("extractInstanceInfo() unexpected error = %v", err)
				return
			}

			if event.ResourceUUID != tt.wantUUID {
				t.Errorf("ResourceUUID = %s, want %s", event.ResourceUUID, tt.wantUUID)
			}

			if status, ok := event.Data["status"].(string); !ok || status != tt.wantStatus {
				t.Errorf("Status = %v, want %s", event.Data["status"], tt.wantStatus)
			}
		})
	}
}

// TestExtractVolumeInfo 测试提取存储卷信息
func TestExtractVolumeInfo(t *testing.T) {
	db, err := setupTestDB()
	if err != nil {
		t.Skipf("SQLite driver not available, skipping database tests. Error: %v", err)
		return
	}
	defer db.Close()

	setupTestData(t, db)

	tests := []struct {
		name       string
		resourceID int64
		wantUUID   string
		wantStatus string
		wantSize   int
		wantErr    bool
	}{
		{
			name:       "Existing volume",
			resourceID: 1,
			wantUUID:   "660e8400-e29b-41d4-a716-446655440001",
			wantStatus: "available",
			wantSize:   100,
			wantErr:    false,
		},
		{
			name:       "Non-existent volume",
			resourceID: 999,
			wantUUID:   "",
			wantStatus: "",
			wantSize:   0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := extractVolumeInfo(db, tt.resourceID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("extractVolumeInfo() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("extractVolumeInfo() unexpected error = %v", err)
				return
			}

			if event.ResourceUUID != tt.wantUUID {
				t.Errorf("ResourceUUID = %s, want %s", event.ResourceUUID, tt.wantUUID)
			}

			if status, ok := event.Data["status"].(string); !ok || status != tt.wantStatus {
				t.Errorf("Status = %v, want %s", event.Data["status"], tt.wantStatus)
			}
		})
	}
}

// TestExtractImageInfo 测试提取镜像信息
func TestExtractImageInfo(t *testing.T) {
	db, err := setupTestDB()
	if err != nil {
		t.Skipf("SQLite driver not available, skipping database tests. Error: %v", err)
		return
	}
	defer db.Close()

	setupTestData(t, db)

	tests := []struct {
		name       string
		resourceID int64
		wantUUID   string
		wantStatus string
		wantSize   int64
		wantErr    bool
	}{
		{
			name:       "Existing image",
			resourceID: 1,
			wantUUID:   "770e8400-e29b-41d4-a716-446655440002",
			wantStatus: "active",
			wantSize:   2147483648,
			wantErr:    false,
		},
		{
			name:       "Non-existent image",
			resourceID: 999,
			wantUUID:   "",
			wantStatus: "",
			wantSize:   0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := extractImageInfo(db, tt.resourceID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("extractImageInfo() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("extractImageInfo() unexpected error = %v", err)
				return
			}

			if event.ResourceUUID != tt.wantUUID {
				t.Errorf("ResourceUUID = %s, want %s", event.ResourceUUID, tt.wantUUID)
			}

			if status, ok := event.Data["status"].(string); !ok || status != tt.wantStatus {
				t.Errorf("Status = %v, want %s", event.Data["status"], tt.wantStatus)
			}
		})
	}
}

// TestExtractInterfaceInfo 测试提取网络接口信息
func TestExtractInterfaceInfo(t *testing.T) {
	db, err := setupTestDB()
	if err != nil {
		t.Skipf("SQLite driver not available, skipping database tests. Error: %v", err)
		return
	}
	defer db.Close()

	setupTestData(t, db)

	tests := []struct {
		name       string
		resourceID int64
		wantUUID   string
		wantStatus string
		wantErr    bool
	}{
		{
			name:       "Existing interface (attached)",
			resourceID: 1,
			wantUUID:   "880e8400-e29b-41d4-a716-446655440003",
			wantStatus: "active",
			wantErr:    false,
		},
		{
			name:       "Non-existent interface",
			resourceID: 999,
			wantUUID:   "",
			wantStatus: "",
			wantErr:    true,
		},
	}

	// 添加未挂载的接口测试
	db.Exec(`INSERT INTO interfaces (uuid, name, mac_addr, instance, hyper, type, owner)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"990e8400-e29b-41d4-a716-446655440004", "eth1", "52:54:00:12:34:57", 0, -1, "vxlan", 111)

	tests = append(tests, struct {
		name       string
		resourceID int64
		wantUUID   string
		wantStatus string
		wantErr    bool
	}{
		name:       "Existing interface (unattached)",
		resourceID: 2,
		wantUUID:   "990e8400-e29b-41d4-a716-446655440004",
		wantStatus: "unattached",
		wantErr:    false,
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := extractInterfaceInfo(db, tt.resourceID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("extractInterfaceInfo() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("extractInterfaceInfo() unexpected error = %v", err)
				return
			}

			if event.ResourceUUID != tt.wantUUID {
				t.Errorf("ResourceUUID = %s, want %s", event.ResourceUUID, tt.wantUUID)
			}

			if status, ok := event.Data["status"].(string); !ok || status != tt.wantStatus {
				t.Errorf("Status = %v, want %s", event.Data["status"], tt.wantStatus)
			}
		})
	}
}

// TestExtractInstanceStatusBatch 测试批量状态更新
func TestExtractInstanceStatusBatch(t *testing.T) {
	args := []string{"launch_vm", "3", "5 running 7 running 9 shut_off"}

	event, err := extractInstanceStatusBatch(context.Background(), args)

	if err != nil {
		t.Errorf("extractInstanceStatusBatch() unexpected error = %v", err)
	}

	if event != nil {
		t.Error("extractInstanceStatusBatch() should return nil for batch operation")
	}
}

// TestCommandMetadataRegistry 测试命令元数据注册表
func TestCommandMetadataRegistry(t *testing.T) {
	tests := []struct {
		name          string
		cmd           string
		wantExists    bool
		wantType      ResourceType
		wantArgIndex  int
		wantExtractor bool
	}{
		{
			name:         "launch_vm command",
			cmd:          "launch_vm",
			wantExists:   true,
			wantType:     ResourceTypeInstance,
			wantArgIndex: 1,
		},
		{
			name:          "inst_status command",
			cmd:           "inst_status",
			wantExists:    true,
			wantType:      ResourceTypeInstance,
			wantExtractor: true,
		},
		{
			name:         "create_volume_local command",
			cmd:          "create_volume_local",
			wantExists:   true,
			wantType:     ResourceTypeVolume,
			wantArgIndex: 1,
		},
		{
			name:         "attach_volume_local command",
			cmd:          "attach_volume_local",
			wantExists:   true,
			wantType:     ResourceTypeVolume,
			wantArgIndex: 2,
		},
		{
			name:         "create_image command",
			cmd:          "create_image",
			wantExists:   true,
			wantType:     ResourceTypeImage,
			wantArgIndex: 1,
		},
		{
			name:         "attach_vm_nic command",
			cmd:          "attach_vm_nic",
			wantExists:   true,
			wantType:     ResourceTypeInterface,
			wantArgIndex: 2,
		},
		{
			name:       "Unknown command",
			cmd:        "unknown_command",
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, exists := commandMetadataRegistry[tt.cmd]

			if exists != tt.wantExists {
				t.Errorf("Command %s exists = %v, want %v", tt.cmd, exists, tt.wantExists)
				return
			}

			if !tt.wantExists {
				return
			}

			if metadata.ResourceType != tt.wantType {
				t.Errorf("ResourceType = %s, want %s", metadata.ResourceType, tt.wantType)
			}

			if tt.wantArgIndex != 0 && metadata.IDArgIndex != tt.wantArgIndex {
				t.Errorf("IDArgIndex = %d, want %d", metadata.IDArgIndex, tt.wantArgIndex)
			}

			if tt.wantExtractor && metadata.Extractor == nil {
				t.Error("Expected Extractor to be set, got nil")
			}
		})
	}
}

// TestExtractAndPushEvent 测试提取并推送事件
func TestExtractAndPushEvent(t *testing.T) {
	db, err := setupTestDB()
	if err != nil {
		t.Skipf("SQLite driver not available, skipping database tests. Error: %v", err)
		return
	}
	defer db.Close()

	setupTestData(t, db)
	defer mockDB(db)()

	// 设置配置
	viper.Set("callback.enabled", true)
	viper.Set("callback.url", "http://test.example.com")
	viper.Set("callback.workers", 1)
	viper.Set("callback.queue_size", 100)
	viper.Set("callback.timeout", 5)
	viper.Set("callback.retry_max", 1)
	viper.Set("callback.retry_interval", 1)

	// 初始化队列
	InitQueue(10)
	defer func() {
		// 清空队列
		for len(eventQueue) > 0 {
			<-eventQueue
		}
	}()

	tests := []struct {
		name       string
		cmd        string
		args       []string
		execError  error
		wantPushed bool
	}{
		{
			name:       "Successful instance launch",
			cmd:        "launch_vm",
			args:       []string{"launch_vm", "1"},
			execError:  nil,
			wantPushed: true,
		},
		{
			name:       "Failed command execution",
			cmd:        "launch_vm",
			args:       []string{"launch_vm", "1"},
			execError:  fmt.Errorf("command failed"),
			wantPushed: false,
		},
		{
			name:       "Unregistered command",
			cmd:        "unknown_command",
			args:       []string{"unknown_command", "1"},
			execError:  nil,
			wantPushed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 清空队列
			for len(eventQueue) > 0 {
				<-eventQueue
			}

			// 执行提取和推送
			ExtractAndPushEvent(context.Background(), tt.cmd, tt.args, tt.execError)

			// 检查队列
			queueLength := GetQueueLength()
			wasPushed := queueLength > 0

			if wasPushed != tt.wantPushed {
				t.Errorf("Event pushed = %v, want %v", wasPushed, tt.wantPushed)
			}
		})
	}
}

// BenchmarkExtractInstanceInfo 性能测试
func BenchmarkExtractInstanceInfo(b *testing.B) {
	db, err := setupTestDB()
	if err != nil {
		b.Skipf("SQLite driver not available, skipping benchmark")
		return
	}
	defer db.Close()

	// 临时包装 testing.B 为 testing.T 接口用于 setupTestData
	var dummyT testing.T
	setupTestData(&dummyT, db)

	resourceID := int64(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = extractInstanceInfo(db, resourceID)
	}
}

// BenchmarkExtractVolumeInfo 性能测试
func BenchmarkExtractVolumeInfo(b *testing.B) {
	db, err := setupTestDB()
	if err != nil {
		b.Skipf("SQLite driver not available, skipping benchmark")
		return
	}
	defer db.Close()

	// 临时包装 testing.B 为 testing.T 接口用于 setupTestData
	var dummyT testing.T
	setupTestData(&dummyT, db)

	resourceID := int64(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = extractVolumeInfo(db, resourceID)
	}
}
