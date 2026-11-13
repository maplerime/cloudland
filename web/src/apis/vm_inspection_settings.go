package apis

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"web/src/common"
	"web/src/model"
	"web/src/routes"
)

type VMInspectionSettingsAPI struct{}

var vmInspectionSettingsAPI = &VMInspectionSettingsAPI{}

func vmInspectionAPIError(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	var cl *common.CLError
	if errors.As(err, &cl) {
		switch cl.Code {
		case common.ErrPermissionDenied:
			code = http.StatusForbidden
		case common.ErrInvalidParameter:
			code = http.StatusBadRequest
		}
	}
	if gorm.IsRecordNotFoundError(err) {
		code = http.StatusNotFound
	}
	if strings.HasPrefix(err.Error(), "conflict:") {
		code = http.StatusConflict
	}
	if code == http.StatusInternalServerError {
		logger.Errorf("[VM-INSPECTION] API failed: %v", err)
	}
	common.ErrorResponse(c, code, "VM inspection operation failed", err)
}
func decodeVMInspectionJSON(c *gin.Context, out interface{}) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		common.ErrorResponse(c, http.StatusBadRequest, "Invalid JSON request", err)
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		common.ErrorResponse(c, http.StatusBadRequest, "Expected one JSON object", err)
		return false
	}
	return true
}

// @Summary Get global and compute-node VM inspection settings
// @Tags VM Inspection
// @Produce json
// @Success 200 {object} routes.VMInspectionSettingsResponse
// @Failure 403 {object} common.APIError
// @Router /vm-inspection/settings [get]
func (*VMInspectionSettingsAPI) Get(c *gin.Context) {
	out, err := routes.ListVMInspectionSettings(c.Request.Context())
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// @Summary Batch update global and compute-node VM inspection settings
// @Tags VM Inspection
// @Accept json
// @Produce json
// @Param body body routes.VMInspectionSettingsPatch true "Global settings, node groups and nodes to reset"
// @Success 200 {object} routes.VMInspectionSettingsResponse
// @Failure 400 {object} common.APIError
// @Failure 403 {object} common.APIError
// @Router /vm-inspection/settings [patch]
func (*VMInspectionSettingsAPI) Update(c *gin.Context) {
	var p routes.VMInspectionSettingsPatch
	if !decodeVMInspectionJSON(c, &p) {
		return
	}
	out, err := routes.UpdateVMInspectionSettings(c.Request.Context(), p)
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

type VMInspectionPinWhitelistPayload struct {
	InstanceUUID string `json:"instance_uuid"`
	Reason       string `json:"reason"`
}

type VMInspectionPinWhitelistResponse struct {
	UUID         string    `json:"uuid"`
	InstanceUUID string    `json:"instance_uuid"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type VMInspectionPinWhitelistListResponse struct {
	Offset  int                                `json:"offset"`
	Limit   int                                `json:"limit"`
	Total   int                                `json:"total"`
	Entries []VMInspectionPinWhitelistResponse `json:"entries"`
}

func convertVMInspectionPinWhitelist(row model.VMInspectionPinWhitelist) VMInspectionPinWhitelistResponse {
	return VMInspectionPinWhitelistResponse{
		UUID: row.UUID, InstanceUUID: row.InstanceUUID, Reason: row.Reason,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func (*VMInspectionSettingsAPI) CreatePinWhitelist(c *gin.Context) {
	var input VMInspectionPinWhitelistPayload
	if !decodeVMInspectionJSON(c, &input) {
		return
	}
	entry, err := routes.CreateVMInspectionPinWhitelist(c.Request.Context(), input.InstanceUUID, input.Reason)
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusCreated, convertVMInspectionPinWhitelist(*entry))
}

func (*VMInspectionSettingsAPI) ListPinWhitelists(c *gin.Context) {
	offset, limit := 0, 16
	for key, target := range map[string]*int{"offset": &offset, "limit": &limit} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || key == "limit" && (value < 1 || value > 200) {
				common.ErrorResponse(c, http.StatusBadRequest, "Invalid pagination", fmt.Errorf("invalid %s", key))
				return
			}
			*target = value
		}
	}
	rows, total, err := routes.ListVMInspectionPinWhitelists(c.Request.Context(), c.Query("instance_uuid"), offset, limit)
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	out := VMInspectionPinWhitelistListResponse{Offset: offset, Limit: limit, Total: total, Entries: []VMInspectionPinWhitelistResponse{}}
	for _, row := range rows {
		out.Entries = append(out.Entries, convertVMInspectionPinWhitelist(row))
	}
	c.JSON(http.StatusOK, out)
}

func (*VMInspectionSettingsAPI) GetPinWhitelist(c *gin.Context) {
	entry, err := routes.GetVMInspectionPinWhitelist(c.Request.Context(), c.Param("uuid"))
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, convertVMInspectionPinWhitelist(*entry))
}

func (*VMInspectionSettingsAPI) DeletePinWhitelist(c *gin.Context) {
	if err := routes.DeleteVMInspectionPinWhitelist(c.Request.Context(), c.Param("uuid")); err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
