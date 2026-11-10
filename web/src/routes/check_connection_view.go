package routes

import (
	"net/http"
	"strings"
	"unicode"

	. "web/src/common"
	"web/src/model"

	macaron "gopkg.in/macaron.v1"
)

var checkConnectionView = &CheckConnectionView{}

type CheckConnectionView struct{}

func (v *CheckConnectionView) List(c *macaron.Context) {
	config, offset, limit := GetPaginationParams(c, "check_connection")
	total, entries, err := checkConnectionAdmin.List(c.Req.Context(), offset, limit)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "500")
		return
	}
	c.Data["Checks"] = entries
	c.Data["Tab"] = "connections"
	c.Data["CanManageSuspicious"] = GetMemberShip(c.Req.Context()).CheckPermission(model.Admin)
	c.Data["CanWrite"] = GetMemberShip(c.Req.Context()).CheckPermission(model.Admin)
	SetPaginationData(c, "check_connection", total, limit, offset, config,
		`["UUID", "IP", "Status", "DispatchError", "CreatedAt", "Delete"]`,
		[]string{"UUID", "IP", "Status", "DispatchError", "CreatedAt", "Delete"})
	c.HTML(http.StatusOK, "check_connection")
}

func (v *CheckConnectionView) Create(c *macaron.Context) {
	if !GetMemberShip(c.Req.Context()).CheckPermission(model.Admin) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusForbidden, "error")
		return
	}
	ips := strings.FieldsFunc(c.QueryTrim("ips"), func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == ';' })
	if _, err := NormalizeCheckConnectionIPs(ips); err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if _, err := checkConnectionAdmin.Create(c.Req.Context(), ips); err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusInternalServerError, "500")
		return
	}
	c.Redirect("/check_connection")
}

func (v *CheckConnectionView) Delete(c *macaron.Context) {
	if !GetMemberShip(c.Req.Context()).CheckPermission(model.Admin) {
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusForbidden, "error")
		return
	}
	if err := checkConnectionAdmin.Delete(c.Req.Context(), c.Params(":uuid")); err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect("/check_connection")
}
