/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

// setupTestConfig 设置测试配置
func setupTestConfig() {
	viper.Reset()
	viper.Set("callback.enabled", true)
	viper.Set("callback.url", "http://localhost:8080/api/v1/resource-changes")
	viper.Set("callback.workers", 3)
	viper.Set("callback.queue_size", 10000)
	viper.Set("callback.timeout", 30)
	viper.Set("callback.retry_max", 3)
	viper.Set("callback.retry_interval", 5)
}

func TestIsEnabled(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		enabled  bool
		expected bool
	}{
		{"Enabled", true, true},
		{"Disabled", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.enabled", tt.enabled)
			result := IsEnabled()
			if result != tt.expected {
				t.Errorf("IsEnabled() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetCallbackURL(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"Valid URL", "http://localhost:8080/api/v1/resource-changes", "http://localhost:8080/api/v1/resource-changes"},
		{"Empty URL", "", ""},
		{"HTTPS URL", "https://example.com/callback", "https://example.com/callback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.url", tt.url)
			result := GetCallbackURL()
			if result != tt.expected {
				t.Errorf("GetCallbackURL() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetWorkerCount(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		count    int
		expected int
	}{
		{"Positive count", 5, 5},
		{"Zero count", 0, 3}, // 默认值
		{"Negative count", -1, 3}, // 默认值
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.workers", tt.count)
			result := GetWorkerCount()
			if result != tt.expected {
				t.Errorf("GetWorkerCount() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetQueueSize(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		size     int
		expected int
	}{
		{"Positive size", 5000, 5000},
		{"Zero size", 0, 10000}, // 默认值
		{"Negative size", -1, 10000}, // 默认值
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.queue_size", tt.size)
			result := GetQueueSize()
			if result != tt.expected {
				t.Errorf("GetQueueSize() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetTimeout(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		timeout  int
		expected int
	}{
		{"Positive timeout", 60, 60},
		{"Zero timeout", 0, 30}, // 默认值
		{"Negative timeout", -1, 30}, // 默认值
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.timeout", tt.timeout)
			result := GetTimeout()
			if result != tt.expected {
				t.Errorf("GetTimeout() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetRetryMax(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		retry    int
		expected int
	}{
		{"Positive retry", 5, 5},
		{"Zero retry", 0, 0},
		{"Negative retry", -1, 3}, // 默认值
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.retry_max", tt.retry)
			result := GetRetryMax()
			if result != tt.expected {
				t.Errorf("GetRetryMax() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetRetryInterval(t *testing.T) {
	setupTestConfig()

	tests := []struct {
		name     string
		interval int
		expected int
	}{
		{"Positive interval", 10, 10},
		{"Zero interval", 0, 5}, // 默认值
		{"Negative interval", -1, 5}, // 默认值
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("callback.retry_interval", tt.interval)
			result := GetRetryInterval()
			if result != tt.expected {
				t.Errorf("GetRetryInterval() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestLoadConfigFromFile 测试从配置文件加载配置
func TestLoadConfigFromFile(t *testing.T) {
	// 创建临时配置文件
	configContent := `
[callback]
enabled = true
url = "http://test.example.com/callback"
workers = 5
queue_size = 20000
timeout = 60
retry_max = 5
retry_interval = 10
`
	tmpfile, err := os.CreateTemp("", "test-config-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// 设置 viper 读取临时配置文件
	viper.Reset()
	viper.SetConfigFile(tmpfile.Name())
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	// 验证配置值
	tests := []struct {
		name     string
		getFunc  func() interface{}
		expected interface{}
	}{
		{"Enabled", func() interface{} { return IsEnabled() }, true},
		{"URL", func() interface{} { return GetCallbackURL() }, "http://test.example.com/callback"},
		{"Workers", func() interface{} { return GetWorkerCount() }, 5},
		{"QueueSize", func() interface{} { return GetQueueSize() }, 20000},
		{"Timeout", func() interface{} { return GetTimeout() }, 60},
		{"RetryMax", func() interface{} { return GetRetryMax() }, 5},
		{"RetryInterval", func() interface{} { return GetRetryInterval() }, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.getFunc()
			if result != tt.expected {
				t.Errorf("Config value = %v, want %v", result, tt.expected)
			}
		})
	}
}
