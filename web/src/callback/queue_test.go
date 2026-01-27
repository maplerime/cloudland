/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestInitQueue 测试队列初始化
func TestInitQueue(t *testing.T) {
	tests := []struct {
		name      string
		size      int
		wantPanic bool
	}{
		{"Normal size", 100, false},
		{"Small size", 1, false},
		{"Large size", 10000, false},
		{"Zero size", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					if !tt.wantPanic {
						t.Errorf("InitQueue() panicked unexpectedly: %v", r)
					}
				}
			}()

			// 重置队列状态（通过重新初始化）
			var testOnce sync.Once
			testOnce.Do(func() {
				eventQueue = nil
			})
			once = testOnce

			InitQueue(tt.size)

			// 验证队列已初始化
			if eventQueue == nil {
				t.Error("InitQueue() did not initialize eventQueue")
			}
		})
	}
}

// TestPushEvent 测试事件推送
func TestPushEvent(t *testing.T) {
	// 初始化队列
	InitQueue(100)

	tests := []struct {
		name        string
		event       *Event
		wantSuccess bool
	}{
		{
			name: "Valid event",
			event: &Event{
				EventType:  "test_event",
				Source:     "Cloudland",
				OccurredAt: time.Now(),
				Resource: Resource{
					Type: "instance",
					ID:   "test-uuid",
				},
				Data: map[string]interface{}{"status": "running"},
			},
			wantSuccess: true,
		},
		{
			name:        "Nil event",
			event:       nil,
			wantSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 清空队列
			for len(eventQueue) > 0 {
				<-eventQueue
			}

			result := PushEvent(tt.event)
			if result != tt.wantSuccess {
				t.Errorf("PushEvent() = %v, want %v", result, tt.wantSuccess)
			}

			// 如果推送成功，验证事件在队列中
			if tt.wantSuccess && tt.event != nil {
				if len(eventQueue) != 1 {
					t.Errorf("Queue length = %d, want 1", len(eventQueue))
				}
			}
		})
	}
}

// TestPushEventQueueFull 测试队列满时的行为
func TestPushEventQueueFull(t *testing.T) {
	// 初始化小容量队列
	queueSize := 5
	InitQueue(queueSize)

	// 清空队列
	for len(eventQueue) > 0 {
		<-eventQueue
	}

	// 填满队列
	for i := 0; i < queueSize; i++ {
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

	// 队列已满，尝试推送新事件
	newEvent := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "new-uuid",
		},
	}
	result := PushEvent(newEvent)

	// 应该失败（队列已满）
	if result {
		t.Error("PushEvent() on full queue should return false, got true")
	}

	// 队列长度应该还是 queueSize
	if len(eventQueue) != queueSize {
		t.Errorf("Queue length = %d, want %d", len(eventQueue), queueSize)
	}
}

// TestGetEventQueue 测试获取事件队列
func TestGetEventQueue(t *testing.T) {
	InitQueue(100)

	queue := GetEventQueue()
	if queue == nil {
		t.Error("GetEventQueue() returned nil")
	}

	// 验证返回的是只读 channel
	if _, ok := (<-chan *Event)(queue); !ok {
		t.Error("GetEventQueue() should return a read-only channel")
	}
}

// TestGetQueueLength 测试获取队列长度
func TestGetQueueLength(t *testing.T) {
	InitQueue(100)

	// 清空队列
	for len(eventQueue) > 0 {
		<-eventQueue
	}

	// 空队列
	length := GetQueueLength()
	if length != 0 {
		t.Errorf("GetQueueLength() on empty queue = %d, want 0", length)
	}

	// 添加事件
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

	length = GetQueueLength()
	if length != eventCount {
		t.Errorf("GetQueueLength() = %d, want %d", length, eventCount)
	}
}

// TestConcurrentPush 测试并发推送事件
func TestConcurrentPush(t *testing.T) {
	InitQueue(1000)

	// 清空队列
	for len(eventQueue) > 0 {
		<-eventQueue
	}

	numGoroutines := 10
	eventsPerGoroutine := 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// 并发推送事件
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				event := &Event{
					EventType:  "test_event",
					Source:     "Cloudland",
					OccurredAt: time.Now(),
					Resource: Resource{
						Type: "instance",
						ID:   "test-uuid",
					},
					Data: map[string]interface{}{
						"goroutine": id,
						"index":     j,
					},
				}
				PushEvent(event)
			}
		}(i)
	}

	wg.Wait()

	// 验证队列长度
	expectedLength := numGoroutines * eventsPerGoroutine
	length := GetQueueLength()
	if length != expectedLength {
		t.Errorf("Concurrent push: queue length = %d, want %d", length, expectedLength)
	}

	// 验证所有事件都能被正确消费
	count := 0
	for length > 0 {
		<-eventQueue
		count++
		length = GetQueueLength()
	}

	if count != expectedLength {
		t.Errorf("Consumed events = %d, want %d", count, expectedLength)
	}
}

// TestEventQueueIntegration 测试队列与 worker 的集成
func TestEventQueueIntegration(t *testing.T) {
	InitQueue(100)

	// 清空队列
	for len(eventQueue) > 0 {
		<-eventQueue
	}

	// 启动 worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 设置测试配置
	viper.Set("callback.url", "http://localhost:18080/test")
	viper.Set("callback.timeout", 5)
	viper.Set("callback.retry_max", 1)
	viper.Set("callback.retry_interval", 1)

	// 启动测试 HTTP 服务器
	server := startTestServer(t, "localhost:18080")
	defer server.Close()

	// 启动 worker
	go StartWorkers(ctx, 1)

	// 推送事件
	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
		Data: map[string]interface{}{"status": "running"},
	}

	success := PushEvent(event)
	if !success {
		t.Fatal("Failed to push event to queue")
	}

	// 等待事件被处理
	time.Sleep(2 * time.Second)

	// 队列应该为空（事件已被 worker 处理）
	length := GetQueueLength()
	if length != 0 {
		t.Errorf("After worker processing, queue length = %d, want 0", length)
	}
}

// BenchmarkPushEvent 性能测试
func BenchmarkPushEvent(b *testing.B) {
	InitQueue(10000)

	// 预填充事件
	for len(eventQueue) > 0 {
		<-eventQueue
	}

	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PushEvent(event)
	}
}

// BenchmarkConcurrentPushEvent 并发推送性能测试
func BenchmarkConcurrentPushEvent(b *testing.B) {
	InitQueue(10000)

	// 清空队列
	for len(eventQueue) > 0 {
		<-eventQueue
	}

	event := &Event{
		EventType:  "test_event",
		Source:     "Cloudland",
		OccurredAt: time.Now(),
		Resource: Resource{
			Type: "instance",
			ID:   "test-uuid",
		},
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			PushEvent(event)
		}
	})
}

// startTestServer 启动测试 HTTP 服务器
func startTestServer(t *testing.T, addr string) *testServer {
	server := &testServer{
		t:       t,
		stopped: make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/test", server.handleCallback)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}

	server.listener = listener
	server.server = &http.Server{Handler: mux}

	go func() {
		if err := server.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Errorf("Test server error: %v", err)
		}
		close(server.stopped)
	}()

	return server
}

type testServer struct {
	t        *testing.T
	server   *http.Server
	listener net.Listener
	stopped  chan struct{}
}

func (s *testServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *testServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	<-s.stopped
	return nil
}
