package apis

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"web/src/routes"
)

type VMIsolationAPI struct{}

var vmIsolationAPI = &VMIsolationAPI{}

type VMIsolationPayload struct {
	Reason     string `json:"reason"`
	RecordUUID string `json:"record_uuid,omitempty"`
}
type VMIsolationAccepted struct {
	InstanceUUID string `json:"instance_uuid"`
	Action       string `json:"action"`
	Status       string `json:"status"`
}

// @Summary Quarantine a VM on CPUs selected by its compute node
// @Tags VM Inspection
// @Accept json
// @Produce json
// @Param id path string true "Instance UUID"
// @Param body body VMIsolationPayload true "Manual quarantine reason"
// @Success 202 {object} VMIsolationAccepted
// @Failure 400 {object} common.APIError
// @Failure 403 {object} common.APIError
// @Failure 409 {object} common.APIError
// @Router /instances/{id}/quarantine [post]
func (*VMIsolationAPI) Quarantine(c *gin.Context) {
	var p VMIsolationPayload
	if !decodeVMInspectionJSON(c, &p) {
		return
	}
	id := c.Param("id")
	if err := routes.QuarantineVM(c.Request.Context(), id, p.Reason); err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, VMIsolationAccepted{id, "quarantine", "accepted"})
}

// @Summary Restore a VM's original CPU affinity
// @Tags VM Inspection
// @Accept json
// @Produce json
// @Param id path string true "Instance UUID"
// @Param body body VMIsolationPayload true "Release reason and optional record UUID"
// @Success 202 {object} VMIsolationAccepted
// @Failure 400 {object} common.APIError
// @Failure 403 {object} common.APIError
// @Failure 409 {object} common.APIError
// @Router /instances/{id}/release-quarantine [post]
func (*VMIsolationAPI) Release(c *gin.Context) {
	var p VMIsolationPayload
	if !decodeVMInspectionJSON(c, &p) {
		return
	}
	id := c.Param("id")
	if err := routes.ReleaseVMQuarantine(c.Request.Context(), id, p.Reason, p.RecordUUID); err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, VMIsolationAccepted{id, "release", "accepted"})
}
