package apis

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	. "web/src/common"
	"web/src/model"
	"web/src/routes"
)

var suspiciousIPAPI = &SuspiciousIPAPI{}
var suspiciousIPAdminAPI = &routes.SuspiciousIPAdmin{}

type SuspiciousIPAPI struct{}

func suspiciousIPAPIPermitted(c *gin.Context) bool {
	if !GetMemberShip(c.Request.Context()).CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return false
	}
	return true
}

func suspiciousIPAPIPagination(c *gin.Context) (offset, limit int, ok bool) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid offset", err)
		return
	}
	limit, err = strconv.Atoi(c.DefaultQuery("limit", "1000"))
	if err != nil || limit < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid limit", err)
		return
	}
	if limit == 0 || limit > 10000 {
		limit = 10000
	}
	return offset, limit, true
}

type SuspiciousIPPayload struct {
	IP   string `json:"ip" binding:"required,ip"`
	Note string `json:"note" binding:"max=4096"`
}

type SuspiciousIPPatchPayload struct {
	IP   *string `json:"ip" binding:"omitempty,ip"`
	Note *string `json:"note" binding:"omitempty,max=4096"`
}

type SuspiciousIPResponse struct {
	UUID         string     `json:"uuid"`
	IP           string     `json:"ip"`
	Note         string     `json:"note"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	LastSeenNode *int32     `json:"last_seen_node"`
	BlockedAt    *time.Time `json:"blocked_at"`
	BlockError   string     `json:"block_error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func suspiciousIPResponse(e *model.SuspiciousIP) *SuspiciousIPResponse {
	return &SuspiciousIPResponse{UUID: e.UUID, IP: e.IP, Note: e.Note, LastSeenAt: e.LastSeenAt,
		LastSeenNode: e.LastSeenNode, BlockedAt: e.BlockedAt, BlockError: e.BlockError, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt}
}

func suspiciousIPError(c *gin.Context, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, routes.ErrSuspiciousIPPermission) {
		code = http.StatusForbidden
	}
	if gorm.IsRecordNotFoundError(err) {
		code = http.StatusNotFound
	}
	ErrorResponse(c, code, "Suspicious IP operation failed", err)
}

func (a *SuspiciousIPAPI) List(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	offset, limit, ok := suspiciousIPAPIPagination(c)
	if !ok {
		return
	}
	total, entries, err := suspiciousIPAdminAPI.List(c.Request.Context(), int64(offset), int64(limit), c.Query("ip"))
	if err != nil {
		suspiciousIPError(c, err)
		return
	}
	response := make([]*SuspiciousIPResponse, 0, len(entries))
	for _, entry := range entries {
		response = append(response, suspiciousIPResponse(entry))
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "offset": offset, "limit": len(entries), "entries": response})
}

func (a *SuspiciousIPAPI) Get(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	entry, err := suspiciousIPAdminAPI.Get(c.Request.Context(), c.Param("uuid"))
	if err != nil {
		suspiciousIPError(c, err)
		return
	}
	c.JSON(http.StatusOK, suspiciousIPResponse(entry))
}

func (a *SuspiciousIPAPI) Create(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	payload := &SuspiciousIPPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		suspiciousIPError(c, err)
		return
	}
	entry, err := suspiciousIPAdminAPI.Create(c.Request.Context(), payload.IP, payload.Note)
	if err != nil {
		suspiciousIPError(c, err)
		return
	}
	c.JSON(http.StatusCreated, suspiciousIPResponse(entry))
}

func (a *SuspiciousIPAPI) Patch(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	payload := &SuspiciousIPPatchPayload{}
	if err := c.ShouldBindJSON(payload); err != nil {
		suspiciousIPError(c, err)
		return
	}
	entry, err := suspiciousIPAdminAPI.Get(c.Request.Context(), c.Param("uuid"))
	if err != nil {
		suspiciousIPError(c, err)
		return
	}
	if payload.IP != nil {
		entry.IP = *payload.IP
	}
	if payload.Note != nil {
		entry.Note = *payload.Note
	}
	entry, err = suspiciousIPAdminAPI.Update(c.Request.Context(), entry.UUID, entry.IP, entry.Note)
	if err != nil {
		suspiciousIPError(c, err)
		return
	}
	c.JSON(http.StatusOK, suspiciousIPResponse(entry))
}

func (a *SuspiciousIPAPI) Delete(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	if err := suspiciousIPAdminAPI.Delete(c.Request.Context(), c.Param("uuid")); err != nil {
		suspiciousIPError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Block accepts a raw IP even when it has not yet been observed.
func (a *SuspiciousIPAPI) Block(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	payload := &struct {
		IP string `json:"ip" binding:"required,ip"`
	}{}
	if err := c.ShouldBindJSON(payload); err != nil {
		suspiciousIPError(c, err)
		return
	}
	a.block(c, payload.IP)
}

func (a *SuspiciousIPAPI) BlockRecord(c *gin.Context) {
	if !suspiciousIPAPIPermitted(c) {
		return
	}
	entry, err := suspiciousIPAdminAPI.Get(c.Request.Context(), c.Param("uuid"))
	if err != nil {
		suspiciousIPError(c, err)
		return
	}
	a.block(c, entry.IP)
}

func (a *SuspiciousIPAPI) block(c *gin.Context, ip string) {
	entry, err := suspiciousIPAdminAPI.Block(c.Request.Context(), ip)
	if err != nil {
		if entry != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "entry": suspiciousIPResponse(entry)})
		} else {
			suspiciousIPError(c, err)
		}
		return
	}
	c.JSON(http.StatusAccepted, suspiciousIPResponse(entry))
}
