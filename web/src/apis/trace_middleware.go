/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	rlog "web/src/utils/log"

	"github.com/gin-gonic/gin"
)

// TraceMiddleware resolves the trace ID for the request:
//  1. X-Trace-ID header (caller already has a trace ID)
//  2. X-Request-ID header (use as trace ID)
//  3. Generate a short ID: "TRC-" + 8 hex chars
//
// Echoes the resolved ID back via X-Trace-ID response header.
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader("X-Trace-ID")
		if traceID == "" {
			traceID = c.GetHeader("X-Request-ID")
		}
		if traceID == "" {
			traceID = rlog.NewTraceID()
		}
		c.Header("X-Trace-ID", traceID)
		rlog.SetGoroutineTrace(traceID)
		defer rlog.ClearGoroutineTrace()
		c.Next()
	}
}
