/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package log

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// goroutineTraces maps goroutine ID → trace ID.
// HTTP handlers run in dedicated goroutines, so this gives per-request storage
// without any context threading. Child goroutines spawned by a handler do NOT
// inherit the parent's trace ID.
var goroutineTraces sync.Map

// SetGoroutineTrace associates traceID with the current goroutine.
// Call from request middleware; pair with defer ClearGoroutineTrace().
func SetGoroutineTrace(id string) {
	goroutineTraces.Store(goid(), id)
}

// ClearGoroutineTrace removes the trace ID for the current goroutine.
func ClearGoroutineTrace() {
	goroutineTraces.Delete(goid())
}

// CurrentTraceID returns the trace ID for the current goroutine, or "".
func CurrentTraceID() string {
	if v, ok := goroutineTraces.Load(goid()); ok {
		return v.(string)
	}
	return ""
}

// goid returns the current goroutine ID by parsing the runtime stack header.
// The first line is always "goroutine NNN [state]:".
func goid() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	id, _ := strconv.ParseInt(strings.Fields(s)[0], 10, 64)
	return id
}
