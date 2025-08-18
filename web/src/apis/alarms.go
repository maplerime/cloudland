package apis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"web/src/model"
	"web/src/routes"

	"context"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

type AlarmAPI struct {
	operator   *routes.AlarmOperator
	alarmAdmin *routes.AlarmAdmin
}

var alarmAPI = &AlarmAPI{
	operator:   &routes.AlarmOperator{},
	alarmAdmin: &routes.AlarmAdmin{},
}

// updateMatchedVMsJSON updates the matched_vms.json file, supporting one VM matching multiple rule groups
// Parameters:
//   - ctx: context
//   - vmUUIDs: list of VM UUIDs
//   - groupUUID: rule group UUID
//   - operation: operation type, "add" for add/update, "remove" for delete
func (a *AlarmAPI) updateMatchedVMsJSON(ctx context.Context, vmUUIDs []string, groupUUID, operation string) error {
	// Path to matched_vms.json file
	matchedVMsFile := "/etc/prometheus/lists/matched_vms.json"

	// Read existing matched_vms.json
	var matchedVMs []map[string]interface{}
	existingData, err := routes.ReadFile(matchedVMsFile)
	if err == nil && len(existingData) > 0 {
		if err := json.Unmarshal(existingData, &matchedVMs); err != nil {
			log.Printf("Failed to parse existing matched_vms.json: %v", err)
			matchedVMs = []map[string]interface{}{}
		}
	} else {
		matchedVMs = []map[string]interface{}{}
	}

	// Process based on operation type
	if operation == "add" {
		// Add or update VM entries
		for _, instanceid := range vmUUIDs {
			domain, err := routes.GetInstanceUUIDByDomain(ctx, instanceid)
			if err != nil {
				log.Printf("Failed to get domain for instanceid=%s: %v", instanceid, err)
				continue
			}

			// Create new entry - simplified version with only necessary fields
			newEntry := map[string]interface{}{
				"labels": map[string]interface{}{
					"domain":         domain,
					"__meta_rule_id": fmt.Sprintf("%s-%s", domain, groupUUID),
				},
			}

			// Check if the same domain+group combination already exists
			entryExists := false
			for i, vm := range matchedVMs {
				labels, ok := vm["labels"].(map[string]interface{})
				if !ok {
					continue
				}

				domainVal, hasDomain := labels["domain"].(string)
				ruleID, hasRuleID := labels["__meta_rule_id"].(string)
				expectedRuleID := fmt.Sprintf("%s-%s", domain, groupUUID)

				if hasDomain && hasRuleID && domainVal == domain && ruleID == expectedRuleID {
					// Update existing entry
					entryExists = true
					matchedVMs[i] = newEntry
					break
				}
			}

			// If it doesn't exist, add a new entry
			if !entryExists {
				matchedVMs = append(matchedVMs, newEntry)
			}
		}
	} else if operation == "remove" {
		// Delete entries related to the specified rule group
		filteredVMs := []map[string]interface{}{}
		for _, vm := range matchedVMs {
			labels, ok := vm["labels"].(map[string]interface{})
			if !ok {
				filteredVMs = append(filteredVMs, vm)
				continue
			}

			ruleID, ok := labels["__meta_rule_id"].(string)
			if !ok || !strings.HasSuffix(ruleID, "-"+groupUUID) {
				filteredVMs = append(filteredVMs, vm)
			}
		}
		matchedVMs = filteredVMs
	}

	// Save updated matched_vms.json
	matchedVMsData, err := json.MarshalIndent(matchedVMs, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal matched_vms.json: %v", err)
		return err
	}

	err = routes.WriteFile(matchedVMsFile, matchedVMsData, 0644)
	if err != nil {
		log.Printf("Failed to write matched_vms.json: %v", err)
		return err
	}

	return nil
}

func (a *AlarmAPI) LinkRuleToVM(c *gin.Context) {
	var req struct {
		GroupUUID string   `json:"group_uuid" binding:"required"`
		VMUUIDs   []string `json:"vm_uuids" binding:"required"`
		Interface string   `json:"interface"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	groupUUID := req.GroupUUID
	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
		return
	} else if err != nil {
		log.Printf("Error retrieving rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
		return
	}
	if !group.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "Rule group is not enabled, please enable the rule group before linking it to a virtual machine",
			"code":       "RULE_GROUP_DISABLED",
			"group_uuid": groupUUID,
		})
		return
	}
	// Full overwrite of VMRuleLink
	_, _ = a.operator.DeleteVMLink(c.Request.Context(), groupUUID, "", req.Interface)
	_ = a.operator.BatchLinkVMs(c.Request.Context(), groupUUID, req.VMUUIDs, req.Interface)

	// Update VM matching information
	if len(req.VMUUIDs) > 0 {
		// Update matched_vms.json with VM information
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.VMUUIDs, group.UUID, "add")
	} else {
		// If no VMs, remove related entries from matched_vms.json
		_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{}, group.UUID, "remove")
	}

	// Query latest linked VMs
	vmLinks, _ := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	linkedVMs := []string{}
	for _, link := range vmLinks {
		linkedVMs = append(linkedVMs, link.VMUUID)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": groupUUID,
			"linked_vms": linkedVMs,
		},
	})
}

func (a *AlarmAPI) CreateCPURule(c *gin.Context) {
	var req struct {
		Name      string           `json:"name" binding:"required"`
		Owner     string           `json:"owner" binding:"required"`
		Email     string           `json:"email"`
		Action    bool             `json:"action"`
		Rules     []routes.CPURule `json:"rules" binding:"required,min=1"`
		LinkedVMs []string         `json:"linkedvms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := &model.RuleGroupV2{
		Name:    req.Name,
		Type:    routes.RuleTypeCPU,
		Owner:   req.Owner,
		Enabled: true,
		Email:   req.Email,
		Action:  req.Action,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operator failed: " + err.Error()})
		return
	}
	for _, rule := range req.Rules {
		detail := &model.CPURuleDetail{
			GroupUUID:    group.UUID,
			Name:         rule.Name,
			Over:         rule.Over,
			Duration:     rule.Duration,
			DownDuration: rule.DownDuration,
			DownTo:       rule.DownTo,
		}
		if err := a.operator.CreateCPURuleDetail(c.Request.Context(), detail); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}
	}
	if len(req.LinkedVMs) > 0 {
		// Full overwrite of VMRuleLink
		_, _ = a.operator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = a.operator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")

		// Update matched_vms.json with VM information
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add")
	}
	data := map[string]interface{}{
		"owner":      req.Owner,
		"rule_group": group.UUID,
		"email":      req.Email,
		"action":     req.Action,
		"rules":      req.Rules,
	}
	for i, rule := range req.Rules {
		data[fmt.Sprintf("name_%d", i)] = rule.Name
		data[fmt.Sprintf("over_%d", i)] = rule.Over
		data[fmt.Sprintf("duration_%d", i)] = rule.Duration
		data[fmt.Sprintf("down_to_%d", i)] = rule.DownTo
		data[fmt.Sprintf("down_duration_%d", i)] = rule.DownDuration
	}
	templateFile := "user-cpu-rule.yml.j2"
	outputFile := filepath.Join(routes.RulesGeneral, fmt.Sprintf("cpu-%s-%s.yml", req.Owner, group.UUID))
	if err := routes.ProcessTemplate(templateFile, outputFile, data); err != nil {
		log.Printf("Failed to render cpu rule template: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render cpu rule template"})
		return
	}
	routes.ReloadPrometheus()
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": group.UUID,
			"enabled":    true,
			"linkedvms":  req.LinkedVMs,
		},
	})
}

func (a *AlarmAPI) GetCPURules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	queryParams := routes.ListRuleGroupsParams{
		RuleType: routes.RuleTypeCPU,
		Page:     page,
		PageSize: pageSize,
	}

	if groupUUID != "" {
		queryParams.GroupUUID = groupUUID
		queryParams.PageSize = 1
	}

	groups, total, err := a.operator.ListRuleGroups(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query rules group failed: " + err.Error()})
		return
	}
	responseData := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		details, err := a.operator.GetCPURuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "function get cpu rule detailed failed: " + err.Error()})
			return
		}
		linkedVMs := make([]string, 0)
		vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err == nil {
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, link.VMUUID)
			}
		}

		ruleDetails := make([]gin.H, 0, len(details))
		for _, d := range details {
			ruleDetails = append(ruleDetails, gin.H{
				"id":            d.ID,
				"rule_uuid":     d.UUID,
				"name":          d.Name,
				"duration":      d.Duration,
				"over":          d.Over,
				"down_to":       d.DownTo,
				"down_duration": d.DownDuration,
			})
		}

		responseData = append(responseData, gin.H{
			"id":          group.ID,
			"group_uuid":  group.UUID,
			"name":        group.Name,
			"trigger_cnt": group.TriggerCnt,
			"create_time": group.CreatedAt.Format(time.RFC3339),
			"rules":       ruleDetails,
			"enabled":     group.Enabled,
			"email":       group.Email,
			"action":      group.Action,
			"linked_vms":  linkedVMs,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data": responseData,
		"meta": gin.H{
			"total":        total,
			"current_page": page,
			"per_page":     pageSize,
			"total_pages":  int(math.Ceil(float64(total) / float64(pageSize))),
		},
	})
}

func (a *AlarmAPI) DeleteCPURule(c *gin.Context) {
	groupUUID := c.Param("uuid")
	if groupUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty UUID error."})
		return
	}
	if _, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, routes.RuleTypeCPU); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "rules not existed",
				"code":  "RESOURCE_NOT_FOUND",
				"uuid":  groupUUID,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "UUID wrong type mismatch failure",
				"code":  "INTERNAL_ERROR",
				"uuid":  groupUUID,
			})
		}
		return
	}

	// Delete all associated VMRuleLink
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	linkedVMs := []string{}
	if err == nil {
		for _, link := range vmLinks {
			linkedVMs = append(linkedVMs, link.VMUUID)
		}
		_, _ = a.operator.DeleteVMLink(c.Request.Context(), groupUUID, "", "")
	}

	// Remove related entries from matched_vms.json
	// Define empty mapping file path for backward compatibility
	mappingFile := ""
	_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{}, groupUUID, "remove")

	// Delete rule-related table data
	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, routes.RuleTypeCPU); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}

	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Failed to reload Prometheus: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload Prometheus"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": []string{mappingFile},
			"linked_vms":    linkedVMs,
		},
	})
}

func (a *AlarmAPI) EnableRules(c *gin.Context) {
	groupUUID := c.Param("id")

	// Retrieve rule group with details using operator
	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
		return
	} else if err != nil {
		log.Printf("Failed to retrieve rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	if group.Enabled {
		c.JSON(http.StatusOK, gin.H{"status": "already enabled"})
		return
	}
	// Generate rule paths
	generalPath, specialPath := routes.RulePaths(group.Type, groupUUID)

	// Create symbolic links
	enabledLinks := make([]string, 0, 2)

	// link general rules
	generalLink := filepath.Join(routes.RulesEnabled, fmt.Sprintf("%s-general-%s.yml", group.Type, groupUUID))
	if err = routes.CreateSymlink(generalPath, generalLink); err != nil && !os.IsExist(err) {
		log.Printf("Failed to create general symlink: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create symlink"})
		return
	}
	//link special links
	enabledLinks = append(enabledLinks, generalLink)
	status, err := routes.CheckFileExists(specialPath)
	if err != nil {
		log.Printf("Failed to check special file existence: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check file existence"})
		return
	}
	if status {
		specialLink := filepath.Join(routes.RulesEnabled, fmt.Sprintf("%s-special-%s.yml", group.Type, groupUUID))
		if err = routes.CreateSymlink(specialPath, specialLink); err != nil && !os.IsExist(err) {
			log.Printf("Failed to create special symlink: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create symlink"})
			return
		}
		enabledLinks = append(enabledLinks, specialLink)
	}

	// Update DB status
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupUUID, true); err != nil {
		log.Printf("Failed to update rule group status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Status update failed"})
		return
	}

	// Reload Prometheus configuration
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Failed to reload Prometheus: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload Prometheus"})
		return
	}

	// Query latest group information and linked VMs
	group, _ = a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	vmLinks, _ := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	linkedVMs := []string{}
	for _, link := range vmLinks {
		linkedVMs = append(linkedVMs, link.VMUUID)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": groupUUID,
			"enabled":    group.Enabled,
			"email":      group.Email,
			"action":     group.Action,
			"linked_vms": linkedVMs,
		},
	})
}

func (a *AlarmAPI) DisableRules(c *gin.Context) {
	groupUUID := c.Param("id")

	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
		return
	} else if err != nil {
		log.Printf("Failed to retrieve rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	if !group.Enabled {
		c.JSON(http.StatusOK, gin.H{"status": "already disabled"})
		return
	}
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("Error getting linked VMs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check VM associations"})
		return
	}
	if len(vmLinks) > 0 {
		vmUUIDs := make([]string, 0, len(vmLinks))
		for _, link := range vmLinks {
			vmUUIDs = append(vmUUIDs, link.VMUUID)
		}

		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "Rule group is linked to virtual machines, please unlink them before disabling",
			"code":       "RULE_GROUP_LINKED",
			"group_uuid": groupUUID,
			"linked_vms": vmUUIDs,
		})
		return
	}

	deletedFiles := make([]string, 0)
	specialLink := filepath.Join(routes.RulesEnabled, fmt.Sprintf("%s-special-%s.yml", group.Type, groupUUID))
	//unlink special rules
	status, err := routes.CheckFileExists(specialLink)
	if err != nil {
		log.Printf("Failed to check special link existence: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check file existence"})
		return
	}
	if status {
		if err := routes.RemoveFile(specialLink); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove special link: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove symlink"})
			return
		}
		deletedFiles = append(deletedFiles, specialLink)
	}

	generalLink := filepath.Join(routes.RulesEnabled, fmt.Sprintf("%s-general-%s.yml", group.Type, groupUUID))
	// unlink general rules
	status, err = routes.CheckFileExists(generalLink)
	if err != nil {
		log.Printf("Failed to check general link existence: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check file existence"})
		return
	}
	if status {
		if err := routes.RemoveFile(generalLink); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove general link: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove symlink"})
			return
		}
		deletedFiles = append(deletedFiles, generalLink)
	}
	// Update DB status
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupUUID, false); err != nil {
		log.Printf("Failed to update rule group status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Status update failed"})
		return
	}

	// Reload Prometheus
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Failed to reload Prometheus: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload Prometheus"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_links": deletedFiles,
			"group_uuid":    groupUUID,
		},
	})
}

func (a *AlarmAPI) GetCurrentAlarms(c *gin.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	targetURL := fmt.Sprintf("http://%s:%d/api/v1/alerts", routes.GetPrometheusIP(), routes.GetPrometheusPort())
	resp, err := client.Get(targetURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}
	// Process active alerts
	if data, exists := result["data"]; exists {
		if alertData, ok := data.(map[string]interface{}); ok {
			if alerts, ok := alertData["alerts"].([]interface{}); ok {
				filtered := filterActiveAlerts(alerts)
				if filtered == nil {
					filtered = make([]interface{}, 0)
				}
				for _, alert := range filtered {
					if alertMap, ok := alert.(map[string]interface{}); ok {
						labels := alertMap["labels"].(map[string]interface{})
						if domain, ok := labels["domain"].(string); ok {
							uuid, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), domain)
							if err != nil {
								log.Printf("Domain conversion failed domain=%s error=%v", domain, err)
								labels["instance_uuid"] = "" // Ensure empty value
							} else {
								labels["instance_uuid"] = uuid
							}
						}
					}
				}
				result["data"] = map[string]interface{}{"alerts": filtered}
			}
		} else {
			log.Printf("Unexpected data format: %T", data)
			result["data"] = map[string]interface{}{"alerts": []interface{}{}}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   result["data"],
	})
}

func (a *AlarmAPI) GetHistoryAlarm(c *gin.Context) {
	startStr := c.Query("start")
	endStr := c.Query("end")
	stepStr := c.DefaultQuery("step", "300s")
	if startStr == "" || endStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start and end parameters are required"})
		return
	}
	// Convert timestamps to integers
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start timestamp"})
		return
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end timestamp"})
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	baseURL := fmt.Sprintf("http://%s:%d/api/v1/query_range", routes.GetPrometheusIP(), routes.GetPrometheusPort())

	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
		return
	}

	// Set query parameters
	q := req.URL.Query()
	q.Add("query", "ALERTS")
	q.Add("start", strconv.FormatInt(start, 10))
	q.Add("end", strconv.FormatInt(end, 10))
	q.Add("step", stepStr)
	req.URL.RawQuery = q.Encode()

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query Prometheus"})
		return
	}
	defer resp.Body.Close()

	// Decode response structure
	var promResp struct {
		Data struct {
			Result []struct {
				Metric struct {
					Alertname  string `json:"alertname"`
					Domain     string `json:"domain"`
					Instance   string `json:"instance"`
					Alertstate string `json:"alertstate"`
				} `json:"metric"`
				Values [][]interface{} `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
		log.Printf("[GetHistoryAlarm] error Prometheus resp status: %s (StatusCode: %d)\n", resp.Status, resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse response"})
		return
	}
	processed := make([]gin.H, 0)
	for _, result := range promResp.Data.Result {
		instanceUUID, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), result.Metric.Domain)
		if err != nil {
			log.Printf("Domain to UUID convert failed : domain=%s error=%v", result.Metric.Domain, err)
			instanceUUID = ""
		}
		events := make([]gin.H, 0)
		now := time.Now().Unix()

		for _, value := range result.Values {
			if len(value) < 2 {
				continue
			}

			// Extract timestamp (Prometheus returns seconds as float)
			timestamp, _ := strconv.ParseInt(fmt.Sprintf("%.0f", value[0]), 10, 64)
			duration := now - timestamp

			events = append(events, gin.H{
				"timestamp": timestamp,
				"duration":  duration,
			})
		}

		processed = append(processed, gin.H{
			"alert":    result.Metric.Alertname,
			"domain":   result.Metric.Domain,
			"instance": instanceUUID,
			"state":    result.Metric.Alertstate,
			"count":    len(events),
			"events":   events,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   processed,
	})
}

func filterActiveAlerts(alerts []interface{}) []interface{} {
	filtered := make([]interface{}, 0)
	for _, a := range alerts {
		alert, ok := a.(map[string]interface{})
		if !ok {
			continue
		}

		if status, ok := alert["state"].(string); ok && status == "firing" {
			filtered = append(filtered, alert)
		}
	}
	return filtered
}

func (a *AlarmAPI) ProcessAlertWebhook(c *gin.Context) {
	var notification struct {
		Status string `json:"status"`
		Alerts []struct {
			State       string            `json:"state"`
			ActiveAt    time.Time         `json:"activeAt"`
			Value       string            `json:"value"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    time.Time         `json:"startsAt"`
			EndsAt      time.Time         `json:"endsAt"`
		} `json:"alerts"`
	}
	log.Printf("ProcessAlertWebhook Processing trigger.\n")
	body, _ := io.ReadAll(c.Request.Body)
	log.Printf("ProcessAlertWebhook Raw request body: %s", string(body))
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	if err := c.ShouldBindJSON(&notification); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid warning msg format\n"})
		log.Printf("ProcessAlertWebhook invalid warning msg format\n")
		return
	}
	status := notification.Status
	log.Printf("ProcessAlertWebhook Processing alert: status=%s \n", status)
	for _, alert := range notification.Alerts {
		alert_type := alert.Labels["alert_type"]
		alertName := alert.Labels["alertname"]
		severity := alert.Labels["severity"]
		owner := alert.Labels["owner"]
		actionFlag := alert.Labels["action"] == "true"
		email := alert.Labels["email"]
		domain := alert.Labels["domain"]
		rule_group_uuid := alert.Labels["rule_group"]
		log.Printf("ProcessAlertWebhook Processing alert: alert_type=%s alertName=%s severity=%s\n", alert_type, alertName, severity)
		log.Printf("ProcessAlertWebhook Processing alert: domain=%s rule_group_uuid=%s\n", domain, rule_group_uuid)
		description := alert.Annotations["description"]
		summary := alert.Annotations["summary"]
		log.Printf("ProcessAlertWebhook Processing alert: summary=%s description=%s \n", summary, description)
		target_device := ""
		if alert_type == "bw" {
			target_device = alert.Labels["target_device"]
		}
		alertRecord := &routes.Alert{
			Name:          alertName,
			RuleGroupUUID: rule_group_uuid,
			Severity:      severity,
			Summary:       summary,
			Description:   description,
			StartsAt:      alert.StartsAt,
			AlertType:     alert_type,
			TargetDevice:  target_device,
		}
		if status == "firing" {
			if email != "" {
				// Email notification logic (existing email logic unchanged)
				log.Printf("[Webhook] Send email to: %s", email)
			}
			if actionFlag {
				if owner == "admin" {
					a.notifyAdminConsole(alertRecord)
					a.adjustResource(alertRecord, domain, true)
				} else {
					a.notifyUserConsole(alertRecord, owner)
				}
			}
			if err := a.notifyRealtimeAlert(alertRecord); err != nil {
				log.Printf("Failed to notify realtime alert: %v", err)
			}
		} else {
			// Resolved: recover resources
			if actionFlag && owner == "admin" {
				a.recoverResource(alertRecord, domain)
			}
			log.Printf("ProcessAlertWebhook alert resolved alert: summary=%s alertRecord=%v \n", summary, alertRecord)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "processed",
		"alerts":  len(notification.Alerts),
		"message": "alarm process completed",
	})
}

// Empty implementation: resource adjustment
func (a *AlarmAPI) adjustResource(alert *routes.Alert, domain string, limit bool) {
	log.Printf("[ResourceAdjust] domain=%s alert=%s limit=%v", domain, alert.Name, limit)
	// TODO: Implement resource limit adjustment logic
}

// Empty implementation: resource recovery
func (a *AlarmAPI) recoverResource(alert *routes.Alert, domain string) {
	log.Printf("[ResourceRecover] domain=%s alert=%s", domain, alert.Name)
	// TODO: Implement resource recovery logic
}

// Empty implementation: notify admin console
func (a *AlarmAPI) notifyAdminConsole(alert *routes.Alert) {
	log.Printf("[AdminConsoleNotify] alert=%s", alert.Name)
	// TODO: Implement admin console notification logic
}

// Empty implementation: notify user console
func (a *AlarmAPI) notifyUserConsole(alert *routes.Alert, owner string) {
	log.Printf("[UserConsoleNotify] owner=%s alert=%s", owner, alert.Name)
	// TODO: Implement user console notification logic
}

func (a *AlarmAPI) notifyRealtimeAlert(alert *routes.Alert) error {
	log.Printf("notifyRealtimeAlert input: %v", alert)
	return nil
	// notify message to ui
}

// GetActiveRules retrieves active rules from Prometheus
func (a *AlarmAPI) GetActiveRules(c *gin.Context) {
	// Build Prometheus API URL from config
	apiURL := fmt.Sprintf("http://%s:%d/api/v1/rules", routes.GetPrometheusIP(), routes.GetPrometheusPort())

	// Create HTTP client with timeout
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		log.Printf("Create request failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create API request"})
		return
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("API request failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to Prometheus"})
		return
	}
	defer resp.Body.Close()

	// Validate response status
	if resp.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code: %d", resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prometheus API returned non-200 status"})
		return
	}

	// Parse JSON response
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("JSON decode error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response data"})
		return
	}

	// Extract rule groups
	activeRules := make([]gin.H, 0)
	if data, ok := result["data"].(map[string]interface{}); ok {
		if groups, ok := data["groups"].([]interface{}); ok {
			for _, group := range groups {
				if gMap, ok := group.(map[string]interface{}); ok {
					activeRules = append(activeRules, gin.H{
						"name":  gMap["name"],
						"rules": gMap["rules"],
					})
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   activeRules,
	})
}

func (a *AlarmAPI) CreateBWRule(c *gin.Context) {
	var req struct {
		Name         string          `json:"name" binding:"required"`
		Owner        string          `json:"owner" binding:"required"`
		Email        string          `json:"email"`
		Action       bool            `json:"action"`
		Rules        []routes.BWRule `json:"rules" binding:"required,min=1"`
		LinkedVMs    []string        `json:"linkedvms"`
		TargetDevice string          `json:"target_device"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := &model.RuleGroupV2{
		Name:    req.Name,
		Type:    routes.RuleTypeBW,
		Owner:   req.Owner,
		Enabled: true,
		Email:   req.Email,
		Action:  req.Action,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operator failed: " + err.Error()})
		return
	}
	for _, rule := range req.Rules {
		detail := &model.BWRuleDetail{
			GroupUUID:       group.UUID,
			Name:            rule.Name,
			InThreshold:     -1,
			InDuration:      -1,
			InOverType:      "absolute",
			InDownTo:        -1,
			InDownDuration:  -1,
			OutThreshold:    -1,
			OutDuration:     -1,
			OutOverType:     "absolute",
			OutDownTo:       -1,
			OutDownDuration: -1,
		}
		if rule.InEnabled {
			detail.InThreshold = rule.InThreshold
			detail.InDuration = rule.InDuration
			detail.InOverType = rule.InOverType
			detail.InDownTo = rule.InDownTo
			detail.InDownDuration = rule.InDownDuration
		}
		if rule.OutEnabled {
			detail.OutThreshold = rule.OutThreshold
			detail.OutDuration = rule.OutDuration
			detail.OutOverType = rule.OutOverType
			detail.OutDownTo = rule.OutDownTo
			detail.OutDownDuration = rule.OutDownDuration
		}
		if err := a.operator.CreateBWRuleDetail(c.Request.Context(), detail); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}
	}
	if len(req.LinkedVMs) > 0 {
		_, _ = a.operator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = a.operator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")

		// Update matched_vms.json with VM information
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add")
	}
	// Render templates for in/out directions
	for _, rule := range req.Rules {
		if rule.InEnabled {
			data := map[string]interface{}{
				"owner":            req.Owner,
				"rule_group":       group.UUID,
				"email":            req.Email,
				"action":           req.Action,
				"in_threshold":     rule.InThreshold,
				"in_duration":      rule.InDuration,
				"in_down_to":       rule.InDownTo,
				"in_down_duration": rule.InDownDuration,
				"target_device":    req.TargetDevice,
			}
			templateFile := "VM-in-bw-rule.yml.j2"
			outputFile := filepath.Join(routes.RulesGeneral, fmt.Sprintf("bw-in-%s-%s-%s.yml", req.Owner, group.UUID, rule.Name))
			if err := routes.ProcessTemplate(templateFile, outputFile, data); err != nil {
				log.Printf("Failed to render in-bw rule template: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render in-bw rule template"})
				return
			}
		}
		if rule.OutEnabled {
			data := map[string]interface{}{
				"owner":             req.Owner,
				"rule_group":        group.UUID,
				"email":             req.Email,
				"action":            req.Action,
				"out_threshold":     rule.OutThreshold,
				"out_duration":      rule.OutDuration,
				"out_down_to":       rule.OutDownTo,
				"out_down_duration": rule.OutDownDuration,
				"target_device":     req.TargetDevice,
			}
			templateFile := "VM-out-bw-rule.yml.j2"
			outputFile := filepath.Join(routes.RulesGeneral, fmt.Sprintf("bw-out-%s-%s-%s.yml", req.Owner, group.UUID, rule.Name))
			if err := routes.ProcessTemplate(templateFile, outputFile, data); err != nil {
				log.Printf("Failed to render out-bw rule template: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render out-bw rule template"})
				return
			}
		}
	}
	routes.ReloadPrometheus()
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": group.UUID,
			"enabled":    true,
			"linkedvms":  req.LinkedVMs,
		},
	})
}

func (a *AlarmAPI) GetBWRules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	queryParams := routes.ListRuleGroupsParams{
		RuleType: routes.RuleTypeBW,
		Page:     page,
		PageSize: pageSize,
	}

	if groupUUID != "" {
		queryParams.GroupUUID = groupUUID
		queryParams.PageSize = 1
	}

	groups, total, err := a.operator.ListRuleGroups(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query BW rules failed: " + err.Error()})
		return
	}
	responseData := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		details, err := a.operator.GetBWRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query BW detail rules failed: " + err.Error()})
			return
		}
		linkedVMs := make([]string, 0)
		vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err == nil {
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, link.VMUUID)
			}
		}

		ruleDetails := make([]gin.H, 0, len(details))
		for _, d := range details {
			ruleDetails = append(ruleDetails, gin.H{
				"id":                d.ID,
				"rule_uuid":         d.UUID,
				"name":              d.Name,
				"in_threshold":      d.InThreshold,
				"in_duration":       d.InDuration,
				"in_over_type":      d.InOverType,
				"in_down_to":        d.InDownTo,
				"in_down_duration":  d.InDownDuration,
				"out_threshold":     d.OutThreshold,
				"out_duration":      d.OutDuration,
				"out_over_type":     d.OutOverType,
				"out_down_to":       d.OutDownTo,
				"out_down_duration": d.OutDownDuration,
			})
		}

		responseData = append(responseData, gin.H{
			"id":          group.ID,
			"group_uuid":  group.UUID,
			"name":        group.Name,
			"trigger_cnt": group.TriggerCnt,
			"create_time": group.CreatedAt.Format(time.RFC3339),
			"rules":       ruleDetails,
			"enabled":     group.Enabled,
			"email":       group.Email,
			"action":      group.Action,
			"linked_vms":  linkedVMs,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data": responseData,
		"meta": gin.H{
			"total":        total,
			"current_page": page,
			"per_page":     pageSize,
			"total_pages":  int(math.Ceil(float64(total) / float64(pageSize))),
		},
	})
}

func (a *AlarmAPI) DeleteBWRules(c *gin.Context) {
	groupUUID := c.Param("uuid")
	if groupUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty UUID error."})
		return
	}
	if _, err := a.operator.GetBWRulesByGroupUUID(c.Request.Context(), groupUUID, routes.RuleTypeBW); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": " target rules not fould",
				"code":  "RESOURCE_NOT_FOUND",
				"uuid":  groupUUID,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "server internal error",
				"code":  "INTERNAL_ERROR",
				"uuid":  groupUUID,
			})
		}
		return
	}

	// Delete all associated VMRuleLink
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	linkedVMs := []string{}
	if err == nil {
		for _, link := range vmLinks {
			linkedVMs = append(linkedVMs, link.VMUUID)
		}
		_, _ = a.operator.DeleteVMLink(c.Request.Context(), groupUUID, "", "")
	}

	// Remove related entries from matched_vms.json
	_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{}, groupUUID, "remove")

	// Define empty mapping file path for backward compatibility
	mappingFile := ""

	// Delete rule-related table data
	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, routes.RuleTypeBW); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}

	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Failed to reload Prometheus: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload Prometheus"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": []string{mappingFile},
			"linked_vms":    linkedVMs,
		},
	})
}

func (a *AlarmAPI) CreateNodeAlarmRule(c *gin.Context) {
	var rule model.NodeAlarmRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if rule.RuleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_type is required"})
		return
	}
	if rule.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if len(rule.Config.RawMessage) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config is required"})
		return
	}
	var temp interface{}
	if err := json.Unmarshal(rule.Config.RawMessage, &temp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config must be valid JSON"})
		return
	}

	rulePtr, err := a.alarmAdmin.CreateNodeAlarmRule(c.Request.Context(), &rule)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "Rule type already exists",
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   rulePtr,
	})
}

func (a *AlarmAPI) GetNodeAlarmRules(c *gin.Context) {
	uuid := c.Query("uuid")
	ruleType := c.Query("rule_type")

	rules, err := a.alarmAdmin.GetNodeAlarmRules(c.Request.Context(), uuid, ruleType)
	if err != nil {
		log.Printf("Failed to get node alarm rules: error=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get node alarm rules"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   rules,
		"count":  len(rules),
	})
}

func (a *AlarmAPI) DeleteNodeAlarmRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uuid parameter is required"})
		return
	}

	deletedFiles, err := a.alarmAdmin.DeleteNodeAlarmRule(c.Request.Context(), uuid)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":        "success",
		"message":       "node alarm rule deleted successfully",
		"uuid":          uuid,
		"deleted_files": deletedFiles,
	})
}

// VMAlarmMapping is used for serialization to vm_alarm_mapping.yml
type VMAlarmMapping struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}
