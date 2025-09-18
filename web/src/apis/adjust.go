package apis

import (
	"context"
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
	"web/src/dbs"
	"web/src/model"
	"web/src/routes"
)

// AdjustAPI 资源自动调整API
type AdjustAPI struct {
	operator *routes.AdjustOperator
}

// 全局变量
var adjustAPI = &AdjustAPI{
	operator: &routes.AdjustOperator{},
}

// 资源调整配置文件路径常量
const (
	PrometheusBasePath = "/etc/prometheus"
	// 这里不再重复声明RulesGeneralPath, RulesSpecialPath, RulesEnabledPath

	// 模板文件
	CPUAdjustRuleTemplate        = "VM-cpu-adjust-rule.yml.j2"
	ResourceAdjustAlertsTemplate = "resource-adjustment-alerts.yml.j2"
	InBWAdjustRuleTemplate       = "VM-in-bw-adjust-rule.yml.j2"
	OutBWAdjustRuleTemplate      = "VM-out-bw-adjust-rule.yml.j2"
)

// CreateCPUAdjustRule creates CPU adjustment rule
func (a *AdjustAPI) CreateCPUAdjustRule(c *gin.Context) {
	var req struct {
		Name          string `json:"name" binding:"required"`
		Owner         string `json:"owner" binding:"required"`
		Email         string `json:"email"`
		AdjustEnabled bool   `json:"adjust_enabled"`
		Rules         []struct {
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
		Email:         req.Email,
		AdjustEnabled: req.AdjustEnabled,
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
			"high_threshold":      detail.HighThreshold,
			"low_threshold":       detail.LowThreshold,
			"smooth_window":       detail.SmoothWindow,
			"trigger_duration":    detail.TriggerDuration,
			"restore_duration":    detail.RestoreDuration,
			"owner":               req.Owner,
			"email":               req.Email,
			"adjust_enabled":      req.AdjustEnabled,
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
		_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add", "cpu")
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

// GetCPUAdjustRules 获取CPU调整规则
func (a *AdjustAPI) GetCPUAdjustRules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	// 检查权限：只有admin可以查看调整规则
	// 这里可以从请求中获取用户信息，暂时使用admin检查
	// TODO: 从认证信息中获取当前用户
	currentUser := "admin" // 临时设置，实际应该从认证信息获取
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

	if groupUUID != "" {
		queryParams.GroupUUID = groupUUID
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

		// 构建规则数据
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

		// 获取最近的调整历史
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
			"id":             group.ID,
			"group_uuid":     group.UUID,
			"name":           group.Name,
			"owner":          group.Owner,
			"enabled":        group.Enabled,
			"email":          group.Email,
			"adjust_enabled": group.AdjustEnabled,
			"create_time":    group.CreatedAt.Format(time.RFC3339),
			"rules":          rules,
			"linked_vms":     linkedVMs,
			"history":        historyData,
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

// DeleteCPUAdjustRule 删除CPU调整规则
func (a *AdjustAPI) DeleteCPUAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required", "code": "MISSING_UUID"})
		return
	}

	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found", "code": "NOT_FOUND"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}

	if group.Type != model.RuleTypeAdjustCPU {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type", "code": "INVALID_RULE_TYPE"})
		return
	}

	// 检查权限：只有admin可以删除调整规则
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can delete adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can delete adjustment rules"})
		return
	}

	// 删除链接的VM
	alarmOperator := &routes.AlarmOperator{}
	_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), uuid, "", "")

	// 更新matched_vms.json
	alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, uuid, "remove", "cpu")

	// 确定文件路径
	var rulePath, alertPath string
	if group.Owner == "admin" {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
	} else {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
	}

	// 确定symlink路径
	ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

	// 删除symlink和规则文件
	deletedFiles := []string{}

	// 删除symlink
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

	// 删除规则文件
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

	// 删除数据库记录
	err = a.operator.DeleteAdjustRuleGroupWithDependencies(c.Request.Context(), uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete rule: " + err.Error()})
		return
	}

	// 重新加载Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("[ADJUST-WARNING] Failed to reload Prometheus: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_files": deletedFiles,
			"group_uuid":    uuid,
		},
	})
}

// EnableAdjustRule 启用资源调整规则
func (a *AdjustAPI) EnableAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	// 获取规则组
	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get rule information"})
		return
	}

	// 检查权限：只有admin可以启用调整规则
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can enable adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can enable adjustment rules"})
		return
	}

	// 设置启用状态
	group.AdjustEnabled = true

	if err := dbs.DB().Save(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enable rule"})
		return
	}

	// 根据规则类型确定文件路径
	var rulePath, alertPath string
	if group.Type == model.RuleTypeAdjustCPU {
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		} else {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		}
	} else if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/bw-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		} else {
			rulePath = fmt.Sprintf("%s/bw-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported rule type"})
		return
	}

	// 告警文件路径
	if group.Owner == "admin" {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
	} else {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
	}

	// 创建符号链接
	ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

	log.Printf("[ADJUST-INFO] Enabling adjustment rule: uuid=%s, type=%s", uuid, group.Type)

	// 使用routes.CreateSymlink而不是os.Symlink以支持远程Prometheus服务器
	if err := routes.CreateSymlink(rulePath, ruleLinkPath); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to create rule symlink: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create rule symlink"})
		return
	}

	if err := routes.CreateSymlink(alertPath, alertLinkPath); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to create alert symlink: %v", err)
		// 如果第二个链接失败，尝试清理第一个
		routes.RemoveFile(ruleLinkPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create alert symlink"})
		return
	}

	// 重新加载Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
	routes.ReloadPrometheus()

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": uuid,
			"enabled":    true,
		},
	})
}

// DisableAdjustRule 禁用资源调整规则
func (a *AdjustAPI) DisableAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}

	// 检查权限：只有admin可以禁用调整规则
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can disable adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can disable adjustment rules"})
		return
	}

	// 更新启用状态
	group.Enabled = false
	group.AdjustEnabled = false

	if err := dbs.DB().Save(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disable rule"})
		return
	}

	// 根据规则类型确定文件路径
	var rulePath, alertPath string
	if group.Type == model.RuleTypeAdjustCPU {
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		} else {
			rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		}
	} else if group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW {
		if group.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/bw-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		} else {
			rulePath = fmt.Sprintf("%s/bw-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported rule type"})
		return
	}

	// 告警文件路径
	if group.Owner == "admin" {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
	} else {
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
	}

	// 删除符号链接
	ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

	log.Printf("[ADJUST-INFO] Disabling adjustment rule: uuid=%s, type=%s", uuid, group.Type)

	// 使用routes.RemoveSymlink而不是os.Remove以支持远程Prometheus服务器
	if err := routes.RemoveFile(ruleLinkPath); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to remove rule symlink: %v", err)
		// 不返回错误，继续尝试删除其他链接
	}

	if err := routes.RemoveFile(alertLinkPath); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to remove alert symlink: %v", err)
		// 不返回错误，因为禁用操作仍然可以继续
	}

	// 重新加载Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
	routes.ReloadPrometheus()

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": uuid,
			"enabled":    false,
		},
	})
}

// ProcessResourceAdjustmentWebhook 处理资源自动调整的webhook
func (a *AdjustAPI) ProcessResourceAdjustmentWebhook(c *gin.Context) {
	// 记录请求来源
	requestIP := c.ClientIP()
	requestMethod := c.Request.Method
	requestURI := c.Request.RequestURI
	requestUA := c.Request.UserAgent()

	log.Printf("[ADJUST-DEBUG] Received resource adjustment webhook request: IP=%s, Method=%s, URI=%s, UserAgent=%s",
		requestIP, requestMethod, requestURI, requestUA)

	// 读取和记录原始请求数据
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to read request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request", "code": "REQUEST_READ_ERROR"})
		return
	}

	// 记录请求信息，限制日志大小
	bodyStr := string(body)
	if len(bodyStr) > 2000 {
		log.Printf("[ADJUST-DEBUG] Original request body (truncated): %s...(truncated, full length: %d)", bodyStr[:2000], len(bodyStr))
	} else {
		log.Printf("[ADJUST-DEBUG] Original request body: %s", bodyStr)
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	// 解析告警数据
	var req routes.AlertWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Failed to parse request body: %v", err)
		log.Printf("[ADJUST-ERROR] Request body content: %s", bodyStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert format", "code": "INVALID_FORMAT"})
		return
	}

	log.Printf("[ADJUST-INFO] Received adjustment request, status: %s, number of alerts: %d", req.Status, len(req.Alerts))

	// 记录处理的告警数量
	successCount := 0
	failedCount := 0

	// 处理每个告警
	for i, alert := range req.Alerts {
		log.Printf("[ADJUST-INFO] Starting to process alert %d", i+1)

		// 创建一个请求追踪ID，用于关联日志
		requestID := fmt.Sprintf("adjust-%s-%d", time.Now().Format("20060102-150405"), i)

		// 处理告警的通用函数，减少代码重复
		result := a.processAlertAdjustment(c.Request.Context(), alert, requestID)
		if result {
			successCount++
		} else {
			failedCount++
		}
	}

	log.Printf("[ADJUST-INFO] Resource adjustment processing completed: total=%d, success=%d, failed=%d",
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

// 处理单个告警的调整逻辑
func (a *AdjustAPI) processAlertAdjustment(ctx context.Context, alert routes.AdjustAlert, requestID string) bool {
	startTime := time.Now()

	// 提取所有标签供后续使用
	domain := alert.Labels["domain"]
	ruleID := alert.Labels["rule_id"]
	actionType := alert.Labels["action_type"]
	ruleGroup := alert.Labels["rule_group"]
	instanceID := alert.Labels["instance_id"] // 提取instance_id

	log.Printf("[ADJUST-%s] Processing alert: domain=%s, ruleID=%s, actionType=%s, instanceID=%s",
		requestID, domain, ruleID, actionType, instanceID)

	// 参数验证
	if domain == "" || ruleID == "" || actionType == "" {
		log.Printf("[ADJUST-%s] Missing required parameters: domain=%s, ruleID=%s, actionType=%s",
			requestID, domain, ruleID, actionType)
		return false
	}

	fmt.Printf("[Processing alert] Domain: %s, Rule: %s, Action: %s\n", domain, ruleID, actionType)

	// 记录详细的标签信息
	log.Printf("[ADJUST-%s] Alert status: %s", requestID, alert.Status)
	log.Printf("[ADJUST-%s] Alert labels:", requestID)
	for key, value := range alert.Labels {
		log.Printf("[ADJUST-%s]   %s = %s", requestID, key, value)
	}
	log.Printf("[ADJUST-%s] Alert annotations:", requestID)
	for key, value := range alert.Annotations {
		log.Printf("[ADJUST-%s]   %s = %s", requestID, key, value)
	}

	// 创建调整记录
	record := &routes.AdjustmentRecord{
		Name:          alert.Labels["alertname"],
		RuleGroupUUID: ruleGroup,
		Summary:       alert.Annotations["summary"],
		Description:   alert.Annotations["description"],
		StartsAt:      alert.StartsAt,
		AdjustType:    actionType,
		TargetDevice:  alert.Labels["target_device"],
	}

	// 记录调整历史
	history := &model.AdjustmentHistory{
		DomainName: domain,
		RuleID:     ruleID,
		GroupUUID:  ruleGroup,
		ActionType: actionType,
		Status:     "processing",
		Details:    fmt.Sprintf("Processing %s (domain: %s)", actionType, domain),
		AdjustTime: time.Now(),
	}

	// 发送邮件通知(如果配置了邮件)
	if email := alert.Labels["email"]; email != "" {
		log.Printf("[ADJUST-%s] Sending email notification to: %s", requestID, email)
		if err := a.operator.SendAdjustEmail(email, record, domain); err != nil {
			log.Printf("[ADJUST-%s] Failed to send email notification: %v", requestID, err)
		}
	}

	// 根据操作类型执行相应的资源调整
	var err error
	fmt.Printf("wngzhe ProcessResourceAdjustmentWebhook - Processing actionType: %s, domain: %s, ruleGroup: %s\n", actionType, domain, ruleGroup)
	switch actionType {
	case "limit_cpu":
		fmt.Printf("wngzhe ProcessResourceAdjustmentWebhook - Executing CPU limit operation for domain: %s\n", domain)
		log.Printf("[ADJUST-%s] Executing CPU limit operation", requestID)
		err = a.operator.AdjustCPUResource(ctx, record, domain, true, instanceID)
	case "restore_cpu":
		fmt.Printf("wngzhe ProcessResourceAdjustmentWebhook - Executing CPU restore operation for domain: %s\n", domain)
		log.Printf("[ADJUST-%s] Executing CPU restore operation", requestID)
		err = a.operator.RestoreCPUResource(ctx, record, domain, instanceID)
	case "limit_in_bw":
		log.Printf("[ADJUST-%s] Executing inbound bandwidth limit operation, device: %s", requestID, record.TargetDevice)
		err = a.operator.AdjustBandwidthResource(ctx, record, domain, record.TargetDevice, true, instanceID)
	case "restore_in_bw":
		log.Printf("[ADJUST-%s] Executing inbound bandwidth restore operation, device: %s", requestID, record.TargetDevice)
		err = a.operator.RestoreBandwidthResource(ctx, record, domain, record.TargetDevice, instanceID)
	case "limit_out_bw":
		log.Printf("[ADJUST-%s] Executing outbound bandwidth limit operation, device: %s", requestID, record.TargetDevice)
		err = a.operator.AdjustBandwidthResource(ctx, record, domain, record.TargetDevice, true, instanceID)
	case "restore_out_bw":
		log.Printf("[ADJUST-%s] Executing outbound bandwidth restore operation, device: %s", requestID, record.TargetDevice)
		err = a.operator.RestoreBandwidthResource(ctx, record, domain, record.TargetDevice, instanceID)
	default:
		log.Printf("[ADJUST-%s] Unknown adjustment type: %s", requestID, actionType)
		history.Status = "failed"
		history.Details = fmt.Sprintf("Unknown adjustment type: %s", actionType)
		if dbErr := a.operator.SaveAdjustmentHistory(ctx, history); dbErr != nil {
			log.Printf("[ADJUST-%s] Failed to save adjustment history: %v", requestID, dbErr)
		}
		return false
	}

	// 更新历史记录状态
	if err != nil {
		fmt.Printf("wngzhe ProcessResourceAdjustmentWebhook - Processing failed for domain %s, actionType %s: %v\n", domain, actionType, err)
		log.Printf("[ADJUST-%s] Processing failed: %v", requestID, err)
		history.Status = "failed"
		history.Details = fmt.Sprintf("Processing %s failed: %v", actionType, err)
	} else {
		fmt.Printf("wngzhe ProcessResourceAdjustmentWebhook - Processing successful for domain %s, actionType %s\n", domain, actionType)
		log.Printf("[ADJUST-%s] Processing successful", requestID)
		history.Status = "completed"
		history.Details = fmt.Sprintf("Successfully processed %s (domain: %s)", actionType, domain)
	}

	// 保存调整历史
	if dbErr := a.operator.SaveAdjustmentHistory(ctx, history); dbErr != nil {
		log.Printf("[ADJUST-%s] Failed to save adjustment history: %v", requestID, dbErr)
	}

	elapsed := time.Since(startTime)
	fmt.Printf("wngzhe ProcessResourceAdjustmentWebhook - Processing completed for domain %s, elapsed time: %v, success: %v\n", domain, elapsed, err == nil)
	log.Printf("[ADJUST-%s] Processing completed, time taken: %v", requestID, elapsed)
	fmt.Printf("[Processing completed] Domain: %s, Result: %v, Time taken: %v\n", domain, err == nil, elapsed)

	return err == nil
}

// CreateBWAdjustRule creates bandwidth adjustment rule
func (a *AdjustAPI) CreateBWAdjustRule(c *gin.Context) {
	var req struct {
		Name          string `json:"name" binding:"required"`
		Owner         string `json:"owner" binding:"required"`
		Email         string `json:"email"`
		AdjustEnabled bool   `json:"adjust_enabled"`
		Rules         []struct {
			Name             string `json:"name"`
			InEnabled        bool   `json:"in_enabled"`
			InHighThreshold  int64  `json:"in_high_threshold"`
			InLowThreshold   int64  `json:"in_low_threshold"`
			OutEnabled       bool   `json:"out_enabled"`
			OutHighThreshold int64  `json:"out_high_threshold"`
			OutLowThreshold  int64  `json:"out_low_threshold"`
			SmoothWindow     int    `json:"smooth_window"`
			TriggerDuration  int    `json:"trigger_duration"`
			RestoreDuration  int    `json:"restore_duration"`
			LimitValue       int    `json:"limit_value"`
			TargetDevice     string `json:"target_device"`
		} `json:"rules"`
		LinkedVMs []string `json:"linkedvms"`
	}

	log.Printf("[ADJUST-INFO] Creating bandwidth adjustment rule, request IP: %s", c.ClientIP())

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

	// Create a variable to store the group for use outside the loop
	var group *model.AdjustRuleGroup

	// Process each rule (currently only one)
	for _, rule := range req.Rules {
		// Validate rule
		if !rule.InEnabled && !rule.OutEnabled {
			log.Printf("[ADJUST-ERROR] Both inbound and outbound bandwidth adjustment are disabled")
			c.JSON(http.StatusBadRequest, gin.H{"error": "At least one of inbound or outbound bandwidth adjustment must be enabled"})
			return
		}

		// Create rule group
		ruleType := model.RuleTypeAdjustInBW
		if rule.OutEnabled && !rule.InEnabled {
			ruleType = model.RuleTypeAdjustOutBW
		} else if rule.OutEnabled && rule.InEnabled {
			ruleType = model.RuleTypeAdjustInBW // If both are enabled, prioritize inbound type
			log.Printf("[ADJUST-INFO] Both inbound and outbound bandwidth adjustment are enabled, but currently only inbound is supported. Outbound configuration will be ignored.")
		}

		group = &model.AdjustRuleGroup{
			Name:          req.Name,
			Type:          ruleType,
			Owner:         req.Owner,
			Enabled:       true,
			Email:         req.Email,
			AdjustEnabled: req.AdjustEnabled,
		}

		log.Printf("[ADJUST-INFO] Creating bandwidth adjustment rule group: name=%s, type=%s", req.Name, ruleType)
		if err := a.operator.CreateAdjustRuleGroup(c.Request.Context(), group); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to create rule group: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule group failed: " + err.Error()})
			return
		}

		// Create rule details
		detail := &model.BWAdjustRuleDetail{
			GroupUUID:        group.UUID,
			Name:             rule.Name,
			InHighThreshold:  rule.InHighThreshold,
			InLowThreshold:   rule.InLowThreshold,
			OutHighThreshold: rule.OutHighThreshold,
			OutLowThreshold:  rule.OutLowThreshold,
			SmoothWindow:     rule.SmoothWindow,
			TriggerDuration:  rule.TriggerDuration,
			RestoreDuration:  rule.RestoreDuration,
			LimitValue:       rule.LimitValue,
		}

		// Validate required parameters
		if rule.InEnabled {
			if detail.InHighThreshold == 0 {
				log.Printf("[ADJUST-ERROR] Inbound high threshold cannot be zero")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Inbound high threshold cannot be zero"})
				return
			}
			if detail.InLowThreshold == 0 {
				log.Printf("[ADJUST-ERROR] Inbound low threshold cannot be zero")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Inbound low threshold cannot be zero"})
				return
			}
		}

		if rule.OutEnabled {
			if detail.OutHighThreshold == 0 {
				log.Printf("[ADJUST-ERROR] Outbound high threshold cannot be zero")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Outbound high threshold cannot be zero"})
				return
			}
			if detail.OutLowThreshold == 0 {
				log.Printf("[ADJUST-ERROR] Outbound low threshold cannot be zero")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Outbound low threshold cannot be zero"})
				return
			}
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
		if detail.LimitValue == 0 {
			log.Printf("[ADJUST-ERROR] Bandwidth limit value cannot be zero")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Bandwidth limit value cannot be zero"})
			return
		}

		log.Printf("[ADJUST-INFO] Creating bandwidth adjustment rule detail: name=%s, in_enabled=%v, out_enabled=%v",
			rule.Name, rule.InEnabled, rule.OutEnabled)
		if err := a.operator.CreateBWAdjustRuleDetail(c.Request.Context(), detail); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to create rule detail: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}

		// Generate rule files
		ruleData := map[string]interface{}{
			"rule_group":          strings.ReplaceAll(group.UUID, "-", "_"),
			"rule_group_original": group.UUID,
			"in_enabled":          rule.InEnabled,
			"in_high_threshold":   detail.InHighThreshold,
			"in_low_threshold":    detail.InLowThreshold,
			"out_enabled":         rule.OutEnabled,
			"out_high_threshold":  detail.OutHighThreshold,
			"out_low_threshold":   detail.OutLowThreshold,
			"smooth_window":       detail.SmoothWindow,
			"trigger_duration":    detail.TriggerDuration,
			"restore_duration":    detail.RestoreDuration,
			"owner":               req.Owner,
			"email":               req.Email,
			"adjust_enabled":      req.AdjustEnabled,
			"target_device":       rule.TargetDevice,
		}

		// Generate record rules
		var templateName string
		if ruleType == model.RuleTypeAdjustOutBW {
			templateName = OutBWAdjustRuleTemplate
		} else {
			templateName = InBWAdjustRuleTemplate
		}

		log.Printf("[ADJUST-INFO] Generating bandwidth adjustment rule file")
		if err := routes.ProcessTemplate(templateName, fmt.Sprintf("bw-adjust-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to render BW adjust rule: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render BW adjust rule"})
			return
		}

		// Generate alert rules
		log.Printf("[ADJUST-INFO] Generating resource adjustment alerts file")
		if err := routes.ProcessTemplate(ResourceAdjustAlertsTemplate, fmt.Sprintf("resource-adjust-alerts-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to render resource adjustment alerts: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render resource adjustment alerts"})
			return
		}

		// Create symlinks for the generated files
		var rulePath, alertPath string
		if req.Owner == "admin" {
			rulePath = fmt.Sprintf("%s/bw-adjust-%s-%s.yml", routes.RulesGeneral, req.Owner, group.UUID)
			alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, req.Owner, group.UUID)
		} else {
			rulePath = fmt.Sprintf("%s/bw-adjust-%s-%s.yml", routes.RulesSpecial, req.Owner, group.UUID)
			alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, req.Owner, group.UUID)
		}

		// Create symlinks
		ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))
		alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

		fmt.Printf("wngzhe CreateBWAdjustRule - rulePath: %s, ruleLinkPath: %s", rulePath, ruleLinkPath)
		fmt.Printf("wngzhe CreateBWAdjustRule - alertPath: %s, alertLinkPath: %s", alertPath, alertLinkPath)
		fmt.Printf("wngzhe CreateBWAdjustRule - isRemotePrometheus: %t", routes.IsRemotePrometheus())

		log.Printf("[ADJUST-INFO] Creating symlinks for bandwidth adjustment rule")
		if err := routes.CreateSymlink(rulePath, ruleLinkPath); err != nil {
			fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create rule symlink: %v", err)
			log.Printf("[ADJUST-ERROR] Failed to create rule symlink: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create rule symlink"})
			return
		}

		if err := routes.CreateSymlink(alertPath, alertLinkPath); err != nil {
			fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create alert symlink: %v", err)
			log.Printf("[ADJUST-ERROR] Failed to create alert symlink: %v", err)
			// 如果第二个链接失败，尝试清理第一个
			routes.RemoveFile(ruleLinkPath)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create alert symlink"})
			return
		}
	}

	// Link VMs
	if len(req.LinkedVMs) > 0 {
		log.Printf("[ADJUST-INFO] Linking virtual machines: count=%d", len(req.LinkedVMs))
		// Use existing link function
		alarmOperator := &routes.AlarmOperator{}
		_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")

		// Update matched_vms.json
		alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
		_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add", "bw")
	}

	// Reload Prometheus
	log.Printf("[ADJUST-INFO] Reloading Prometheus configuration")
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

// GetBWAdjustRules 获取带宽调整规则
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

	// 检查权限：只有admin可以查看调整规则
	// 这里可以从请求中获取用户信息，暂时使用admin检查
	// TODO: 从认证信息中获取当前用户
	currentUser := "admin" // 临时设置，实际应该从认证信息获取
	if currentUser != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can view adjustment rules, user: %s", currentUser)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can view adjustment rules"})
		return
	}

	log.Printf("[ADJUST-INFO] Getting bandwidth adjustment rules: page=%d, pageSize=%d, uuid=%s", page, pageSize, groupUUID)

	queryParams := routes.ListAdjustRuleGroupsParams{
		RuleType: model.RuleTypeAdjustInBW,
		Page:     page,
		PageSize: pageSize,
	}

	if groupUUID != "" {
		queryParams.GroupUUID = groupUUID
		queryParams.PageSize = 1
	}

	groups, total, err := a.operator.ListAdjustRuleGroups(c.Request.Context(), queryParams)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to query bandwidth adjustment rule groups: %v", err)
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

		// 构建规则数据
		rules := make([]gin.H, 0, len(details))
		for _, detail := range details {
			rules = append(rules, gin.H{
				"name":               detail.Name,
				"in_high_threshold":  detail.InHighThreshold,
				"in_low_threshold":   detail.InLowThreshold,
				"out_high_threshold": detail.OutHighThreshold,
				"out_low_threshold":  detail.OutLowThreshold,
				"smooth_window":      detail.SmoothWindow,
				"trigger_duration":   detail.TriggerDuration,
				"restore_duration":   detail.RestoreDuration,
			})
		}

		// 获取最近的调整历史
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
			"id":             group.ID,
			"group_uuid":     group.UUID,
			"name":           group.Name,
			"owner":          group.Owner,
			"enabled":        group.Enabled,
			"email":          group.Email,
			"adjust_enabled": group.AdjustEnabled,
			"create_time":    group.CreatedAt.Format(time.RFC3339),
			"rules":          rules,
			"linked_vms":     linkedVMs,
			"history":        historyData,
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

// DeleteBWAdjustRule 删除带宽调整规则
func (a *AdjustAPI) DeleteBWAdjustRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required", "code": "MISSING_UUID"})
		return
	}

	log.Printf("[ADJUST-INFO] Deleting bandwidth adjustment rule: uuid=%s", uuid)

	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[ADJUST-ERROR] Bandwidth adjustment rule not found: %s", uuid)
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found", "code": "NOT_FOUND"})
		return
	} else if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to retrieve rule information: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}

	if group.Type != model.RuleTypeAdjustInBW && group.Type != model.RuleTypeAdjustOutBW {
		log.Printf("[ADJUST-ERROR] Invalid rule type: %s", group.Type)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type", "code": "INVALID_RULE_TYPE"})
		return
	}

	// 检查权限：只有admin可以删除调整规则
	if group.Owner != "admin" {
		log.Printf("[ADJUST-ERROR] Permission denied: only admin can delete adjustment rules, owner: %s", group.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied: only admin can delete adjustment rules"})
		return
	}

	// 删除链接的VM
	alarmOperator := &routes.AlarmOperator{}
	_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), uuid, "", "")

	// 更新matched_vms.json
	alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, uuid, "remove", "bw")

	// 确定文件路径
	var inRulePath, outRulePath, alertPath string
	if group.Owner == "admin" {
		inRulePath = fmt.Sprintf("%s/in-bw-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		outRulePath = fmt.Sprintf("%s/out-bw-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
	} else {
		inRulePath = fmt.Sprintf("%s/in-bw-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		outRulePath = fmt.Sprintf("%s/out-bw-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
	}

	// 确定symlink路径
	inRuleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(inRulePath))
	outRuleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(outRulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))

	// 删除symlink和规则文件
	deletedFiles := []string{}
	// 删除入站规则文件
	if err := routes.RemoveFile(inRuleLinkPath); err == nil {
		deletedFiles = append(deletedFiles, inRuleLinkPath)
		log.Printf("[ADJUST-INFO] Removed symlink: %s", inRuleLinkPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove symlink %s: %v", inRuleLinkPath, err)
	}
	if err := routes.RemoveFile(inRulePath); err == nil {
		deletedFiles = append(deletedFiles, inRulePath)
		log.Printf("[ADJUST-INFO] Removed file: %s", inRulePath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove file %s: %v", inRulePath, err)
	}

	// 删除出站规则文件
	if err := routes.RemoveFile(outRuleLinkPath); err == nil {
		deletedFiles = append(deletedFiles, outRuleLinkPath)
		log.Printf("[ADJUST-INFO] Removed symlink: %s", outRuleLinkPath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove symlink %s: %v", outRuleLinkPath, err)
	}
	if err := routes.RemoveFile(outRulePath); err == nil {
		deletedFiles = append(deletedFiles, outRulePath)
		log.Printf("[ADJUST-INFO] Removed file: %s", outRulePath)
	} else {
		log.Printf("[ADJUST-ERROR] Failed to remove file %s: %v", outRulePath, err)
	}

	// 删除告警规则文件
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

	// 删除数据库记录
	err = a.operator.DeleteAdjustRuleGroupWithDependencies(c.Request.Context(), uuid)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to delete rule group and its dependencies: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete rule: " + err.Error()})
		return
	}

	// 重新加载Prometheus
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("[ADJUST-WARN] Failed to reload Prometheus: %v", err)
	}

	log.Printf("[ADJUST-INFO] Bandwidth adjustment rule deleted successfully: uuid=%s", uuid)
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_files": deletedFiles,
			"group_uuid":    uuid,
		},
	})
}
