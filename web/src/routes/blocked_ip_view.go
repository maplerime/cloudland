/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	. "web/src/common"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var blockedIPView = &BlockedIPView{}

const blockedIPListName = "blocked_ips"

type BlockedIPView struct{}

// ParseTimeInClientZone turns a timestamp submitted by a browser into UTC.
//
// An HTML datetime-local input submits a local time with no zone, so the offset
// the browser reports alongside it is what makes the value unambiguous. Both
// the parsing and the offset handling are the scheduled_task helpers, reused as
// is: they already accept RFC3339 too, which is what API clients send and what
// the pagination links carry.
func ParseTimeInClientZone(raw, tzOffsetMinutes string, fallback time.Time) (ts time.Time, err error) {
	if raw == "" {
		return fallback, nil
	}
	return parseScheduledTaskExecutionTime(raw, parseScheduledTaskTimezoneOffset(tzOffsetMinutes))
}

// List shows the IPs being dropped right now. Nothing is fetched from the
// compute nodes on demand: they export their ipset as metrics on their own
// schedule, so the page is at most one export plus one scrape interval behind.
func (v *BlockedIPView) List(c *macaron.Context, store session.Store) {
	if !v.permitted(c) {
		return
	}
	blocked, window, err := blockedIPAdmin.ListCurrent(v.filter(c))
	budget := &BlockedIPBudgetError{}
	if errors.As(err, &budget) {
		logger.Warningf("Blocked ip current over budget: %v", err)
		c.Data["BudgetExceeded"] = true
		c.Data["BudgetSamples"] = budget.Points
		c.Data["BudgetLimit"] = budget.Budget
		v.render(c, "current", nil, window)
		return
	}
	if err != nil {
		logger.Errorf("Failed to query blocked ips: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	// The range asked for is fixed, but what gets served is not: the size guard
	// narrows this query like any other, and then the rows on screen are a slice
	// of the blocks rather than all of them -- with ownership and direction
	// resolved over that same slice. Reporting it is the whole point of the
	// banner, and this is the tab an operator opens first.
	v.render(c, "current", blocked, window)
}

// History shows blocking episodes inside the selected range. Still-active
// episodes are included, so the two tabs overlap on purpose.
func (v *BlockedIPView) History(c *macaron.Context, store session.Store) {
	if !v.permitted(c) {
		return
	}
	tzOffset := c.QueryTrim("timezone_offset_minutes")
	end, err := ParseTimeInClientZone(c.QueryTrim("end"), tzOffset, time.Now().UTC())
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	start, err := ParseTimeInClientZone(c.QueryTrim("start"), tzOffset, end.Add(-BlockedIPHistoryWindow))
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if !start.Before(end) {
		c.Data["ErrorMsg"] = "start must be before end"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	blocked, window, err := blockedIPAdmin.ListHistory(v.filter(c), start, end)
	budget := &BlockedIPBudgetError{}
	if errors.As(err, &budget) {
		// Staying on the page is the whole point: the remedy is to narrow the
		// range or add a filter, and both controls live here. An error page would
		// take away the only means of following its own advice.
		logger.Warningf("Blocked ip history over budget: %v", err)
		c.Data["BudgetExceeded"] = true
		c.Data["BudgetSamples"] = budget.Points
		c.Data["BudgetLimit"] = budget.Budget
		v.render(c, "history", nil, window)
		return
	}
	if err != nil {
		logger.Errorf("Failed to query blocked ip history: %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	v.render(c, "history", blocked, window)
}

func (v *BlockedIPView) filter(c *macaron.Context) *BlockedIPFilter {
	return &BlockedIPFilter{
		IP:        c.QueryTrim("ip"),
		Hostname:  c.QueryTrim("hostname"),
		BlockType: c.QueryTrim("block_type"),
	}
}

// render pages the result and hands the window back to the form.
//
// Paging happens here rather than in the query because a blocking is derived
// from a whole Prometheus series: the episodes have to be built before they can
// be counted, so there is nothing to push down.
//
// window is nil for the current tab, whose range is fixed.
func (v *BlockedIPView) render(c *macaron.Context, tab string, blocked []*BlockedIP, window *BlockedIPWindow) {
	listConfig, offset, limit := GetPaginationParams(c, blockedIPListName)
	total := int64(len(blocked))
	if offset > total {
		offset = total
	}
	last := offset + limit
	if last > total {
		last = total
	}
	c.Data["BlockedIPs"] = blocked[offset:last]
	c.Data["Tab"] = tab
	c.Data["IP"] = c.QueryTrim("ip")
	c.Data["Hostname"] = c.QueryTrim("hostname")
	// datetime-local renders whatever it is handed, so the resolved window goes
	// out as UTC and the browser turns it into local time -- the same handoff
	// scheduled_tasks_patch.tmpl uses. Feeding back the resolved window rather
	// than the raw input is what makes the default range visible: open the tab
	// with no filter and the fields show the day that is actually being queried.
	//
	// The fields look exactly like what was typed whether or not the search took
	// effect, so the window that was actually queried is also named in the
	// header. Handed over as time.Time so the template can emit both the machine
	// value the browser converts and the UTC text that stands in when it cannot.
	// The pickers and the header show the window that was ASKED for, never the
	// narrowed one. Feeding the narrowed window back was wrong twice over: the
	// default range became invisible the moment a query was too large, and Reset
	// looked broken because it re-derived the same default and got narrowed to
	// the same place. What was actually served belongs in the banner instead.
	if window != nil {
		// Only the history tab feeds the window back into the title and the
		// pickers -- the current tab has no pickers and a fixed range, so a
		// range in its heading would be noise. Narrowing is the other half and
		// belongs to both: it is the difference between "these are the blocks"
		// and "these are some of the blocks".
		if tab == "history" {
			c.Data["StartUTC"] = window.Requested.Format(time.RFC3339)
			c.Data["EndUTC"] = window.End.Format(time.RFC3339)
			c.Data["WindowStart"] = window.Requested
			c.Data["WindowEnd"] = window.End
		}
		// Truncation and over-budget are the same event seen at two depths: the
		// query narrowed, and narrowing found nothing. Only the second is worth
		// saying -- "showing the most recent part" beside an empty table reads as
		// a contradiction, so the template drops this banner when the other one
		// is up.
		if window.Truncated {
			c.Data["WindowTruncated"] = true
			c.Data["WindowServed"] = window.Start
			c.Data["WindowSamples"] = window.Points
		}
	}
	// The current tab does not parse start or end, so its pagination links must
	// not carry a window it would silently ignore.
	pageWindow := window
	if tab != "history" {
		pageWindow = nil
	}
	c.Data["ExtraQuery"] = blockedIPExtraQuery(c, pageWindow)
	SetPaginationData(c, blockedIPListName, total, limit, offset, listConfig,
		`["Hostname", "IP", "Direction", "InstanceUUID", "BlockedAt", "ExpiresAt"]`,
		[]string{"Hostname", "IP", "Direction", "InstanceUUID", "BlockedAt", "ExpiresAt"})
	c.HTML(http.StatusOK, "blocked_ips")
}

// blockedIPExtraQuery carries the filters through the pagination links, which
// would otherwise drop them and silently widen the result on the second page.
//
// The window travels as RFC3339 so it survives the round trip without needing
// the browser offset again: parseScheduledTaskExecutionTime tries RFC3339 first
// and an explicit zone wins over the location it is given.
func blockedIPExtraQuery(c *macaron.Context, window *BlockedIPWindow) template.URL {
	params := url.Values{}
	for _, name := range []string{"ip", "hostname", "block_type"} {
		if value := c.QueryTrim(name); value != "" {
			params.Set(name, value)
		}
	}
	if window != nil {
		// The requested window travels, matching what the pickers show. Narrowing
		// is deterministic for the same data, so every page re-derives the same
		// served window -- carrying the narrowed one instead would make the
		// pickers disagree with the links the moment a page was followed.
		params.Set("start", window.Requested.Format(time.RFC3339))
		params.Set("end", window.End.Format(time.RFC3339))
	}
	if len(params) == 0 {
		return ""
	}
	return template.URL(fmt.Sprintf("&%s", params.Encode()))
}

// permitted keeps both views admin-only: the list spans every tenant's VMs plus
// external attack sources that belong to no tenant at all.
func (v *BlockedIPView) permitted(c *macaron.Context) bool {
	memberShip := GetMemberShip(c.Req.Context())
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return false
	}
	return true
}
