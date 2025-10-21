package apis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bytes"
	"io"
	"web/src/common"
	"web/src/dbs"
	"web/src/model"
	"web/src/routes"
)

// AdjustAPI Resource auto-adjustment API
type AdjustAPI struct {
	operator *routes.AdjustOperator
}

// Global variable
var adjustAPI = &AdjustAPI{
	operator: &routes.AdjustOperator{},
}

// Resource adjustment configuration file path constants
const (
	PrometheusBasePath = "/etc/prometheus"
	// Do not re-declare RulesGeneralPath, RulesSpecialPath, RulesEnabledPath here

	// Template files
	CPUAdjustRuleTemplate        = "VM-cpu-adjust-rule.yml.j2"
	ResourceAdjustAlertsTemplate = "resource-adjustment-alerts.yml.j2"
	InBWAdjustRuleTemplate       = "VM-in-bw-adjust-rule.yml.j2"
	OutBWAdjustRuleTemplate      = "VM-out-bw-adjust-rule.yml.j2"
)

// CreateCPUAdjustRule creates CPU adjustment rule
func (a *AdjustAPI) CreateCPUAdjustRule(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required"`
		Owner    string `json:"owner" binding:"required"`
		Email    string `json:"email"`
		RegionID string `json:"region_id" binding:"required"`
		RuleID   string `json:"rule_id" binding:"required"`
		Rules    []struct {
			Name            string  `json:"name"`
			HighThreshold   float64 `json:"high_threshold"`
			LowThreshold    float64 `json:"low_threshold"`
			SmoothWindow    int     `json:"smooth_window"`
			TriggerDuration int     `json:"trigger_duration"`
			RestoreDuration int     `json:"restore_duration"`
			LimitPercent    int     `json:"limit_percent"`
		} `json:"rules"`
		LinkedVMs []string `json:"linkedvms"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if rules are provided
	if len(req.Rules) == 0 {
		log.Printf("[ADJUST-ERROR] No rules provided")
		c.JSON(http.StatusBadRequest, gin.H{"error": "No rules provided"})
		return
	}

	// Currently only support one rule
	if len(req.Rules) > 1 {
		log.Printf("[ADJUST-ERROR] Currently only one rule is supported")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Currently only one rule is supported"})
		return
	}

	// Check if the owner is admin
	if req.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can create adjustment rules")
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can create adjustment rules"})
		return
	}

	// Create rule group
	group := &model.AdjustRuleGroup{
		Name:          req.Name,
		Type:          model.RuleTypeAdjustCPU,
		Owner:         req.Owner,
		Enabled:       true,
		Email:         "",
		AdjustEnabled: true, // Always set to true
		RegionID:      req.RegionID,
		RuleID:        req.RuleID,
	}

	if err := a.operator.CreateAdjustRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule group failed: " + err.Error()})
		return
	}

	// Process each rule (currently only one)
	for _, rule := range req.Rules {
		// Create rule details
		detail := &model.CPUAdjustRuleDetail{
			GroupUUID:       group.UUID,
			Name:            rule.Name,
			HighThreshold:   rule.HighThreshold,
			LowThreshold:    rule.LowThreshold,
			SmoothWindow:    rule.SmoothWindow,
			TriggerDuration: rule.TriggerDuration,
			RestoreDuration: rule.RestoreDuration,
			LimitPercent:    rule.LimitPercent,
		}

		// Validate required parameters - no default values allowed
		if detail.HighThreshold == 0 {
			log.Printf("[ADJUST-ERROR] High threshold cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "High threshold cannot be zero"})
			return
		}
		if detail.LowThreshold == 0 {
			log.Printf("[ADJUST-ERROR] Low threshold cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Low threshold cannot be zero"})
			return
		}
		if detail.SmoothWindow == 0 {
			log.Printf("[ADJUST-ERROR] Smooth window cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Smooth window cannot be zero"})
			return
		}
		if detail.TriggerDuration == 0 {
			log.Printf("[ADJUST-ERROR] Trigger duration cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Trigger duration cannot be zero"})
			return
		}
		if detail.RestoreDuration == 0 {
			log.Printf("[ADJUST-ERROR] Restore duration cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Restore duration cannot be zero"})
			return
		}
		if detail.LimitPercent == 0 {
			log.Printf("[ADJUST-ERROR] CPU limit percentage cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "CPU limit percentage cannot be zero"})
			return
		}

		if err := a.operator.CreateCPUAdjustRuleDetail(c.Request.Context(), detail); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}

		// Generate rule files
		ruleData := map[string]interface{}{
			"rule_group":          strings.ReplaceAll(group.UUID, "-", "_"),
			"rule_group_original": group.UUID,
			"global_rule_id":      group.RuleID, // User-specified global rule ID
			"high_threshold":      detail.HighThreshold,
			"low_threshold":       detail.LowThreshold,
			"smooth_window":       detail.SmoothWindow,
			"trigger_duration":    detail.TriggerDuration,
			"restore_duration":    detail.RestoreDuration,
			"owner":               req.Owner,
			"email":               "",
			"adjust_enabled":      true,
		}

		// Generate record rules
		if err := routes.ProcessTemplate(CPUAdjustRuleTemplate, fmt.Sprintf("cpu-adjust-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
			log.Printf("Failed to render CPU adjust rule: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render CPU adjust rule"})
			return
		}

		// Generate alert rules
		if err := routes.ProcessTemplate(ResourceAdjustAlertsTemplate, fmt.Sprintf("resource-adjust-alerts-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
			log.Printf("Failed to render resource adjustment alerts: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render resource adjustment alerts"})
			return
		}
	}

	// Link VMs
	if len(req.LinkedVMs) > 0 {
		// Use existing link function
		alarmOperator := &routes.AlarmOperator{}
		_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")

		// Update matched_vms.json
		alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
		_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add", "adjust-cpu")
	}

	// Reload Prometheus
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

// GetCPUAdjustRules gets CPU adjustment rules
func (a *AdjustAPI) GetCPUAdjustRules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	identifier := c.Param("uuid")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	// Check permission: only admin can view adjustment rules
	// TODO: Get current user from authentication info
	currentUser := "admin" // Temporary setting, should get from authentication
	if currentUser != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can view adjustment rules, user: %s", currentUser)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can view adjustment rules"})
		return
	}

	queryParams := routes.ListAdjustRuleGroupsParams{
		RuleType: model.RuleTypeAdjustCPU,
		Page:     page,
		PageSize: pageSize,
	}

	if identifier != "" {
		// Dual identifier query: try rule_id first, then group_uuid
		queryParams.RuleID = identifier
		queryParams.GroupUUID = identifier
		queryParams.PageSize = 1
	}

	groups, total, err := a.operator.ListAdjustRuleGroups(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query rules group failed: " + err.Error()})
		return
	}

	responseData := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		details, err := a.operator.GetCPUAdjustRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "get cpu adjust rule details failed: " + err.Error()})
			return
		}

		linkedVMs := make([]string, 0)
		alarmOperator := &routes.AlarmOperator{}
		vmLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err == nil {
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, link.VMUUID)
			}
		}

		// Build rules data
		rules := make([]gin.H, 0, len(details))
		for _, detail := range details {
			rules = append(rules, gin.H{
				"name":             detail.Name,
				"high_threshold":   detail.HighThreshold,
				"low_threshold":    detail.LowThreshold,
				"smooth_window":    detail.SmoothWindow,
				"trigger_duration": detail.TriggerDuration,
				"restore_duration": detail.RestoreDuration,
				"limit_percent":    detail.LimitPercent,
			})
		}

		// Get recent adjustment history
		history, _ := a.operator.GetAdjustmentHistory(c.Request.Context(), group.UUID, 5)
		historyData := make([]gin.H, 0, len(history))
		for _, h := range history {
			historyData = append(historyData, gin.H{
				"domain":      h.DomainName,
				"action_type": h.ActionType,
				"status":      h.Status,
				"adjust_time": h.AdjustTime.Format(time.RFC3339),
				"details":     h.Details,
			})
		}

		responseData = append(responseData, gin.H{
			"group_uuid":  group.UUID,
			"name":        group.Name,
			"owner":       group.Owner,
			"enabled":     group.Enabled,
			"create_time": group.CreatedAt.Format(time.RFC3339),
			"region_id":   group.RegionID,
			"rule_id":     group.RuleID,
			"rules":       rules,
			"linked_vms":  linkedVMs,
			"history":     historyData,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data": responseData,
		"meta": gin.H{
			"current_page": page,
			"per_page":     pageSize,
			"total":        total,
			"total_pages":  (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// DeleteCPUAdjustRule deletes CPU adjustment rule
func (a *AdjustAPI) DeleteCPUAdjustRule(c *gin.Context) {
	identifier := c.Param("uuid")
	if identifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Identifier is required", "code": "MISSING_IDENTIFIER"})
		return
	}

	group, err := a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), identifier)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found", "code": "NOT_FOUND", "identifier": identifier})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}

	if group.Type != model.RuleTypeAdjustCPU {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type", "code": "INVALID_RULE_TYPE"})
		return
	}

	// Check permission: only admin can delete adjustment rules
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can delete adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can delete adjustment rules"})
		return
	}

	// Delete linked VMs
	alarmOperator := &routes.AlarmOperator{}
	_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")

	// Update matched_vms.json
	alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, group.UUID, "remove", "adjust-cpu")

	// Determine file paths
	var rulePath, alertPath string
	if group.Owner == "admin" {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
	} else {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
	}

	// Determine symlink paths
	ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

	// Delete symlinks and rule files
	deletedFiles := []string{}

	// Delete symlinks
	if err := routes.RemoveFile(ruleLinkPath); err == nil {
		deletedFiles = append(deletedFiles, ruleLinkPath)
		log.Printf("[ADJUST-INFO] Removed symlink: %s", ruleLinkPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove symlink %s: %v", ruleLinkPath, err)
	}

	if err := routes.RemoveFile(alertLinkPath); err == nil {
		deletedFiles = append(deletedFiles, alertLinkPath)
		log.Printf("[ADJUST-INFO] Removed symlink: %s", alertLinkPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove symlink %s: %v", alertLinkPath, err)
	}

	// Delete rule files
	if err := routes.RemoveFile(rulePath); err == nil {
		deletedFiles = append(deletedFiles, rulePath)
		log.Printf("[ADJUST-INFO] Removed file: %s", rulePath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove file %s: %v", rulePath, err)
	}

	if err := routes.RemoveFile(alertPath); err == nil {
		deletedFiles = append(deletedFiles, alertPath)
		log.Printf("[ADJUST-INFO] Removed file: %s", alertPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove file %s: %v", alertPath, err)
	}

	// Restore CPU resources for all linked VMs
	log.Printf("[ADJUST-INFO] Restoring CPU resources for all linked VMs before rule deletion: %s", group.UUID)
	vmLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
	if err != nil {
		log.Printf("[ADJUST-WARNING] Failed to get linked VMs for CPU restore: %v", err)
	} else {
		// Restore CPU resources for each VM
		for _, link := range vmLinks {
			// Get VM domain information
			domain, err := routes.GetDomainByInstanceUUID(c.Request.Context(), link.VMUUID)
			if err != nil {
				log.Printf("[ADJUST-WARNING] Failed to get domain for VM %s: %v", link.VMUUID, err)
				continue
			}

			// Create restore record
			record := &routes.AdjustmentRecord{
				RuleGroupUUID: group.UUID,
				AdjustType:    "restore_cpu",
			}

			// Restore CPU resources
			err = a.operator.RestoreCPUResource(c.Request.Context(), record, domain, link.VMUUID)
			if err != nil {
				log.Printf("[ADJUST-WARNING] Failed to restore CPU for VM %s: %v", link.VMUUID, err)
			} else {
				log.Printf("[ADJUST-INFO] Successfully restored CPU for VM %s", link.VMUUID)
			}
		}
	}

	// Clean up adjustment status metrics on compute nodes
	log.Printf("[ADJUST-INFO] Cleaning up CPU adjustment metrics for rule: %s", group.UUID)
	if err := a.cleanupRuleMetricsOnNodes(c.Request.Context(), group.UUID, "cpu"); err != nil {
		log.Printf("[ADJUST-WARNING] Failed to cleanup rule metrics: %v", err)
	}

	// Delete database records
	err = a.operator.DeleteAdjustRuleGroupWithDependencies(c.Request.Context(), group.UUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete rule: " + err.Error()})
		return
	}

	// Reload Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("[ADJUST-WARNING] Failed to reload Prometheus: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":    group.UUID,
			"rule_id":       group.RuleID,
			"deleted_files": deletedFiles,
		},
	})
}

// EnableAdjustRule enables resource adjustment rule
func (a *AdjustAPI) EnableAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	// Smart recognition: try as rule_id first, then as group_uuid
	var group *model.AdjustRuleGroup
	var err error
	var identifierType string

	// 1. Try as rule_id first
	group, err = a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), uuid)
	if err == nil {
		identifierType = "rule_id"
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// 2. If rule_id not found, try as group_uuid
		group, err = a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
		if err == nil {
			identifierType = "group_uuid"
		}
	}

	// Unified error handling
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get rule information"})
		return
	}

	// Check permission: only admin can enable adjustment rules
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can enable adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can enable adjustment rules"})
		return
	}

	// Set enabled status
	group.AdjustEnabled = true

	if err := dbs.DB().Save(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enable rule"})
		return
	}

	// Determine file paths based on rule type
	var rulePaths []string
	var alertPath string

	if group.Type == model.RuleTypeAdjustCPU {
		var rulePath string
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
		} else {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
		}
		rulePaths = append(rulePaths, rulePath)
	} else if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		// Get bandwidth adjustment rule details, generate file paths for each direction
		details, err := a.operator.GetBWAdjustRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			log.Printf("[ADJUST-ERROR] Failed to get BW adjust rule details: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get rule details"})
			return
		}

		for _, detail := range details {
			var filename string
			switch detail.Direction {
			case "in":
				filename = fmt.Sprintf("bw-in-adjust-%s-%s.yml", group.Owner, group.UUID)
			case "out":
				filename = fmt.Sprintf("bw-out-adjust-%s-%s.yml", group.Owner, group.UUID)
			}

			var rulePath string
			if group.Owner == "admin" {
				rulePath = fmt.Sprintf("%s/%s", routes.RulesGeneral, filename)
			} else {
				rulePath = fmt.Sprintf("%s/%s", routes.RulesSpecial, filename)
			}
			rulePaths = append(rulePaths, rulePath)
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported rule type"})
		return
	}

	// Alert file path
	if group.Owner == "admin" {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
	} else {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
	}

	log.Printf("[ADJUST-INFO] Enabling adjustment rule: identifier=%s, type=%s, group_uuid=%s, rule_id=%s", uuid, group.Type, group.UUID, group.RuleID)

	// Create rule file symlinks
	var createdLinks []string
	for _, rulePath := range rulePaths {
		ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))

		// Use routes.CreateSymlink instead of os.Symlink to support remote Prometheus server
		if err := routes.CreateSymlink(rulePath, ruleLinkPath); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to create rule symlink: %v", err)
			for _, link := range createdLinks {
				routes.RemoveFile(link)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create rule symlink"})
			return
		}
		createdLinks = append(createdLinks, ruleLinkPath)
	}

	// Create alert file symlink
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))
	if err := routes.CreateSymlink(alertPath, alertLinkPath); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to create alert symlink: %v", err)
		for _, link := range createdLinks {
			routes.RemoveFile(link)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create alert symlink"})
		return
	}

	// Reload Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
	routes.ReloadPrometheus()

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":      group.UUID,
			"rule_id":         group.RuleID,
			"enabled":         true,
			"identifier_type": identifierType,
		},
	})
}

// DisableAdjustRule disables resource adjustment rule
func (a *AdjustAPI) DisableAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	// Smart recognition: try as rule_id first, then as group_uuid
	var group *model.AdjustRuleGroup
	var err error
	var identifierType string

	// 1. Try as rule_id first
	group, err = a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), uuid)
	if err == nil {
		identifierType = "rule_id"
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// 2. If rule_id not found, try as group_uuid
		group, err = a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
		if err == nil {
			identifierType = "group_uuid"
		}
	}

	// Unified error handling
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}

	// Check permission: only admin can disable adjustment rules
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can disable adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can disable adjustment rules"})
		return
	}

	// Update enabled status
	group.Enabled = false
	group.AdjustEnabled = false

	if err := dbs.DB().Save(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disable rule"})
		return
	}

	// Determine file paths based on rule type
	var rulePaths []string
	var alertPath string

	if group.Type == model.RuleTypeAdjustCPU {
		var rulePath string
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
		} else {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
		}
		rulePaths = append(rulePaths, rulePath)
	} else if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		// Get bandwidth adjustment rule details, generate file paths for each direction
		details, err := a.operator.GetBWAdjustRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			log.Printf("[ADJUST-ERROR] Failed to get BW adjust rule details: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get rule details"})
			return
		}

		for _, detail := range details {
			var filename string
			switch detail.Direction {
			case "in":
				filename = fmt.Sprintf("bw-in-adjust-%s-%s.yml", group.Owner, group.UUID)
			case "out":
				filename = fmt.Sprintf("bw-out-adjust-%s-%s.yml", group.Owner, group.UUID)
			}

			var rulePath string
			if group.Owner == "admin" {
				rulePath = fmt.Sprintf("%s/%s", routes.RulesGeneral, filename)
			} else {
				rulePath = fmt.Sprintf("%s/%s", routes.RulesSpecial, filename)
			}
			rulePaths = append(rulePaths, rulePath)
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported rule type"})
		return
	}

	// Alert file path
	if group.Owner == "admin" {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
	} else {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
	}

	log.Printf("[ADJUST-INFO] Disabling adjustment rule: identifier=%s, type=%s, group_uuid=%s, rule_id=%s", uuid, group.Type, group.UUID, group.RuleID)

	// Delete rule file symlinks
	for _, rulePath := range rulePaths {
		ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))

		// Use routes.RemoveFile instead of os.Remove to support remote Prometheus server
		if err := routes.RemoveFile(ruleLinkPath); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to remove rule symlink: %v", err)
		}
	}

	// Delete alert file symlink
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))
	if err := routes.RemoveFile(alertLinkPath); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to remove alert symlink: %v", err)
	}

	// Reload Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
	routes.ReloadPrometheus()

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":      group.UUID,
			"rule_id":         group.RuleID,
			"enabled":         false,
			"identifier_type": identifierType,
		},
	})
}

// ProcessResourceAdjustmentWebhook processes resource auto-adjustment webhook
func (a *AdjustAPI) ProcessResourceAdjustmentWebhook(c *gin.Context) {
	// Read request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to read request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request", "code": "REQUEST_READ_ERROR"})
		return
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	// Parse alert data
	var req routes.AlertWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to parse request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert format", "code": "INVALID_FORMAT"})
		return
	}

	log.Printf("[ADJUST-INFO] Processing %d adjustment alert(s) with status: %s", len(req.Alerts), req.Status)

	// Track processing results
	successCount := 0
	failedCount := 0

	// Process each alert
	for i, alert := range req.Alerts {
		// Create request tracking ID
		requestID := fmt.Sprintf("adjust-%s-%d", time.Now().Format("20060102-150405"), i)

		// Process alert adjustment
		result := a.processAlertAdjustment(c.Request.Context(), alert, requestID)
		if result {
			successCount++
		} else {
			failedCount++
		}
		// Send realtime notification
		if err := a.operator.SendAdjustmentNotification(c.Request.Context(), alert, result); err != nil {
			log.Printf("[ADJUST-WARNING] Failed to send notification for alert %d: %v", i+1, err)
		}
	}

	log.Printf("[ADJUST-INFO] Adjustment completed: total=%d, success=%d, failed=%d",
		len(req.Alerts), successCount, failedCount)

	c.JSON(http.StatusOK, gin.H{
		"status":        "success",
		"total_alerts":  len(req.Alerts),
		"success_count": successCount,
		"failed_count":  failedCount,
		"message":       "Resource adjustment processing completed",
		"processed_at":  time.Now().Format(time.RFC3339),
	})
}

// processAlertAdjustment processes single alert adjustment logic
func (a *AdjustAPI) processAlertAdjustment(ctx context.Context, alert routes.AdjustAlert, requestID string) bool {
	startTime := time.Now()

	// Extract all labels for subsequent use
	domain := alert.Labels["domain"]
	ruleID := alert.Labels["rule_id"]
	actionType := alert.Labels["action_type"]
	ruleGroup := alert.Labels["rule_group"]
	instanceID := alert.Labels["instance_id"]

	// Parameter validation
	if domain == "" || ruleID == "" || actionType == "" {
		log.Printf("[ADJUST-%s] Missing required parameters: domain=%s, ruleID=%s, actionType=%s",
			requestID, domain, ruleID, actionType)
		return false
	}

	log.Printf("[ADJUST-%s] Processing %s for domain=%s, rule=%s", requestID, actionType, domain, ruleID)

	// Create adjustment record
	record := &routes.AdjustmentRecord{
		Name:          alert.Labels["alertname"],
		RuleGroupUUID: ruleGroup,
		Summary:       alert.Annotations["summary"],
		Description:   alert.Annotations["description"],
		StartsAt:      alert.StartsAt,
		AdjustType:    actionType,
		TargetDevice:  alert.Labels["target_device"],
	}

	// Record adjustment history
	history := &model.AdjustmentHistory{
		DomainName: domain,
		RuleID:     ruleID,
		GroupUUID:  ruleGroup,
		ActionType: actionType,
		Status:     "processing",
		Details:    fmt.Sprintf("Processing %s (domain: %s)", actionType, domain),
		AdjustTime: time.Now(),
	}

	// Execute resource adjustment based on action type
	var err error
	switch actionType {
	case "limit_cpu":
		err = a.operator.AdjustCPUResource(ctx, record, domain, true, instanceID)
	case "restore_cpu":
		err = a.operator.RestoreCPUResource(ctx, record, domain, instanceID)
	case "limit_in_bw":
		err = a.operator.AdjustBandwidthResource(ctx, record, domain, record.TargetDevice, true, instanceID)
	case "restore_in_bw":
		err = a.operator.RestoreBandwidthResource(ctx, record, domain, record.TargetDevice, instanceID)
	case "limit_out_bw":
		err = a.operator.AdjustBandwidthResource(ctx, record, domain, record.TargetDevice, true, instanceID)
	case "restore_out_bw":
		err = a.operator.RestoreBandwidthResource(ctx, record, domain, record.TargetDevice, instanceID)
	default:
		log.Printf("[ADJUST-%s] Unknown adjustment type: %s", requestID, actionType)
		history.Status = "failed"
		history.Details = fmt.Sprintf("Unknown adjustment type: %s", actionType)
		a.operator.SaveAdjustmentHistory(ctx, history)
		return false
	}

	// Update history record status
	if err != nil {
		log.Printf("[ADJUST-%s] Failed: %v", requestID, err)
		history.Status = "failed"
		history.Details = fmt.Sprintf("Processing %s failed: %v", actionType, err)
	} else {
		log.Printf("[ADJUST-%s] Completed successfully (%.2fs)", requestID, time.Since(startTime).Seconds())
		history.Status = "completed"
		history.Details = fmt.Sprintf("Successfully processed %s", actionType)
	}

	// Save adjustment history
	a.operator.SaveAdjustmentHistory(ctx, history)

	return err == nil
}

// CreateBWAdjustRule creates bandwidth adjustment rule
func (a *AdjustAPI) CreateBWAdjustRule(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required"`
		Owner    string `json:"owner" binding:"required"`
		RegionID string `json:"region_id" binding:"required"`
		RuleID   string `json:"rule_id" binding:"required"`
		Rules    []struct {
			Name            string `json:"name" binding:"required"`
			Enabled         bool   `json:"enabled"`
			Direction       string `json:"direction" binding:"required,oneof=in out"`
			HighThreshold   int64  `json:"high_threshold" binding:"required,min=1"`
			LowThreshold    int64  `json:"low_threshold" binding:"required,min=1"`
			SmoothWindow    int    `json:"smooth_window" binding:"required,min=1"`
			TriggerDuration int    `json:"trigger_duration" binding:"required,min=1"`
			RestoreDuration int    `json:"restore_duration" binding:"required,min=1"`
			LimitValue      int    `json:"limit_value" binding:"required,min=1"`
		} `json:"rules" binding:"required,min=1"`
		LinkedVMs []struct {
			VMUUID       string `json:"vm_uuid"`
			TargetDevice string `json:"target_device"`
		} `json:"linkedvms"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Parameter parsing failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if rules are provided
	if len(req.Rules) == 0 {
		log.Printf("[ADJUST-ERROR] No rules provided")
		c.JSON(http.StatusBadRequest, gin.H{"error": "No rules provided"})
		return
	}

	// Check if the owner is admin
	if req.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can create adjustment rules")
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can create adjustment rules"})
		return
	}

	// Validate rules
	for _, rule := range req.Rules {
		if !rule.Enabled {
			continue // Skip disabled rules
		}

		// Validate threshold logic
		if rule.LowThreshold >= rule.HighThreshold {
			log.Printf("[ADJUST-ERROR] Low threshold must be less than high threshold")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Low threshold must be less than high threshold"})
			return
		}
	}

	// Create rule group - use the first enabled rule's direction as primary type
	var ruleType string
	for _, rule := range req.Rules {
		if rule.Enabled {
			if rule.Direction == "in" {
				ruleType = model.RuleTypeAdjustInBW
			} else {
				ruleType = model.RuleTypeAdjustOutBW
			}
			break
		}
	}

	group := &model.AdjustRuleGroup{
		Name:          req.Name,
		Type:          ruleType,
		Owner:         req.Owner,
		Enabled:       true,
		Email:         "",   // Email notifications handled by other modules
		AdjustEnabled: true, // Always set to true
		RegionID:      req.RegionID,
		RuleID:        req.RuleID,
	}

	log.Printf("[ADJUST-INFO] Creating bandwidth adjustment rule group: name=%s, type=%s", req.Name, ruleType)
	if err := a.operator.CreateAdjustRuleGroup(c.Request.Context(), group); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to create rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule group failed: " + err.Error()})
		return
	}

	// Process each rule
	for _, rule := range req.Rules {
		if !rule.Enabled {
			continue // Skip disabled rules
		}

		// Create rule details
		detail := &model.BWAdjustRuleDetail{
			GroupUUID:       group.UUID,
			Name:            rule.Name,
			Direction:       rule.Direction,
			HighThreshold:   rule.HighThreshold,
			LowThreshold:    rule.LowThreshold,
			SmoothWindow:    rule.SmoothWindow,
			TriggerDuration: rule.TriggerDuration,
			RestoreDuration: rule.RestoreDuration,
			LimitValue:      rule.LimitValue,
		}

		if err := a.operator.CreateBWAdjustRuleDetail(c.Request.Context(), detail); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to create rule detail: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}

		// Generate rule files
		ruleData := map[string]interface{}{
			"rule_group":          strings.ReplaceAll(group.UUID, "-", "_"),
			"rule_group_original": group.UUID,
			"global_rule_id":      group.RuleID,
			"high_threshold":      detail.HighThreshold,
			"low_threshold":       detail.LowThreshold,
			"smooth_window":       detail.SmoothWindow,
			"trigger_duration":    detail.TriggerDuration,
			"restore_duration":    detail.RestoreDuration,
			"owner":               req.Owner,
			"email":               "",
			"adjust_enabled":      true,
		}

		// Generate record rules based on direction
		var template, filename string
		switch rule.Direction {
		case "in":
			template = InBWAdjustRuleTemplate
			filename = fmt.Sprintf("bw-in-adjust-%s-%s.yml", req.Owner, group.UUID)
			ruleData["in_enabled"] = true
			ruleData["in_high_threshold"] = detail.HighThreshold
			ruleData["in_low_threshold"] = detail.LowThreshold
			ruleData["out_enabled"] = false
		case "out":
			template = OutBWAdjustRuleTemplate
			filename = fmt.Sprintf("bw-out-adjust-%s-%s.yml", req.Owner, group.UUID)
			ruleData["out_enabled"] = true
			ruleData["out_high_threshold"] = detail.HighThreshold
			ruleData["out_low_threshold"] = detail.LowThreshold
			ruleData["in_enabled"] = false
		}

		if err := routes.ProcessTemplate(template, filename, ruleData); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to render %s BW adjust rule: %v", rule.Direction, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to render %s BW adjust rule", rule.Direction)})
			return
		}

		// Create symlink for the generated rule file
		var rulePath string
		if req.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/%s", routes.RulesGeneral, filename)
		} else {
			rulePath = fmt.Sprintf("%s/%s", routes.RulesSpecial, filename)
		}
		ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))

		fmt.Printf("wngzhe CreateBWAdjustRule - rulePath: %s, ruleLinkPath: %s", rulePath, ruleLinkPath)
		if err := routes.CreateSymlink(rulePath, ruleLinkPath); err != nil {
			fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create rule symlink: %v", err)
			log.Printf("[ADJUST-ERROR] Failed to create rule symlink: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create rule symlink"})
			return
		}
	}

	// Generate alert rules (once per rule group)
	alertRuleData := map[string]interface{}{
		"rule_group":          strings.ReplaceAll(group.UUID, "-", "_"),
		"rule_group_original": group.UUID,
		"global_rule_id":      group.RuleID,
		"smooth_window":       5, // Default value
		"trigger_duration":    30,
		"restore_duration":    300,
		"owner":               req.Owner,
		"email":               "",
		"adjust_enabled":      true,
	}

	if err := routes.ProcessTemplate(ResourceAdjustAlertsTemplate, fmt.Sprintf("resource-adjust-alerts-%s-%s.yml", req.Owner, group.UUID), alertRuleData); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to render resource adjustment alerts: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render resource adjustment alerts"})
		return
	}

	// Create symlinks for alert files
	var alertPath string
	if req.Owner == "admin" {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, req.Owner, group.UUID)
	} else {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, req.Owner, group.UUID)
	}

	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))
	fmt.Printf("wngzhe CreateBWAdjustRule - alertPath: %s, alertLinkPath: %s", alertPath, alertLinkPath)
	fmt.Printf("wngzhe CreateBWAdjustRule - isRemotePrometheus: %t", routes.IsRemotePrometheus())

	if err := routes.CreateSymlink(alertPath, alertLinkPath); err != nil {
		fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create alert symlink: %v", err)
		log.Printf("[ADJUST-ERROR] Failed to create alert symlink: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create alert symlink"})
		return
	}

	// Link VMs
	if len(req.LinkedVMs) > 0 {
		// Validate LinkedVMs
		for i, vm := range req.LinkedVMs {
			if vm.VMUUID == "" {
				log.Printf("[ADJUST-ERROR] Empty VM UUID at index %d", i)
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("VM UUID cannot be empty at index %d", i)})
				return
			}
		}

		// Use existing link function
		alarmOperator := &routes.AlarmOperator{}

		// Get and delete existing linked VMs
		existingLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err != nil {
			log.Printf("[ADJUST-WARN] Failed to get existing VM links for group %s: %v", group.UUID, err)
		} else {
			for _, link := range existingLinks {
				_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, link.VMUUID, link.Interface)
			}
		}

		// Create new VM links with target device mapping
		for _, vm := range req.LinkedVMs {
			_ = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, []string{vm.VMUUID}, vm.TargetDevice)
		}

		// Update matched_vms.json
		alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
		for _, vm := range req.LinkedVMs {
			_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{vm.VMUUID}, group.UUID, "add", "adjust-bw", vm.TargetDevice)
		}
	}

	// Reload Prometheus
	routes.ReloadPrometheus()

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": group.UUID,
			"region_id":  group.RegionID,
			"rule_id":    group.RuleID,
			"enabled":    true,
			"linkedvms":  req.LinkedVMs,
		},
	})
}

// GetBWAdjustRules gets bandwidth adjustment rules
func (a *AdjustAPI) GetBWAdjustRules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	// Check permission: only admin can view adjustment rules
	// TODO: Get current user from authentication info
	currentUser := "admin" // Temporary setting, should get from authentication
	if currentUser != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can view adjustment rules, user: %s", currentUser)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can view adjustment rules"})
		return
	}

	// Query inbound and outbound bandwidth rules separately
	var groups []model.AdjustRuleGroup
	var total int64
	var err error

	if groupUUID != "" {
		// Dual identifier query: match rule_id first, then group_uuid
		group, err := a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), groupUUID)
		if err == nil && (group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW) {
			groups = []model.AdjustRuleGroup{*group}
			total = 1
		}
	} else {
		// Query inbound and outbound bandwidth rules separately
		inBWParams := routes.ListAdjustRuleGroupsParams{
			RuleType: model.RuleTypeAdjustInBW,
			Page:     page,
			PageSize: pageSize,
		}
		inBWGroups, inBWTotal, inBWErr := a.operator.ListAdjustRuleGroups(c.Request.Context(), inBWParams)

		outBWParams := routes.ListAdjustRuleGroupsParams{
			RuleType: model.RuleTypeAdjustOutBW,
			Page:     page,
			PageSize: pageSize,
		}
		outBWGroups, outBWTotal, outBWErr := a.operator.ListAdjustRuleGroups(c.Request.Context(), outBWParams)

		if inBWErr != nil && outBWErr != nil {
			err = inBWErr
		} else {
			// Merge results
			groups = append(groups, inBWGroups...)
			groups = append(groups, outBWGroups...)
			total = inBWTotal + outBWTotal
		}
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query rules group failed: " + err.Error()})
		return
	}

	responseData := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		details, err := a.operator.GetBWAdjustRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			log.Printf("[ADJUST-ERROR] Failed to get bandwidth adjustment rule details: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "get bw adjust rule details failed: " + err.Error()})
			return
		}

		linkedVMs := make([]string, 0)
		alarmOperator := &routes.AlarmOperator{}
		vmLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err == nil {
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, link.VMUUID)
			}
		}

		// Build rules data
		rules := make([]gin.H, 0, len(details))
		for _, detail := range details {
			rules = append(rules, gin.H{
				"name":             detail.Name,
				"enabled":          true, // All stored rules are enabled
				"direction":        detail.Direction,
				"high_threshold":   detail.HighThreshold,
				"low_threshold":    detail.LowThreshold,
				"smooth_window":    detail.SmoothWindow,
				"trigger_duration": detail.TriggerDuration,
				"restore_duration": detail.RestoreDuration,
				"limit_value":      detail.LimitValue,
			})
		}

		// Get recent adjustment history
		history, _ := a.operator.GetAdjustmentHistory(c.Request.Context(), group.UUID, 5)
		historyData := make([]gin.H, 0, len(history))
		for _, h := range history {
			historyData = append(historyData, gin.H{
				"domain":      h.DomainName,
				"action_type": h.ActionType,
				"status":      h.Status,
				"adjust_time": h.AdjustTime.Format(time.RFC3339),
				"details":     h.Details,
			})
		}

		responseData = append(responseData, gin.H{
			"group_uuid":  group.UUID,
			"name":        group.Name,
			"owner":       group.Owner,
			"enabled":     group.Enabled,
			"create_time": group.CreatedAt.Format(time.RFC3339),
			"region_id":   group.RegionID,
			"rule_id":     group.RuleID,
			"rules":       rules,
			"linked_vms":  linkedVMs,
			"history":     historyData,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data": responseData,
		"meta": gin.H{
			"current_page": page,
			"per_page":     pageSize,
			"total":        total,
			"total_pages":  (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// DeleteBWAdjustRule deletes bandwidth adjustment rule
func (a *AdjustAPI) DeleteBWAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required", "code": "MISSING_UUID"})
		return
	}

	group, err := a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found", "code": "NOT_FOUND"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}

	if group.Type != model.RuleTypeAdjustInBW && group.Type != model.RuleTypeAdjustOutBW {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type", "code": "INVALID_RULE_TYPE"})
		return
	}

	// Check permission: only admin can delete adjustment rules
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can delete adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can delete adjustment rules"})
		return
	}

	// Delete linked VMs
	alarmOperator := &routes.AlarmOperator{}
	_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")

	// Update matched_vms.json
	alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, group.UUID, "remove", "adjust-bw")

	// Get rule details to determine files to delete
	details, err := a.operator.GetBWAdjustRuleDetails(c.Request.Context(), group.UUID)
	if err != nil {
		log.Printf("[ADJUST-WARNING] Failed to get rule details for file cleanup: %v", err)
		details = []model.BWAdjustRuleDetail{}
	}

	// Determine alert file path
	var alertPath string
	if group.Owner == "admin" {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
	} else {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, group.UUID)
	}
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

	// Delete symlinks and rule files
	deletedFiles := []string{}

	// Delete file for each rule
	for _, detail := range details {
		var filename string
		switch detail.Direction {
		case "in":
			filename = fmt.Sprintf("bw-in-adjust-%s-%s.yml", group.Owner, group.UUID)
		case "out":
			filename = fmt.Sprintf("bw-out-adjust-%s-%s.yml", group.Owner, group.UUID)
		}

		var rulePath string
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/%s", routes.RulesGeneral, filename)
		} else {
			rulePath = fmt.Sprintf("%s/%s", routes.RulesSpecial, filename)
		}
		ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filename)

		// Delete symlink
		if err := routes.RemoveFile(ruleLinkPath); err == nil {
			deletedFiles = append(deletedFiles, ruleLinkPath)
			log.Printf("[ADJUST-INFO] Removed symlink: %s", ruleLinkPath)
		} else {
			log.Printf("[ADJUST-ERROR] Failed to remove symlink %s: %v", ruleLinkPath, err)
		}

		// Delete rule file
		if err := routes.RemoveFile(rulePath); err == nil {
			deletedFiles = append(deletedFiles, rulePath)
			log.Printf("[ADJUST-INFO] Removed file: %s", rulePath)
		} else {
			log.Printf("[ADJUST-ERROR] Failed to remove file %s: %v", rulePath, err)
		}
	}

	// Delete alert rule files
	if err := routes.RemoveFile(alertLinkPath); err == nil {
		deletedFiles = append(deletedFiles, alertLinkPath)
		log.Printf("[ADJUST-INFO] Removed symlink: %s", alertLinkPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove symlink %s: %v", alertLinkPath, err)
	}
	if err := routes.RemoveFile(alertPath); err == nil {
		deletedFiles = append(deletedFiles, alertPath)
		log.Printf("[ADJUST-INFO] Removed file: %s", alertPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove file %s: %v", alertPath, err)
	}

	// Restore bandwidth resources for all linked VMs
	log.Printf("[ADJUST-INFO] Restoring bandwidth resources for all linked VMs before rule deletion: %s", group.UUID)
	vmLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
	if err != nil {
		log.Printf("[ADJUST-WARNING] Failed to get linked VMs for bandwidth restore: %v", err)
	} else {
		// Restore bandwidth resources for each VM
		for _, link := range vmLinks {
			// Get VM domain information
			domain, err := routes.GetDomainByInstanceUUID(c.Request.Context(), link.VMUUID)
			if err != nil {
				log.Printf("[ADJUST-WARNING] Failed to get domain for VM %s: %v", link.VMUUID, err)
				continue
			}

			// Create restore record
			record := &routes.AdjustmentRecord{
				RuleGroupUUID: group.UUID,
				AdjustType:    "restore_bw",
				TargetDevice:  link.Interface,
			}

			// Restore bandwidth resources
			err = a.operator.RestoreBandwidthResource(c.Request.Context(), record, domain, link.Interface, link.VMUUID)
			if err != nil {
				log.Printf("[ADJUST-WARNING] Failed to restore bandwidth for VM %s: %v", link.VMUUID, err)
			} else {
				log.Printf("[ADJUST-INFO] Successfully restored bandwidth for VM %s", link.VMUUID)
			}
		}
	}

	// Clean up adjustment status metrics on compute nodes
	log.Printf("[ADJUST-INFO] Cleaning up bandwidth adjustment metrics for rule: %s", group.UUID)
	if err := a.cleanupRuleMetricsOnNodes(c.Request.Context(), group.UUID, "bandwidth"); err != nil {
		log.Printf("[ADJUST-WARNING] Failed to cleanup rule metrics: %v", err)
	}

	// Delete database records
	err = a.operator.DeleteAdjustRuleGroupWithDependencies(c.Request.Context(), group.UUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to delete rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete rule: " + err.Error()})
		return
	}

	// Reload Prometheus
	routes.ReloadPrometheus()
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":    group.UUID,
			"rule_id":       group.RuleID,
			"deleted_files": deletedFiles,
		},
	})
}

// LinkAdjustRuleRequest Link adjustment rule request
type LinkAdjustRuleRequest struct {
	GroupUUID string `json:"group_uuid" binding:"required"`
	LinkedVMs []struct {
		VMUUID       string `json:"vm_uuid" binding:"required"`
		TargetDevice string `json:"target_device,omitempty"`
	} `json:"linked_vms" binding:"required"`
}

// UnlinkAdjustRuleRequest Unlink adjustment rule request
type UnlinkAdjustRuleRequest struct {
	GroupUUID    string   `json:"group_uuid" binding:"required"`
	VMUUIDs      []string `json:"vm_uuids,omitempty"`
	TargetDevice string   `json:"target_device,omitempty"`
}

// LinkedVMInfo Linked VM information
type LinkedVMInfo struct {
	VMUUID       string `json:"vm_uuid"`
	Domain       string `json:"domain"`
	TargetDevice string `json:"target_device"`
	RuleID       string `json:"rule_id"`
}

// LinkAdjustRule Link VM to adjustment rule group
func (a *AdjustAPI) LinkAdjustRule(c *gin.Context) {
	var req struct {
		GroupUUID string `json:"group_uuid,omitempty"`
		RuleID    string `json:"rule_id,omitempty"`
		VMUUID    string `json:"vm_uuid" binding:"required"`
		Interface string `json:"interface"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Invalid request parameters: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters: " + err.Error()})
		return
	}

	// Validate that either group_uuid or rule_id must be provided
	if req.GroupUUID == "" && req.RuleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "either group_uuid or rule_id must be provided"})
		return
	}

	var group *model.AdjustRuleGroup
	var err error
	var identifier string

	// Prioritize rule_id, use group_uuid if not provided
	if req.RuleID != "" {
		identifier = req.RuleID
		group, err = a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), req.RuleID)
	} else {
		identifier = req.GroupUUID
		group, err = a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
	}

	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get adjustment rule group: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "adjustment rule group not found"})
		return
	}

	log.Printf("[ADJUST-INFO] Linking VM to adjustment rule: identifier=%s, vm_uuid=%s, interface=%s",
		identifier, req.VMUUID, req.Interface)

	// Validate interface parameter based on rule type
	if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		// BW type requires interface parameter
		if req.Interface == "" {
			log.Printf("[ADJUST-ERROR] Interface parameter is required for bandwidth adjustment rules")
			c.JSON(http.StatusBadRequest, gin.H{"error": "interface parameter is required for bandwidth adjustment rules"})
			return
		}
	} else {
		// CPU type doesn't need interface parameter, clear it
		req.Interface = ""
	}

	// Check if VM is already linked to this rule group
	alarmOperator := &routes.AlarmOperator{}
	existingLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
	if err == nil {
		for _, link := range existingLinks {
			if link.VMUUID == req.VMUUID {
				// For BW type, also check if interface is the same
				if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
					if link.Interface == req.Interface {
						log.Printf("[ADJUST-WARN] VM already linked to rule group with same interface: vm_uuid=%s, group_uuid=%s, interface=%s",
							req.VMUUID, group.UUID, req.Interface)
						c.JSON(http.StatusConflict, gin.H{"error": "VM already linked to this rule group with the same interface"})
						return
					}
				} else {
					// CPU type, VM already linked
					log.Printf("[ADJUST-WARN] VM already linked to rule group: vm_uuid=%s, group_uuid=%s", req.VMUUID, group.UUID)
					c.JSON(http.StatusConflict, gin.H{"error": "VM already linked to this rule group"})
					return
				}
			}
		}
	}

	// Create link
	err = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, []string{req.VMUUID}, req.Interface)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to create VM link: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create VM link: " + err.Error()})
		return
	}

	// Update matched_vms.json
	alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
	ruleType := "adjust-cpu"
	if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		ruleType = "adjust-bw"
	}

	err = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{req.VMUUID}, group.UUID, "add", ruleType, req.Interface)
	if err != nil {
		log.Printf("[ADJUST-WARN] Failed to update Prometheus config: %v", err)
		// Don't return error as database operation was successful
	}

	log.Printf("[ADJUST-INFO] Successfully linked VM to adjustment rule: group_uuid=%s, vm_uuid=%s, interface=%s",
		group.UUID, req.VMUUID, req.Interface)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "VM successfully linked to adjustment rule",
		"data": gin.H{
			"group_uuid": group.UUID,
			"rule_id":    group.RuleID,
			"vm_uuid":    req.VMUUID,
			"interface":  req.Interface,
			"rule_type":  group.Type,
		},
	})
}

// UnlinkAdjustRule Unlink VM from adjustment rule
func (a *AdjustAPI) UnlinkAdjustRule(c *gin.Context) {
	var req struct {
		GroupUUID string `json:"group_uuid,omitempty"`
		RuleID    string `json:"rule_id,omitempty"`
		VMUUID    string `json:"vm_uuid" binding:"required"`
		Interface string `json:"interface"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Invalid request parameters: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters: " + err.Error()})
		return
	}

	// Validate that either group_uuid or rule_id must be provided
	if req.GroupUUID == "" && req.RuleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "either group_uuid or rule_id must be provided"})
		return
	}

	var group *model.AdjustRuleGroup
	var err error
	var identifier string

	// Prioritize rule_id, use group_uuid if not provided
	if req.RuleID != "" {
		identifier = req.RuleID
		group, err = a.operator.GetAdjustRulesByIdentifier(c.Request.Context(), req.RuleID)
	} else {
		identifier = req.GroupUUID
		group, err = a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
	}

	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get adjustment rule group: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "adjustment rule group not found"})
		return
	}

	log.Printf("[ADJUST-INFO] Unlinking VM from adjustment rule: identifier=%s, vm_uuid=%s, interface=%s",
		identifier, req.VMUUID, req.Interface)

	// Validate interface parameter based on rule type
	if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		// BW type requires interface parameter
		if req.Interface == "" {
			log.Printf("[ADJUST-ERROR] Interface parameter is required for bandwidth adjustment rules")
			c.JSON(http.StatusBadRequest, gin.H{"error": "interface parameter is required for bandwidth adjustment rules"})
			return
		}
	} else {
		// CPU type doesn't need interface parameter, clear it
		req.Interface = ""
	}

	// Check if VM is linked to this rule group
	alarmOperator := &routes.AlarmOperator{}
	existingLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get linked VMs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get linked VMs: " + err.Error()})
		return
	}

	var linkExists bool
	var vmFoundWithDifferentInterface bool
	var existingInterface string

	for _, link := range existingLinks {
		if link.VMUUID == req.VMUUID {
			// For BW type, also need to check if interface is the same
			if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
				if link.Interface == req.Interface {
					linkExists = true
					break
				} else {
					// VM exists but interface doesn't match
					vmFoundWithDifferentInterface = true
					existingInterface = link.Interface
				}
			} else {
				// CPU type, VM already linked
				linkExists = true
				break
			}
		}
	}

	if !linkExists {
		if vmFoundWithDifferentInterface {
			// VM exists but interface doesn't match, provide specific error message
			log.Printf("[ADJUST-ERROR] VM linked with different interface: vm_uuid=%s, requested_interface=%s, actual_interface=%s",
				req.VMUUID, req.Interface, existingInterface)
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("VM %s is linked to this rule group but with interface '%s', not '%s'. Please use the correct interface value.",
					req.VMUUID, existingInterface, req.Interface),
				"details": gin.H{
					"vm_uuid":             req.VMUUID,
					"requested_interface": req.Interface,
					"actual_interface":    existingInterface,
					"suggestion":          fmt.Sprintf("Use interface '%s' instead of '%s'", existingInterface, req.Interface),
				},
			})
		} else {
			// VM is not linked to this rule group at all
			log.Printf("[ADJUST-WARN] VM not linked to rule group: vm_uuid=%s, group_uuid=%s, interface=%s",
				req.VMUUID, group.UUID, req.Interface)
			c.JSON(http.StatusNotFound, gin.H{"error": "VM not linked to this rule group"})
		}
		return
	}

	// Get VM domain information
	domain, err := routes.GetDomainByInstanceUUID(c.Request.Context(), req.VMUUID)
	vmExists := true
	if err != nil {
		log.Printf("[ADJUST-WARN] Failed to get domain for VM %s (VM may have been deleted): %v", req.VMUUID, err)
		vmExists = false
		domain = "" // Set to empty to avoid subsequent operations using invalid domain
	}

	// Check if VM is currently limited, perform restore if yes
	if vmExists {
		log.Printf("[ADJUST-INFO] Checking if VM is currently being limited: domain=%s", domain)
	} else {
		log.Printf("[ADJUST-INFO] VM %s does not exist, skipping resource restoration", req.VMUUID)
	}

	// Create restore record
	record := &routes.AdjustmentRecord{
		RuleGroupUUID: group.UUID,
		TargetDevice:  req.Interface,
	}

	// Execute corresponding restore operation based on rule type (only if VM exists)
	if vmExists {
		if group.Type == model.RuleTypeAdjustCPU {
			// CPU type: check and restore CPU resources
			log.Printf("[ADJUST-INFO] Checking CPU adjustment status for domain: %s", domain)

			// Check CPU adjustment status metrics
			isCPULimited, err := a.checkVMAdjustmentStatus(c.Request.Context(), domain, "cpu", group.UUID)
			if err != nil {
				log.Printf("[ADJUST-WARN] Failed to check CPU adjustment status: %v", err)
			} else if isCPULimited {
				log.Printf("[ADJUST-INFO] VM is currently CPU limited, performing restore: domain=%s", domain)
				record.AdjustType = "restore_cpu"
				err = a.operator.RestoreCPUResource(c.Request.Context(), record, domain, req.VMUUID)
				if err != nil {
					log.Printf("[ADJUST-WARN] Failed to restore CPU resources: %v", err)
				} else {
					log.Printf("[ADJUST-INFO] Successfully restored CPU resources for domain: %s", domain)
				}
			} else {
				log.Printf("[ADJUST-INFO] VM is not currently CPU limited: domain=%s", domain)
			}
		} else if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
			// Bandwidth type: check and restore bandwidth resources
			log.Printf("[ADJUST-INFO] Checking bandwidth adjustment status for domain: %s, interface: %s", domain, req.Interface)

			// Check bandwidth adjustment status metrics
			isBWLimited, err := a.checkVMAdjustmentStatus(c.Request.Context(), domain, "bandwidth", group.UUID)
			if err != nil {
				log.Printf("[ADJUST-WARN] Failed to check bandwidth adjustment status: %v", err)
			} else if isBWLimited {
				log.Printf("[ADJUST-INFO] VM is currently bandwidth limited, performing restore: domain=%s, interface=%s", domain, req.Interface)
				record.AdjustType = "restore_bw"
				err = a.operator.RestoreBandwidthResource(c.Request.Context(), record, domain, req.Interface, req.VMUUID)
				if err != nil {
					log.Printf("[ADJUST-WARN] Failed to restore bandwidth resources: %v", err)
				} else {
					log.Printf("[ADJUST-INFO] Successfully restored bandwidth resources for domain: %s, interface: %s", domain, req.Interface)
				}
			} else {
				log.Printf("[ADJUST-INFO] VM is not currently bandwidth limited: domain=%s, interface=%s", domain, req.Interface)
			}
		}
	} else {
		log.Printf("[ADJUST-INFO] Skipping resource restoration for deleted VM: %s", req.VMUUID)
	}

	// Delete link
	_, err = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, req.VMUUID, req.Interface)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to delete VM link: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete VM link: " + err.Error()})
		return
	}

	// Clean up custom metrics
	ruleType := "cpu"
	if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		ruleType = "bandwidth"
	}

	log.Printf("[ADJUST-INFO] Cleaning up %s adjustment metrics for rule: %s", ruleType, group.UUID)
	if err := a.cleanupRuleMetricsOnNodes(c.Request.Context(), group.UUID, ruleType); err != nil {
		log.Printf("[ADJUST-WARN] Failed to cleanup rule metrics: %v", err)
		// Don't return error as database operation was successful
	}

	// Update matched_vms.json
	alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
	ruleType = "adjust-cpu"
	if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		ruleType = "adjust-bw"
	}

	err = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{req.VMUUID}, group.UUID, "remove", ruleType, req.Interface)
	if err != nil {
		log.Printf("[ADJUST-WARN] Failed to update Prometheus config: %v", err)
		// Don't return error as database operation was successful
	}

	log.Printf("[ADJUST-INFO] Successfully unlinked VM from adjustment rule: group_uuid=%s, vm_uuid=%s, interface=%s",
		group.UUID, req.VMUUID, req.Interface)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "VM successfully unlinked from adjustment rule",
		"data": gin.H{
			"group_uuid": group.UUID,
			"rule_id":    group.RuleID,
			"vm_uuid":    req.VMUUID,
			"interface":  req.Interface,
		},
	})
}

// checkVMAdjustmentStatus Check if VM is currently being limited
func (a *AdjustAPI) checkVMAdjustmentStatus(ctx context.Context, domain, metricType, ruleGroupUUID string) (bool, error) {
	// Build query statement
	query := ""
	if metricType == "cpu" {
		// Query CPU adjustment status
		query = fmt.Sprintf("vm_cpu_adjustment_status{domain=\"%s\", rule_id=~\"adjust-cpu-.*-%s\"}", domain, ruleGroupUUID)
	} else if metricType == "bandwidth" {
		// Query bandwidth adjustment status
		query = fmt.Sprintf("vm_bandwidth_adjustment_status{domain=\"%s\", rule_id=~\"adjust-bw-.*-%s\"}", domain, ruleGroupUUID)
	} else {
		return false, fmt.Errorf("unsupported metric type: %s", metricType)
	}

	// Build Prometheus URL using configuration from monitor.go
	prometheusURL := fmt.Sprintf("http://%s:%d/api/v1/query", prometheusIP, prometheusPort)

	// Send HTTP request
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", prometheusURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", err)
	}

	q := req.URL.Query()
	q.Add("query", query)
	req.URL.RawQuery = q.Encode()

	log.Printf("[ADJUST-INFO] Querying Prometheus: %s", req.URL.String())

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to query Prometheus: %v", err)
	}
	defer resp.Body.Close()

	// Parse response
	var result struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Values [][]interface{} `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to parse Prometheus response: %v", err)
	}

	// Check if there are results and status is 1 (being limited)
	if result.Status == "success" && len(result.Data.Result) > 0 {
		for _, r := range result.Data.Result {
			if len(r.Values) > 0 && len(r.Values[0]) >= 2 {
				if status, ok := r.Values[0][1].(string); ok && status == "1" {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// cleanupVMMetrics Clean up VM custom metrics
func (a *AdjustAPI) cleanupVMMetrics(ctx context.Context, vmUUID, ruleGroupUUID string, ruleType string) error {
	// Get VM instance information
	instance, err := routes.GetInstanceByUUIDWithAuth(ctx, vmUUID)
	if err != nil {
		return fmt.Errorf("failed to get instance info: %v", err)
	}

	// Get VM domain information
	domain, err := routes.GetDomainByInstanceUUID(ctx, vmUUID)
	if err != nil {
		return fmt.Errorf("failed to get domain for VM: %v", err)
	}

	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// Clean up corresponding metrics based on rule type
	if ruleType == model.RuleTypeAdjustCPU {
		// Clean up CPU adjustment metrics
		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_cpu_adjustment_status.sh --domain '%s' --rule-id '%s' --status 0",
			domain, fmt.Sprintf("%s-%s", domain, ruleGroupUUID))

		err = common.HyperExecute(ctx, control, command)
		if err != nil {
			return fmt.Errorf("failed to cleanup CPU metrics: %v", err)
		}
	} else if ruleType == model.RuleTypeAdjustInBW || ruleType == model.RuleTypeAdjustOutBW {
		// Clean up bandwidth adjustment metrics
		// Need to determine bandwidth type (in/out)
		bwType := "in"
		if ruleType == model.RuleTypeAdjustOutBW {
			bwType = "out"
		}

		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_bandwidth_adjustment_status.sh --domain '%s' --rule-id '%s' --type '%s' --status 0 --target-device '%s'",
			domain, fmt.Sprintf("adjust-bw-%s-%s", domain, ruleGroupUUID), bwType, "unknown")

		err = common.HyperExecute(ctx, control, command)
		if err != nil {
			return fmt.Errorf("failed to cleanup bandwidth metrics: %v", err)
		}
	}

	return nil
}

// GetLinkAdjustRule Get VM link information for adjustment rule
func (a *AdjustAPI) GetLinkAdjustRule(c *gin.Context) {
	groupUUID := c.Query("group_uuid")
	if groupUUID == "" {
		log.Printf("[ADJUST-ERROR] Missing group_uuid parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_uuid parameter is required"})
		return
	}

	log.Printf("[ADJUST-INFO] Getting adjustment rule links: group_uuid=%s", groupUUID)

	// Check if rule group exists
	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get adjustment rule group: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "adjustment rule group not found"})
		return
	}

	// Get linked VM list
	alarmOperator := &routes.AlarmOperator{}
	vmLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get linked VMs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get linked VMs: " + err.Error()})
		return
	}

	// Build response data
	linkedVMs := make([]gin.H, 0, len(vmLinks))
	for _, link := range vmLinks {
		// Get domain information
		domain, err := routes.GetDomainByInstanceUUID(c.Request.Context(), link.VMUUID)
		if err != nil {
			log.Printf("[ADJUST-WARN] Failed to get domain for VM %s: %v", link.VMUUID, err)
			domain = "unknown"
		}

		// Generate rule_id
		var ruleID string
		if group.Type == model.RuleTypeAdjustCPU {
			ruleID = fmt.Sprintf("adjust-cpu-%s-%s", domain, groupUUID)
		} else {
			ruleID = fmt.Sprintf("adjust-bw-%s-%s", domain, groupUUID)
		}

		linkedVMs = append(linkedVMs, gin.H{
			"vm_uuid":       link.VMUUID,
			"domain":        domain,
			"target_device": link.Interface,
			"rule_id":       ruleID,
			"created_at":    link.CreatedAt.Format(time.RFC3339),
			"updated_at":    link.UpdatedAt.Format(time.RFC3339),
		})
	}

	log.Printf("[ADJUST-INFO] Retrieved adjustment rule links: group_uuid=%s, link_count=%d", groupUUID, len(linkedVMs))

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":   groupUUID,
			"rule_type":    group.Type,
			"rule_name":    group.Name,
			"linked_count": len(linkedVMs),
			"linked_vms":   linkedVMs,
		},
	})
}

// cleanupRuleMetricsOnNodes Clean up rule-related metrics on compute nodes
func (a *AdjustAPI) cleanupRuleMetricsOnNodes(ctx context.Context, ruleGroupUUID, ruleType string) error {
	log.Printf("[ADJUST-INFO] Starting cleanup of %s metrics for rule group: %s", ruleType, ruleGroupUUID)

	// Get list of VMs associated with this rule group
	alarmOperator := &routes.AlarmOperator{}
	vmLinks, err := alarmOperator.GetLinkedVMs(ctx, ruleGroupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get linked VMs for rule cleanup: %v", err)
		return fmt.Errorf("failed to get linked VMs: %v", err)
	}

	if len(vmLinks) == 0 {
		log.Printf("[ADJUST-INFO] No VMs linked to rule group %s, no metrics to cleanup", ruleGroupUUID)
		return nil
	}

	log.Printf("[ADJUST-INFO] Found %d VMs linked to rule group %s", len(vmLinks), ruleGroupUUID)

	// Get compute nodes where these VMs are located
	hyperNodes := make(map[int32]bool)
	instanceAdmin := &routes.InstanceAdmin{}
	for _, link := range vmLinks {
		// Get VM instance information to determine its compute node
		instance, err := instanceAdmin.GetInstanceByUUID(ctx, link.VMUUID)
		if err != nil {
			log.Printf("[ADJUST-WARNING] Failed to get instance info for VM %s: %v", link.VMUUID, err)
			continue
		}
		if instance.Hyper > 0 {
			hyperNodes[instance.Hyper] = true
		}
	}

	if len(hyperNodes) == 0 {
		log.Printf("[ADJUST-WARNING] No valid compute nodes found for rule group %s", ruleGroupUUID)
		return nil
	}

	log.Printf("[ADJUST-INFO] Will cleanup metrics on %d compute nodes", len(hyperNodes))

	// Execute cleanup operations on each compute node
	var cleanupErrors []string
	for hyperID := range hyperNodes {
		log.Printf("[ADJUST-INFO] Cleaning up %s metrics on compute node %d for rule %s", ruleType, hyperID, ruleGroupUUID)

		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/cleanup_rule_metrics.sh --rule-id '%s' --type '%s'",
			ruleGroupUUID, ruleType)
		log.Printf("wngzhe[ADJUST-INFO] Cleaning up %s comamnd is  %s", ruleType, command)
		err := common.HyperExecute(ctx, fmt.Sprintf("inter=%d", hyperID), command)
		if err != nil {
			errMsg := fmt.Sprintf("failed to cleanup metrics on node %d: %v", hyperID, err)
			log.Printf("[ADJUST-ERROR] %s", errMsg)
			cleanupErrors = append(cleanupErrors, errMsg)
		} else {
			log.Printf("[ADJUST-INFO] Successfully cleaned up %s metrics on compute node %d", ruleType, hyperID)
		}
	}

	if len(cleanupErrors) > 0 {
		return fmt.Errorf("metrics cleanup failed on some nodes: %s", strings.Join(cleanupErrors, "; "))
	}

	log.Printf("[ADJUST-INFO] Successfully cleaned up %s metrics for rule group %s on all compute nodes", ruleType, ruleGroupUUID)
	return nil
}
