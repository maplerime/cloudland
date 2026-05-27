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

// InitQueue initializes event queue.
func InitQueue(size int) {
	once.Do(func() {
		eventQueue = make(chan *Event, size)
		logger.Infof("Initialized callback event queue with size: %d", size)
	})
}

// PushEvent pushes an event into queue in non-blocking mode.
// Returns true on success, false if queue is full or event is invalid.
func PushEvent(event *Event) bool {
	// Queue not initialized.
	if eventQueue == nil {
		logger.Warning("Event queue not initialized, skipping event push")
		return false
	}

	// Nil event.
	if event == nil {
		logger.Warning("Nil event provided, skipping event push")
		return false
	}

	// Non-blocking push.
	select {
	case eventQueue <- event:
		logger.Debugf("Event pushed to queue: %s/%s", event.EventType, event.Resource.ID)
		return true
	default:
		// Queue full: warn and drop the event.
		logger.Warningf("Event queue is full, dropping event: %s/%s", event.EventType, event.Resource.ID)
		return false
	}
}

// GetEventQueue returns queue for workers.
func GetEventQueue() <-chan *Event {
	return eventQueue
}

// GetQueueLength returns current queue length for monitoring.
func GetQueueLength() int {
	if eventQueue == nil {
		return 0
	}
	return len(eventQueue)
}
