package apis

import (
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var checkConnectionAPI = &CheckConnectionAPI{}
var checkConnectionAdminAPI = &routes.CheckConnectionAdmin{}

type CheckConnectionAPI struct{}

type CheckConnectionPayload struct {
	IPs []string `json:"ips" binding:"required,min=1,max=256"`
}

type CheckConnectionResponse struct {
	UUID          string `json:"uuid"`
	IP            string `json:"ip"`
	Status        string `json:"status"`
	DispatchError string `json:"dispatch_error,omitempty"`
	CreatedAt     string `json:"created_at"`
}

func checkConnectionResponse(e *model.CheckConnection) *CheckConnectionResponse {
	return &CheckConnectionResponse{UUID: e.UUID, IP: e.IP, Status: e.Status, DispatchError: e.DispatchError, CreatedAt: e.CreatedAt.Format(TimeStringForMat)}
}

// @Summary Submit IPs for local VM ownership checks on all cloudlets
// @Tags Network
// @Accept json
// @Produce json
// @Param message body CheckConnectionPayload true "IPs to check"
// @Success 202 {object} map[string]interface{}
// @Router /check_connection [post]
func (a *CheckConnectionAPI) Create(c *gin.Context) {
	if !GetMemberShip(c.Request.Context()).CheckPermission(model.Writer) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized", nil)
		return
	}
	payload := &CheckConnectionPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	ips, err := routes.NormalizeCheckConnectionIPs(payload.IPs)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid IP addresses", err)
		return
	}
	entries, err := checkConnectionAdminAPI.Create(c.Request.Context(), ips)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Failed to save or dispatch checks", err)
		return
	}
	response := make([]*CheckConnectionResponse, 0, len(entries))
	for _, entry := range entries {
		response = append(response, checkConnectionResponse(entry))
	}
	c.JSON(http.StatusAccepted, gin.H{"entries": response})
}

func (a *CheckConnectionAPI) List(c *gin.Context) {
	offset, e1 := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
	limit, e2 := strconv.ParseInt(c.DefaultQuery("limit", "50"), 10, 64)
	if e1 != nil || e2 != nil || offset < 0 || limit < 1 || limit > 1000 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid offset or limit (1-1000)", nil)
		return
	}
	total, entries, err := checkConnectionAdminAPI.List(c.Request.Context(), offset, limit)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Failed to list checks", err)
		return
	}
	response := make([]*CheckConnectionResponse, 0, len(entries))
	for _, entry := range entries {
		response = append(response, checkConnectionResponse(entry))
	}
	c.JSON(http.StatusOK, gin.H{"offset": offset, "total": total, "limit": len(entries), "entries": response})
}
