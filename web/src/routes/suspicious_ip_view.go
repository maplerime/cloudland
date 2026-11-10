package routes

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	macaron "gopkg.in/macaron.v1"
)

var suspiciousIPView = &SuspiciousIPView{}

type SuspiciousIPView struct{}

func (v *SuspiciousIPView) List(c *macaron.Context) {
	if !blockedIPView.permitted(c) {
		return
	}
	config, offset, limit := GetPaginationParams(c, "suspicious_ips")
	ip, status := c.QueryTrim("ip"), c.QueryTrim("status")
	total, entries, err := suspiciousIPAdmin.List(c.Req.Context(), offset, limit, ip, status)
	if err != nil {
		v.fail(c, err)
		return
	}
	c.Data["Entries"], c.Data["Tab"] = entries, "suspicious"
	c.Data["IP"], c.Data["Status"] = ip, status
	c.Data["ExtraQuery"] = template.URL("&" + url.Values{"ip": {ip}, "status": {status}}.Encode())
	SetPaginationData(c, "suspicious_ips", total, limit, offset, config,
		`["IP", "Status", "Note", "LastSeenAt", "LastSeenNode", "BlockedAt", "BlockError", "Actions"]`,
		[]string{"IP", "Status", "Note", "LastSeenAt", "LastSeenNode", "BlockedAt", "BlockError", "Actions"})
	c.HTML(http.StatusOK, "suspicious_ips")
}

func (v *SuspiciousIPView) New(c *macaron.Context) {
	if !blockedIPView.permitted(c) {
		return
	}
	c.Data["Tab"] = "suspicious"
	c.HTML(http.StatusOK, "suspicious_ips_edit")
}

func (v *SuspiciousIPView) Edit(c *macaron.Context) {
	if !blockedIPView.permitted(c) {
		return
	}
	entry, err := suspiciousIPAdmin.Get(c.Req.Context(), c.Params(":uuid"))
	if err != nil {
		v.fail(c, err)
		return
	}
	c.Data["Entry"], c.Data["Tab"] = entry, "suspicious"
	c.HTML(http.StatusOK, "suspicious_ips_edit")
}

func (v *SuspiciousIPView) Save(c *macaron.Context) {
	if !blockedIPView.permitted(c) {
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
	c.Redirect("/blocked-ips/suspicious")
}

func (v *SuspiciousIPView) Delete(c *macaron.Context) {
	if !blockedIPView.permitted(c) {
		return
	}
	if err := suspiciousIPAdmin.Delete(c.Req.Context(), c.Params(":uuid")); err != nil {
		v.fail(c, err)
		return
	}
	c.Redirect("/blocked-ips/suspicious")
}

func (v *SuspiciousIPView) Block(c *macaron.Context) {
	if !blockedIPView.permitted(c) {
		return
	}
	entry, err := suspiciousIPAdmin.Get(c.Req.Context(), c.Params(":uuid"))
	if err != nil {
		v.fail(c, err)
		return
	}
	entry, err = suspiciousIPAdmin.Block(c.Req.Context(), entry.IP)
	// Dispatch failures are visible in the list alongside the retained record.
	if err != nil && entry == nil {
		v.fail(c, err)
		return
	}
	c.Redirect("/blocked-ips/suspicious")
}

func (v *SuspiciousIPView) fail(c *macaron.Context, err error) {
	c.Data["ErrorMsg"] = err.Error()
	c.HTML(http.StatusBadRequest, "error")
}
