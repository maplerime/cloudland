/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var blockedIPAPI = &BlockedIPAPI{}
var blockedIPAdminAPI = &routes.BlockedIPAdmin{}

const (
	blockedIPDefaultLimit = 1000
	blockedIPMaxLimit     = 10000
)

type BlockedIPAPI struct{}

// BlockedIPResponse is one blocking of one IP on one compute node.
type BlockedIPResponse struct {
	// The blocked address.
	IP string `json:"ip" example:"137.184.24.227"`
	// Which side of the half-open connection was blocked: src for the SYN
	// source, dst for its destination. unknown is defensive only -- the exporter
	// always labels the set it read, so it does not occur in practice.
	BlockType string `json:"block_type" example:"src"`
	// Which side of the flood this address was on, derived from block_type plus
	// whether it resolved to one of our instances. One incident always yields
	// two rows, the attacker and its target. One of: vm_compromised (our VM is
	// flooding outward), target_under_attack (an address being flooded that we
	// cannot attribute), external_attacker (an address flooding us that is no
	// running VM of ours), vm_under_attack (our VM being flooded), unknown.
	Direction string `json:"direction" example:"external_attacker"`
	// Compute node that installed the block.
	Hostname string `json:"hostname" example:"sv6-cland-compute-0"`
	// Instance UUID owning the address, or NA when no instance holds it. NA
	// beside ours=true is normal rather than a gap: a reserved address, a
	// detached floating IP and a load balancer VIP are all ours and hold no VM.
	InstanceID string `json:"instance_id" example:"NA"`
	// Load balancer holding the address, non-empty only where instance_id is NA.
	// The only thing on such a row that points anywhere: there is no VM behind
	// the address, and this names the load balancer that is behind it instead.
	LbID string `json:"lb_id" example:""`
	// Whether the address belongs to this region at all, which is what decides
	// direction. Independent of instance_id for the reason above.
	Ours bool `json:"ours" example:"false"`
	// Which lookup answered: metric, or none for an external address.
	Source string `json:"source" example:"none"`
	// How far the mapping could be trusted. One of:
	//
	//   ""             resolved cleanly, or cleanly found not to be ours
	//   conflict       the address maps to more than one instance, so instance_id
	//                  names one candidate among several and must not be acted on
	//   unavailable    the mapping was not being published across the window, so
	//                  neither the hit nor the miss means anything and direction
	//                  is reported as unknown
	OwnerState string `json:"owner_state" example:""`
	// When the block started. Never earlier than the real block, only later: the
	// collection delay adds up to 60s and the sample grid up to one step. The
	// current list is on a fixed 30s grid; the history grid scales with the
	// window, 30s on a narrow one up to 300s on a month, so the wider the window
	// the coarser this is. Not a to-the-second timestamp.
	BlockedAt string `json:"blocked_at" example:"2026-08-26 07:12:31.000000"`
	// Derived as blocked_at plus the one hour ipset timeout.
	ExpiresAt string `json:"expires_at" example:"2026-08-26 08:12:31.000000"`
}

// BlockedIPListResponse is a page of blockings.
type BlockedIPListResponse struct {
	// Offset actually applied, clamped to the result size.
	Offset int `json:"offset"`
	// Number of blockings matching the query, before paging.
	//
	// These are blocking episodes rather than samples, and how finely they
	// separate depends on the window: two blocks of one address merge into one
	// episode when they sit closer together than twice the sample step, and that
	// step grows with the window. The same data therefore yields fewer episodes
	// over a month than over an hour, so this is not an absolute count of times
	// an address was blocked.
	Total int `json:"total"`
	// Number of entries in this page.
	Limit int `json:"limit"`
	// The page itself, newest blocking first.
	Entries []*BlockedIPResponse `json:"entries"`
	// Start of the window actually queried, UTC. Equal to what was requested
	// unless truncated is set.
	WindowStart string `json:"window_start" example:"2026-08-26 07:12:31.000000"`
	// End of the window actually queried, UTC.
	WindowEnd string `json:"window_end" example:"2026-08-27 07:12:31.000000"`
	// Set when the requested window held more data than one query may move, so
	// window_start was moved forward to the most recent slice that fits. The
	// rows returned are complete and correctly counted for the window reported
	// here -- they are simply not the whole window that was asked for. Narrow
	// the range or add a filter to see the rest.
	Truncated bool `json:"truncated" example:"false"`
	// The start originally asked for, present only when truncated is set.
	RequestedStart string `json:"requested_start,omitempty"`
	// Raw samples behind the window as requested, present only when truncated is
	// set. This is the figure that overflowed, so comparing it against how much
	// of the range was actually served says how much further to narrow, or how
	// much a filter would have to cut.
	MeasuredSamples int64 `json:"measured_samples,omitempty"`
}

// List returns the IPs being dropped right now across the region.
//
// @Summary      List currently blocked IPs
// @Description  Returns every IP the region is dropping right now, resolved to the
// @Description  instance owning it when the address is ours. Data comes from the
// @Description  ipset exported by each compute node as Prometheus metrics, so an
// @Description  address appears within about 90 seconds of being blocked.
// @Description
// @Description  The window here is fixed and bounded by the detection thresholds, so it
// @Description  is not normally large enough to be narrowed; if it ever is, truncated
// @Description  says so and the only remedy is a filter. Admin only.
// @tags Network
// @Accept       json
// @Produce      json
// @Param        ip          query string false "Exact address to filter by"
// @Param        hostname    query string false "Compute node to filter by"
// @Param        block_type  query string false "Filter by blocked side" Enums(src, dst, unknown)
// @Param        offset      query int    false "Rows to skip (default 0)"
// @Param        limit       query int    false "Page size (default 1000; anything larger is clamped to 10000)"
// @Success      200 {object} BlockedIPListResponse
// @Failure      400 {object} common.APIError "Invalid offset or limit, or the window holds more than one query may carry and no part of it that fits holds any blocking"
// @Failure      403 {object} common.APIError "Not authorized"
// @Failure      500 {object} common.APIError "Prometheus unreachable"
// @Router       /metrics/blocked-ips [get]
func (a *BlockedIPAPI) List(c *gin.Context) {
	if !blockedIPPermitted(c) {
		return
	}
	offset, limit, ok := blockedIPPagination(c)
	if !ok {
		return
	}
	blocked, window, err := blockedIPAdminAPI.ListCurrent(blockedIPFilter(c))
	budget := &routes.BlockedIPBudgetError{}
	if errors.As(err, &budget) {
		// The caller can act on this: narrow the range, add a filter. That makes
		// it a bad request, not a server fault.
		logger.Warningf("Query over budget: %v", err)
		ErrorResponse(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err != nil {
		logger.Errorf("Failed to query blocked ips: %v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to query blocked ips", err)
		return
	}
	c.JSON(http.StatusOK, blockedIPListResponse(blocked, window, offset, limit))
}

// History returns blocking episodes inside a time window. Episodes still active
// are included on purpose, so results overlap with List.
//
// @Summary      List historical IP blockings
// @Description  Returns blockings that started inside [start, end], including the ones
// @Description  still in force, so results overlap with the current list on purpose.
// @Description  Ownership is resolved as of the time of the block, which stays correct
// @Description  after a VM is deleted or an address reused. An address with no traffic
// @Description  metrics anywhere in the window is looked up again over the whole
// @Description  retention period, so a VM that was powered off while it was blocked
// @Description  still resolves -- owner_state says when that happened. The window
// @Description  itself cannot reach past the 30 day Prometheus retention.
// @Description
// @Description  Before any data is moved the window is measured, and if it holds more
// @Description  than one query may carry it is narrowed to the most recent slice that
// @Description  fits: truncated then says so and window_start reports what was served.
// @Description  Admin only.
// @tags Network
// @Accept       json
// @Produce      json
// @Param        start                   query string false "Window start, RFC3339 or 2006-01-02T15:04 (default: 24h before end)"
// @Param        end                     query string false "Window end, same formats (default: now)"
// @Param        timezone_offset_minutes query int    false "Minutes behind UTC, as JavaScript getTimezoneOffset() reports it; needed only when start/end carry no zone"
// @Param        ip                      query string false "Exact address to filter by"
// @Param        hostname                query string false "Compute node to filter by"
// @Param        block_type              query string false "Filter by blocked side" Enums(src, dst, unknown)
// @Param        offset                  query int    false "Rows to skip (default 0)"
// @Param        limit                   query int    false "Page size (default 1000; anything larger is clamped to 10000)"
// @Success      200 {object} BlockedIPListResponse
// @Failure      400 {object} common.APIError "Invalid time range, offset or limit, or the range holds more than one query may carry and the most recent part of it that fits holds no blocking"
// @Failure      403 {object} common.APIError "Not authorized"
// @Failure      500 {object} common.APIError "Prometheus unreachable"
// @Router       /metrics/blocked-ips/history [get]
func (a *BlockedIPAPI) History(c *gin.Context) {
	if !blockedIPPermitted(c) {
		return
	}
	offset, limit, ok := blockedIPPagination(c)
	if !ok {
		return
	}
	tzOffset := c.Query("timezone_offset_minutes")
	end, err := routes.ParseTimeInClientZone(c.Query("end"), tzOffset, time.Now().UTC())
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid end", err)
		return
	}
	start, err := routes.ParseTimeInClientZone(c.Query("start"), tzOffset, end.Add(-routes.BlockedIPHistoryWindow))
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid start", err)
		return
	}
	if !start.Before(end) {
		ErrorResponse(c, http.StatusBadRequest, "start must be before end", nil)
		return
	}
	blocked, window, err := blockedIPAdminAPI.ListHistory(blockedIPFilter(c), start, end)
	budget := &routes.BlockedIPBudgetError{}
	if errors.As(err, &budget) {
		// The caller can act on this: narrow the range, add a filter. That makes
		// it a bad request, not a server fault.
		logger.Warningf("Query over budget: %v", err)
		ErrorResponse(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err != nil {
		logger.Errorf("Failed to query blocked ip history: %v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to query blocked ip history", err)
		return
	}
	c.JSON(http.StatusOK, blockedIPListResponse(blocked, window, offset, limit))
}

func blockedIPFilter(c *gin.Context) *routes.BlockedIPFilter {
	return &routes.BlockedIPFilter{
		IP:        c.Query("ip"),
		Hostname:  c.Query("hostname"),
		BlockType: c.Query("block_type"),
	}
}

func blockedIPPermitted(c *gin.Context) bool {
	memberShip := GetMemberShip(c.Request.Context())
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return false
	}
	return true
}

func blockedIPPagination(c *gin.Context) (offset, limit int, ok bool) {
	// Atoi rather than ParseInt: it yields an int directly, so the slice bounds
	// in blockedIPListResponse never need a conversion, and an offset too large
	// for this platform's int is refused at the door instead of wrapping.
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid offset", err)
		return
	}
	limit, err = strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(blockedIPDefaultLimit)))
	if err != nil || limit < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid limit", err)
		return
	}
	if limit == 0 || limit > blockedIPMaxLimit {
		limit = blockedIPMaxLimit
	}
	return offset, limit, true
}

// blockedIPListResponse paginates in memory: the whole result set comes back
// from one PromQL query, and it is bounded by how many IPs the region actually
// blocks (about two thousand distinct addresses per month).
func blockedIPListResponse(blocked []*routes.BlockedIP, window *routes.BlockedIPWindow, offset, limit int) *BlockedIPListResponse {
	total := len(blocked)
	from := offset
	if from > total {
		from = total
	}
	to := from + limit
	if to > total {
		to = total
	}
	page := blocked[from:to]

	resp := &BlockedIPListResponse{
		Offset:  from,
		Total:   total,
		Limit:   len(page),
		Entries: make([]*BlockedIPResponse, 0, len(page)),
	}
	if window != nil {
		resp.WindowStart = window.Start.Format(TimeStringForMat)
		resp.WindowEnd = window.End.Format(TimeStringForMat)
		resp.Truncated = window.Truncated
		if window.Truncated {
			resp.RequestedStart = window.Requested.Format(TimeStringForMat)
			resp.MeasuredSamples = window.Points
		}
	}
	for _, entry := range page {
		// An empty instance means the IP belongs to no VM of ours, which is the
		// normal outcome for an attack source.
		instanceID := entry.InstanceUUID
		if instanceID == "" {
			instanceID = "NA"
		}
		resp.Entries = append(resp.Entries, &BlockedIPResponse{
			IP:         entry.IP,
			BlockType:  entry.BlockType,
			Direction:  entry.Direction,
			Hostname:   entry.Hostname,
			InstanceID: instanceID,
			LbID:       entry.LbID,
			Ours:       entry.Ours,
			Source:     entry.Source,
			OwnerState: entry.OwnerState,
			BlockedAt:  entry.BlockedAt.Format(TimeStringForMat),
			ExpiresAt:  entry.ExpiresAt.Format(TimeStringForMat),
		})
	}
	return resp
}
