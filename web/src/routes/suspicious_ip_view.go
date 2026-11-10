package routes

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"

	. "web/src/common"
	"web/src/model"

	macaron "gopkg.in/macaron.v1"
)

var suspiciousIPView = &SuspiciousIPView{}

type SuspiciousIPView struct{}

func (v *SuspiciousIPView) List(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	config, offset, limit := GetPaginationParams(c, "suspicious_ips")
	ip := c.QueryTrim("ip")
	total, entries, err := suspiciousIPAdmin.List(c.Req.Context(), offset, limit, ip)
	if err != nil {
		v.fail(c, err)
		return
	}
	c.Data["Entries"], c.Data["IP"] = entries, ip
	c.Data["Tab"], c.Data["CanManageSuspicious"] = "suspicious", true
	c.Data["ExtraQuery"] = template.URL("&" + url.Values{"ip": {ip}}.Encode())
	SetPaginationData(c, "suspicious_ips", total, limit, offset, config,
		`["IP", "Note", "LastSeenAt", "LastSeenNode", "BlockedAt", "BlockError", "Actions"]`,
		[]string{"IP", "Note", "LastSeenAt", "LastSeenNode", "BlockedAt", "BlockError", "Actions"})
	c.HTML(http.StatusOK, "check_connection")
}

func (v *SuspiciousIPView) permitted(c *macaron.Context) bool {
	if !GetMemberShip(c.Req.Context()).CheckPermission(model.Admin) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusForbidden, "error")
		return false
	}
	return true
}

func (v *SuspiciousIPView) New(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	c.Data["Tab"], c.Data["CanManageSuspicious"] = "suspicious", true
	c.HTML(http.StatusOK, "suspicious_ips_edit")
}

func (v *SuspiciousIPView) Edit(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	entry, err := suspiciousIPAdmin.Get(c.Req.Context(), c.Params(":uuid"))
	if err != nil {
		v.fail(c, err)
		return
	}
	c.Data["Entry"] = entry
	c.Data["Tab"], c.Data["CanManageSuspicious"] = "suspicious", true
	c.HTML(http.StatusOK, "suspicious_ips_edit")
}

func (v *SuspiciousIPView) Save(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	ip, note := c.QueryTrim("ip"), c.QueryTrim("note")
	if len(note) > 4096 {
		v.fail(c, fmt.Errorf("note must be at most 4096 bytes"))
		return
	}
	var err error
	if id := c.Params(":uuid"); id != "" {
		_, err = suspiciousIPAdmin.Update(c.Req.Context(), id, ip, note)
	} else {
		_, err = suspiciousIPAdmin.Create(c.Req.Context(), ip, note)
	}
	if err != nil {
		v.fail(c, err)
		return
	}
	c.Redirect("/check_connection/suspicious")
}

func (v *SuspiciousIPView) Delete(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	if err := suspiciousIPAdmin.Delete(c.Req.Context(), c.Params(":uuid")); err != nil {
		v.fail(c, err)
		return
	}
	c.Redirect("/check_connection/suspicious")
}

func (v *SuspiciousIPView) Block(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	var timeout *int64
	if value := c.QueryTrim("timeout"); value != "" {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			v.fail(c, err)
			return
		}
		timeout = &seconds
		if err := ValidateIPBlockTimeout(timeout); err != nil {
			v.fail(c, err)
			return
		}
	}
	entry, err := suspiciousIPAdmin.Get(c.Req.Context(), c.Params(":uuid"))
	if err != nil {
		v.fail(c, err)
		return
	}
	entry, err = suspiciousIPAdmin.Block(c.Req.Context(), entry.IP, timeout)
	// Dispatch failures are visible in the list alongside the retained record.
	if err != nil && entry == nil {
		v.fail(c, err)
		return
	}
	c.Redirect("/check_connection/suspicious")
}

func (v *SuspiciousIPView) Unblock(c *macaron.Context) {
	if !v.permitted(c) {
		return
	}
	entry, err := suspiciousIPAdmin.Get(c.Req.Context(), c.Params(":uuid"))
	if err != nil {
		v.fail(c, err)
		return
	}
	if err := suspiciousIPAdmin.Unblock(c.Req.Context(), entry.IP); err != nil {
		v.fail(c, err)
		return
	}
	c.Redirect("/check_connection/suspicious")
}

func (v *SuspiciousIPView) fail(c *macaron.Context, err error) {
	c.Data["ErrorMsg"] = err.Error()
	c.HTML(http.StatusBadRequest, "error")
}
