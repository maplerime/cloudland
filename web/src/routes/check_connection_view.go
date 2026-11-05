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
	c.Data["CanWrite"] = GetMemberShip(c.Req.Context()).CheckPermission(model.Writer)
	SetPaginationData(c, "check_connection", total, limit, offset, config,
		`["UUID", "IP", "Status", "DispatchError", "CreatedAt"]`,
		[]string{"UUID", "IP", "Status", "DispatchError", "CreatedAt"})
	c.HTML(http.StatusOK, "check_connection")
}

func (v *CheckConnectionView) Create(c *macaron.Context) {
	if !GetMemberShip(c.Req.Context()).CheckPermission(model.Writer) {
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
