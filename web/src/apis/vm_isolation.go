package apis

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"web/src/common"
	"web/src/routes"
)

type VMIsolationAPI struct{}

var vmIsolationAPI = &VMIsolationAPI{}

type VMIsolationAccepted struct {
	InstanceUUID string `json:"instance_uuid"`
	Action       string `json:"action"`
	Status       string `json:"status"`
}

// @Summary Quarantine a VM on CPUs selected by its compute node
// @Tags VM Inspection
// @Produce json
// @Param id path string true "Instance UUID"
// @Success 202 {object} VMIsolationAccepted
// @Failure 400 {object} common.APIError
// @Failure 403 {object} common.APIError
// @Failure 409 {object} common.APIError
// @Router /instances/{id}/quarantine [post]
func (*VMIsolationAPI) Quarantine(c *gin.Context) {
	id := c.Param("id")
	if err := routes.QuarantineVM(c.Request.Context(), id); err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, VMIsolationAccepted{id, "quarantine", "accepted"})
}

// @Summary Remove a VM's CPU isolation pins
// @Tags VM Inspection
// @Produce json
// @Param id path string true "Instance UUID"
// @Success 202 {object} VMIsolationAccepted
// @Failure 400 {object} common.APIError
// @Failure 403 {object} common.APIError
// @Failure 409 {object} common.APIError
// @Router /instances/{id}/release-quarantine [post]
func (*VMIsolationAPI) Release(c *gin.Context) {
	id := c.Param("id")
	if err := routes.ReleaseVMQuarantine(c.Request.Context(), id); err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, VMIsolationAccepted{id, "release", "accepted"})
}

// ProcessVMInspectionWebhook matches collected evidence and decides automatic isolation.
func (*VMIsolationAPI) ProcessVMInspectionWebhook(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var request routes.AlertWebhookRequest
	// Alertmanager includes envelope and alert fields beyond the ones we consume.
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ErrorResponse(c, http.StatusBadRequest, "Invalid alert webhook request", err)
		return
	}
	for _, alert := range request.Alerts {
		if alert.Status != "firing" {
			continue
		}
		if err := routes.MatchVMInspection(c.Request.Context(), alert.Labels); err != nil {
			vmInspectionAPIError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
