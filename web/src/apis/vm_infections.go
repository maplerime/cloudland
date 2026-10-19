package apis

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"time"
	"web/src/common"
	"web/src/model"
	"web/src/routes"
)

type VMInfectionAPI struct{}

var vmInfectionAPI = &VMInfectionAPI{}

type VMInfectionResponse struct {
	UUID             string            `json:"uuid"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	InstanceUUID     string            `json:"instance_uuid"`
	HyperHostname    string            `json:"hyper_hostname"`
	InspectionResult json.RawMessage   `json:"inspection_result"`
	CollectedAt      *time.Time        `json:"collected_at"`
	RulesetVersion   string            `json:"ruleset_version"`
	MatchSummary     string            `json:"match_summary"`
	Verdict          string            `json:"verdict"`
	MatchRuleIDs     []string          `json:"match_rule_ids"`
	IsolationResult  VMIsolationResult `json:"isolation_result"`
	ScanID           string            `json:"scan_id"`
}
type VMIsolationResult struct {
	Status       string     `json:"status"`
	CPUs         string     `json:"cpus"`
	Source       string     `json:"source"`
	OperationAt  *time.Time `json:"operation_at"`
	Remark       string     `json:"remark"`
	ErrorMessage string     `json:"error_message"`
}

type VMInfectionListResponse struct {
	Offset  int                   `json:"offset"`
	Limit   int                   `json:"limit"`
	Total   int                   `json:"total"`
	Records []VMInfectionResponse `json:"records"`
}

func convertVMInfection(row model.VMInfectionRecord) VMInfectionResponse {
	ids := []string{}
	_ = json.Unmarshal([]byte(row.MatchRuleIDs), &ids)
	inspection := json.RawMessage(nil)
	if json.Valid([]byte(row.InspectionResult)) {
		inspection = json.RawMessage(row.InspectionResult)
	}
	if ids == nil {
		ids = []string{}
	}
	var collected, operation *time.Time
	if row.CollectedAt != nil {
		t := row.CollectedAt.UTC()
		collected = &t
	}
	if row.IsolationStatus != "" && row.IsolationStatus != "not_requested" && row.IsolationStatus != "disabled" && !row.OperationAt.IsZero() {
		t := row.OperationAt.UTC()
		operation = &t
	}
	return VMInfectionResponse{UUID: row.UUID, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		InstanceUUID: row.InstanceUUID, HyperHostname: row.HyperHostname, InspectionResult: inspection, CollectedAt: collected,
		RulesetVersion: row.RulesetVersion, MatchSummary: row.MatchSummary, Verdict: row.Verdict, MatchRuleIDs: ids, ScanID: row.ScanID,
		IsolationResult: VMIsolationResult{Status: row.IsolationStatus, CPUs: row.IsolationCPUs, Source: row.IsolationSource,
			OperationAt: operation, Remark: row.Remark, ErrorMessage: row.ErrorMessage}}

}

// @Summary List VM infection and manual disposition records
// @Tags VM Inspection
// @Produce json
// @Param instance_uuid query string false "Instance UUID"
// @Param isolation_status query string false "Disposition status"
// @Param verdict query string false "Inspection verdict"
// @Param start query string false "Operation start, RFC3339"
// @Param end query string false "Operation end, RFC3339"
// @Param offset query int false "Offset"
// @Param limit query int false "Limit, at most 200"
// @Success 200 {object} VMInfectionListResponse
// @Failure 403 {object} common.APIError
// @Router /vm-infections [get]
func (*VMInfectionAPI) List(c *gin.Context) {
	p := routes.VMInfectionListInput{InstanceUUID: c.Query("instance_uuid"), IsolationStatus: c.Query("isolation_status"), Verdict: c.Query("verdict"), Limit: 16}
	for key, target := range map[string]*int{"offset": &p.Offset, "limit": &p.Limit} {
		if raw := c.Query(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || key == "limit" && (n < 1 || n > 200) {
				common.ErrorResponse(c, http.StatusBadRequest, "Invalid pagination", fmt.Errorf("invalid %s", key))
				return
			}
			*target = n
		}
	}
	for key, target := range map[string]*time.Time{"start": &p.Start, "end": &p.End} {
		if raw := c.Query(key); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				common.ErrorResponse(c, http.StatusBadRequest, "Invalid date", err)
				return
			}
			*target = value
		}
	}
	rows, total, err := routes.ListVMInfections(c.Request.Context(), p)
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	out := VMInfectionListResponse{Offset: p.Offset, Limit: p.Limit, Total: total, Records: []VMInfectionResponse{}}
	for _, row := range rows {
		out.Records = append(out.Records, convertVMInfection(row))
	}
	c.JSON(http.StatusOK, out)
}

// @Summary Get a VM infection or disposition record
// @Tags VM Inspection
// @Produce json
// @Param uuid path string true "Record UUID"
// @Success 200 {object} VMInfectionResponse
// @Failure 404 {object} common.APIError
// @Router /vm-infections/{uuid} [get]
func (*VMInfectionAPI) Get(c *gin.Context) {
	row, err := routes.GetVMInfection(c.Request.Context(), c.Param("uuid"))
	if err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, convertVMInfection(*row))
}

// @Summary Delete a record after CPU restoration is no longer needed
// @Tags VM Inspection
// @Produce json
// @Param uuid path string true "Record UUID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} common.APIError
// @Failure 409 {object} common.APIError
// @Router /vm-infections/{uuid} [delete]
func (*VMInfectionAPI) Delete(c *gin.Context) {
	id := c.Param("uuid")
	if err := routes.DeleteVMInfection(c.Request.Context(), id); err != nil {
		vmInspectionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"uuid": id, "status": "deleted"})
}
