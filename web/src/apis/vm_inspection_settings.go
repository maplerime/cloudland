package apis

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"io"
	"net/http"
	"strings"
	"web/src/common"
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
