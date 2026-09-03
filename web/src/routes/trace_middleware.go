/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	rlog "web/src/utils/log"

	"github.com/google/uuid"
	"gopkg.in/macaron.v1"
)

// TraceMiddleware reads X-Request-ID from the request header as the Trace ID.
// If absent, generates a new UUID. Injects it into the request context and
// echoes it back via X-Trace-ID response header.
func TraceMiddleware() macaron.Handler {
	return func(c *macaron.Context) {
		traceID := c.Req.Header.Get("X-Request-ID")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		c.Resp.Header().Set("X-Trace-ID", traceID)
		rlog.SetGoroutineTrace(traceID)
		defer rlog.ClearGoroutineTrace()
		c.Next()
	}
}
