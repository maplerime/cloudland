/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

import (
	"sync"
)

var (
	eventQueue chan *Event
	once       sync.Once
)

// InitQueue 初始化队列
func InitQueue(size int) {
	once.Do(func() {
		eventQueue = make(chan *Event, size)
		logger.Infof("Initialized callback event queue with size: %d", size)
	})
}

// PushEvent 推送事件到队列 (非阻塞)
// 返回 true 表示成功推送，false 表示队列已满或事件无效
func PushEvent(event *Event) bool {
	// 队列未初始化
	if eventQueue == nil {
		logger.Warning("Event queue not initialized, skipping event push")
		return false
	}

	// 事件为 nil
	if event == nil {
		logger.Warning("Nil event provided, skipping event push")
		return false
	}

	// 非阻塞推送
	select {
	case eventQueue <- event:
		logger.Debugf("Event pushed to queue: %s/%s -> %s",
			event.EventType, event.Resource.ID, event.Data["status"])
		return true
	default:
		// 队列满了，记录警告并丢弃事件
		logger.Warningf("Event queue is full, dropping event: %s/%s",
			event.EventType, event.Resource.ID)
		return false
	}
}

// GetEventQueue 获取队列 (供 worker 使用)
func GetEventQueue() <-chan *Event {
	return eventQueue
}

// GetQueueLength 获取当前队列长度 (用于监控)
func GetQueueLength() int {
	if eventQueue == nil {
		return 0
	}
	return len(eventQueue)
}
