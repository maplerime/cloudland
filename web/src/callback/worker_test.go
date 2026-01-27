/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// TestSendEvent 测试发送事件
func TestSendEvent(t *testing.T) {
	tests := []struct {
		name           string
		serverStatus   int
		serverResponse string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "Success",
			serverStatus:   http.StatusOK,
			serverResponse: `{"status":"ok"}`,
			wantErr:        false,
		},
		{
			name:           "Created",
			serverStatus:   http.StatusCreated,
			serverResponse: `{"status":"created"}`,
			wantErr:        false,
		},
		{
			name:           "Bad Request",
			serverStatus:   http.StatusBadRequest,
			serverResponse: `{"error":"invalid request"}`,
			wantErr:        true,
			errContains:    "400",
		},
		{
			name:           "Internal Server Error",
			serverStatus:   http.StatusInternalServerError,
			serverResponse: `{"error":"internal error"}`,
			wantErr:        true,
			errContains:    "500",
		},
		{
			name:           "Gateway Timeout",
			serverStatus:   http.StatusGatewayTimeout,
			serverResponse: `{"error":"timeout"}`,
			wantErr:        true,
			errContains:    "504",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试服务器
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// 验证请求方法
				if r.Method != http.MethodPost {
					t.Errorf("Expected POST request, got %s", r.Method)
				}

				// 验证 Content-Type
				contentType := r.Header.Get("Content-Type")
				if contentType != "application/json" {
					t.Errorf("Expected Content-Type application/json, got %s", contentType)
				}

				// 验证 User-Agent
				userAgent := r.Header.Get("User-Agent")
				if userAgent != "CloudLand-Callback/1.0" {
					t.Errorf("Expected User-Agent CloudLand-Callback/1.0, got %s", userAgent)
				}

				// 验证请求体
				var event Event
				if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}

				// 返回响应
				w.WriteHeader(tt.serverStatus)
				w.Write([]byte(tt.serverResponse))
			}))
			defer server.Close()

			// 创建测试事件
			event := &Event{
				EventType:  "test_event",
				Source:     "Cloudland",
				OccurredAt: time.Now(),
				TenantID:   "123",
				Resource: Resource{
					Type: "instance",
					ID:   "test-uuid-123",
					Name: "test-instance",
				},
				Data: map[string]interface{}{
					"status":  "running",
					"cpu":     4,
					"memory":  8192,
				},
				Metadata: map[string]interface{}{
					"region": "us-east-1",
				},
			}

			// 创建 HTTP 客户端
			client := &http.Client{
				Timeout: 5 * time.Second,
			}

			// 发送事件
			err := sendEvent(client, server.URL, event)

			// 验证结果
			if tt.wantErr {
				if err == nil {
					t.Errorf("sendEvent() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("sendEvent() error = %v, want containing %s", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("sendEvent() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestSendEventInvalidJSON 测试无效 JSON 序列化
func TestSendEventInvalidJSON(t *testing.T) {
	// 创建一个会导致 JSON 序列化失败的事件
	event := &Event{
		EventType: "test_event",
		Source:    "Cloudland",
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
		Data: map[string]interface{}{
			// 创建一个无法序列化的值（无效的 channel）
			"invalid": make(chan int),
		},
	}

	client := &http.Client{Timeout: 5 * time.Second}
	err := sendEvent(client, "http://localhost:9999", event)

	if err == nil {
		t.Error("sendEvent() with invalid data expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to marshal event") {
		t.Errorf("Expected marshal error, got: %v", err)
	}
}

// TestSendEventConnectionError 测试连接错误
func TestSendEventConnectionError(t *testing.T) {
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
	}

	client := &http.Client{
		Timeout: 100 * time.Millisecond,
	}

	// 尝试连接到不存在的服务器
	err := sendEvent(client, "http://localhost:9999/nonexistent", event)

	if err == nil {
		t.Error("sendEvent() with invalid URL expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to send request") && !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Expected connection error, got: %v", err)
	}
}

// TestStartWorkers 测试启动 workers
// 标记为长测试，使用 -short 标志跳过
func TestStartWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping worker test in short mode")
	}

	// 设置测试配置
	viper.Set("callback.url", "http://localhost:18081/test")
	viper.Set("callback.timeout", 5)
	viper.Set("callback.retry_max", 1)
	viper.Set("callback.retry_interval", 1)

	// 启动测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// 更新配置使用测试服务器 URL
	viper.Set("callback.url", server.URL)

	// 初始化队列
	InitQueue(10)

	// 启动 workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerCount := 3
	StartWorkers(ctx, workerCount)

	// 推送测试事件
	eventCount := 5
	for i := 0; i < eventCount; i++ {
		event := &Event{
			EventType:  "test_event",
			Source:     "Cloudland",
			OccurredAt: time.Now(),
			Resource: Resource{
				Type: "instance",
				ID:   "test-uuid",
			},
		}
		PushEvent(event)
	}

	// 等待事件被处理（添加超时保护）
	done := make(chan bool)
	go func() {
		time.Sleep(2 * time.Second)
		done <- true
	}()

	select {
	case <-done:
		// 正常完成
	case <-time.After(5 * time.Second):
		t.Error("Test timed out waiting for event processing")
	}

	// 队列应该为空
	length := GetQueueLength()
	if length != 0 {
		t.Errorf("After worker processing, queue length = %d, want 0", length)
	}
}

// TestWorkerRetryLogic 测试重试逻辑
// 标记为长测试，使用 -short 标志跳过
func TestWorkerRetryLogic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping retry test in short mode")
	}

	// 设置测试配置
	viper.Set("callback.retry_max", 2)
	viper.Set("callback.retry_interval", 100) // 100ms

	// 记录请求次数
	requestCount := 0

	// 启动测试服务器（前两次失败，第三次成功）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal error"}`))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer server.Close()

	viper.Set("callback.url", server.URL)
	viper.Set("callback.timeout", 5)

	// 初始化队列
	InitQueue(10)

	// 启动 worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartWorkers(ctx, 1)

	// 推送测试事件
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
	}
	PushEvent(event)

	// 等待重试完成（最多应该重试 2 次 + 1 次初始请求）
	time.Sleep(1 * time.Second)

	// 验证请求次数
	if requestCount != 3 {
		t.Errorf("Expected 3 requests (1 initial + 2 retries), got %d", requestCount)
	}
}

// TestWorkerMaxRetryExceeded 测试超过最大重试次数
// 标记为长测试，使用 -short 标志跳过
func TestWorkerMaxRetryExceeded(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping max retry test in short mode")
	}

	// 设置测试配置
	viper.Set("callback.retry_max", 2)
	viper.Set("callback.retry_interval", 100) // 100ms

	// 启动总是失败的测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()

	viper.Set("callback.url", server.URL)
	viper.Set("callback.timeout", 5)

	// 初始化队列
	InitQueue(10)

	// 启动 worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartWorkers(ctx, 1)

	// 推送测试事件
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
	}
	PushEvent(event)

	// 等待重试完成（添加超时保护）
	done := make(chan bool)
	go func() {
		time.Sleep(1 * time.Second)
		done <- true
	}()

	select {
	case <-done:
		// 正常完成
	case <-time.After(3 * time.Second):
		t.Error("Test timed out waiting for retry completion")
	}

	// 队列应该为空（事件已被丢弃）
	length := GetQueueLength()
	if length != 0 {
		t.Errorf("After max retries, queue length = %d, want 0", length)
	}
}

// TestWorkerContextCancellation 测试 context 取消
// 标记为长测试，使用 -short 标志跳过
func TestWorkerContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping context cancellation test in short mode")
	}

	viper.Set("callback.url", "http://localhost:18082/test")
	viper.Set("callback.timeout", 5)

	// 初始化队列
	InitQueue(10)

	// 启动 worker
	ctx, cancel := context.WithCancel(context.Background())

	go StartWorkers(ctx, 1)

	// 推送一些事件
	for i := 0; i < 5; i++ {
		event := &Event{
			EventType:  "test_event",
			Source:     "Cloudland",
			OccurredAt: time.Now(),
			Resource: Resource{
				Type: "instance",
				ID:   "test-uuid",
			},
		}
		PushEvent(event)
	}

	// 取消 context
	cancel()

	// 等待 worker 停止（添加超时保护）
	done := make(chan bool)
	go func() {
		time.Sleep(500 * time.Millisecond)
		done <- true
	}()

	select {
	case <-done:
		// 正常完成
	case <-time.After(2 * time.Second):
		t.Error("Test timed out waiting for worker to stop")
	}

	// 验证队列中还有未处理的事件
	length := GetQueueLength()
	if length == 0 {
		t.Error("Expected some events to remain in queue after context cancellation")
	}
}

// TestWorkerEmptyEvent 测试处理空事件
// 标记为长测试，使用 -short 标志跳过
func TestWorkerEmptyEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping empty event test in short mode")
	}

	viper.Set("callback.url", "http://localhost:18083/test")
	viper.Set("callback.timeout", 5)

	// 初始化队列
	InitQueue(10)

	// 启动 worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	viper.Set("callback.url", server.URL)

	go StartWorkers(ctx, 1)

	// 推送 nil 事件
	eventQueue <- nil

	// 等待 worker 处理（添加超时保护）
	done := make(chan bool)
	go func() {
		time.Sleep(500 * time.Millisecond)
		done <- true
	}()

	select {
	case <-done:
		// 正常完成
	case <-time.After(2 * time.Second):
		t.Error("Test timed out waiting for worker to process")
	}

	// 不应该 panic，测试通过
}

// BenchmarkSendEvent 性能测试
func BenchmarkSendEvent(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
		Data: map[string]interface{}{
			"status": "running",
			"cpu":     4,
			"memory":  8192,
		},
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sendEvent(client, server.URL, event)
	}
}
