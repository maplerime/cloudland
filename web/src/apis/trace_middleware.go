/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	rlog "web/src/utils/log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TraceMiddleware reads X-Request-ID from the request header as the Trace ID.
// If absent, generates a new UUID. Injects it into the request context and
// echoes it back via X-Trace-ID response header.
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader("X-Request-ID")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		c.Header("X-Trace-ID", traceID)
		rlog.SetGoroutineTrace(traceID)
		defer rlog.ClearGoroutineTrace()
		c.Next()
	}
}
