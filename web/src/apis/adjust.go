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
	"web/src/common"
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
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, uuid, "remove", "adjust-cpu")

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

	// 清理计算节点上的调整状态指标
	log.Printf("[ADJUST-INFO] Cleaning up CPU adjustment metrics for rule: %s", uuid)
	if err := a.cleanupRuleMetricsOnNodes(c.Request.Context(), uuid, "cpu"); err != nil {
		log.Printf("[ADJUST-WARNING] Failed to cleanup rule metrics: %v", err)
		// 不影响规则删除的成功状态，只记录警告
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
		} `json:"rules"`
		LinkedVMs []struct {
			VMUUID       string `json:"vm_uuid"`
			TargetDevice string `json:"target_device"`
		} `json:"linkedvms"`
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
		// When both in and out are enabled, we'll create a combined rule group
		ruleType := model.RuleTypeAdjustInBW
		if rule.OutEnabled && !rule.InEnabled {
			ruleType = model.RuleTypeAdjustOutBW
		} else if rule.OutEnabled && rule.InEnabled {
			ruleType = model.RuleTypeAdjustInBW // Use inbound as primary type for combined rules
			log.Printf("[ADJUST-INFO] Both inbound and outbound bandwidth adjustment are enabled, will generate both rule files.")
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
		}

		// Generate record rules
		log.Printf("[ADJUST-INFO] Generating bandwidth adjustment rule files")

		// Generate inbound rules if enabled
		if rule.InEnabled {
			log.Printf("[ADJUST-INFO] Generating inbound bandwidth adjustment rule file")
			if err := routes.ProcessTemplate(InBWAdjustRuleTemplate, fmt.Sprintf("bw-in-adjust-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
				log.Printf("[ADJUST-ERROR] Failed to render inbound BW adjust rule: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render inbound BW adjust rule"})
				return
			}
		}

		// Generate outbound rules if enabled
		if rule.OutEnabled {
			log.Printf("[ADJUST-INFO] Generating outbound bandwidth adjustment rule file")
			if err := routes.ProcessTemplate(OutBWAdjustRuleTemplate, fmt.Sprintf("bw-out-adjust-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
				log.Printf("[ADJUST-ERROR] Failed to render outbound BW adjust rule: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render outbound BW adjust rule"})
				return
			}
		}

		// Generate alert rules
		log.Printf("[ADJUST-INFO] Generating resource adjustment alerts file")
		if err := routes.ProcessTemplate(ResourceAdjustAlertsTemplate, fmt.Sprintf("resource-adjust-alerts-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
			log.Printf("[ADJUST-ERROR] Failed to render resource adjustment alerts: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render resource adjustment alerts"})
			return
		}

		// Create symlinks for the generated files
		var alertPath string
		if req.Owner == "admin" {
			alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, req.Owner, group.UUID)
		} else {
			alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesSpecial, req.Owner, group.UUID)
		}

		// Create symlinks for rule files
		log.Printf("[ADJUST-INFO] Creating symlinks for bandwidth adjustment rules")

		// Create symlink for inbound rules if enabled
		if rule.InEnabled {
			var inRulePath string
			if req.Owner == "admin" {
				inRulePath = fmt.Sprintf("%s/bw-in-adjust-%s-%s.yml", routes.RulesGeneral, req.Owner, group.UUID)
			} else {
				inRulePath = fmt.Sprintf("%s/bw-in-adjust-%s-%s.yml", routes.RulesSpecial, req.Owner, group.UUID)
			}
			inRuleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(inRulePath))

			fmt.Printf("wngzhe CreateBWAdjustRule - inRulePath: %s, inRuleLinkPath: %s", inRulePath, inRuleLinkPath)
			if err := routes.CreateSymlink(inRulePath, inRuleLinkPath); err != nil {
				fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create inbound rule symlink: %v", err)
				log.Printf("[ADJUST-ERROR] Failed to create inbound rule symlink: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create inbound rule symlink"})
				return
			}
		}

		// Create symlink for outbound rules if enabled
		if rule.OutEnabled {
			var outRulePath string
			if req.Owner == "admin" {
				outRulePath = fmt.Sprintf("%s/bw-out-adjust-%s-%s.yml", routes.RulesGeneral, req.Owner, group.UUID)
			} else {
				outRulePath = fmt.Sprintf("%s/bw-out-adjust-%s-%s.yml", routes.RulesSpecial, req.Owner, group.UUID)
			}
			outRuleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(outRulePath))

			fmt.Printf("wngzhe CreateBWAdjustRule - outRulePath: %s, outRuleLinkPath: %s", outRulePath, outRuleLinkPath)
			if err := routes.CreateSymlink(outRulePath, outRuleLinkPath); err != nil {
				fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create outbound rule symlink: %v", err)
				log.Printf("[ADJUST-ERROR] Failed to create outbound rule symlink: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create outbound rule symlink"})
				return
			}
		}

		// Create symlink for alert rules
		alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))
		fmt.Printf("wngzhe CreateBWAdjustRule - alertPath: %s, alertLinkPath: %s", alertPath, alertLinkPath)
		fmt.Printf("wngzhe CreateBWAdjustRule - isRemotePrometheus: %t", routes.IsRemotePrometheus())

		if err := routes.CreateSymlink(alertPath, alertLinkPath); err != nil {
			fmt.Printf("wngzhe CreateBWAdjustRule - Failed to create alert symlink: %v", err)
			log.Printf("[ADJUST-ERROR] Failed to create alert symlink: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create alert symlink"})
			return
		}
	}

	// Link VMs
	if len(req.LinkedVMs) > 0 {
		log.Printf("[ADJUST-INFO] Linking virtual machines: count=%d", len(req.LinkedVMs))
		// Use existing link function
		alarmOperator := &routes.AlarmOperator{}

		// First get all existing linked VMs for this group, then delete them one by one
		existingLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err != nil {
			log.Printf("[ADJUST-WARN] Failed to get existing VM links for group %s: %v", group.UUID, err)
		} else {
			for _, link := range existingLinks {
				_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, link.VMUUID, link.Interface)
			}
		}

		// Create new VM links with correct target device mapping
		for _, vm := range req.LinkedVMs {
			// Create the link with target device as interface
			_ = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, []string{vm.VMUUID}, vm.TargetDevice)
		}

		// Update matched_vms.json with target device for each VM
		alarmAPI := &AlarmAPI{operator: &routes.AlarmOperator{}}
		for _, vm := range req.LinkedVMs {
			_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{vm.VMUUID}, group.UUID, "add", "adjust-bw", vm.TargetDevice)
		}
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

	// 需要分别查询入站和出站带宽规则
	var groups []model.AdjustRuleGroup
	var total int64
	var err error

	if groupUUID != "" {
		// 如果指定了groupUUID，直接获取该规则组
		group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), groupUUID)
		if err == nil && (group.Type == model.RuleTypeAdjustInBW || group.Type == model.RuleTypeAdjustOutBW) {
			groups = []model.AdjustRuleGroup{*group}
			total = 1
		}
	} else {
		// 分别查询入站和出站带宽规则
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
			// 合并结果
			groups = append(groups, inBWGroups...)
			groups = append(groups, outBWGroups...)
			total = inBWTotal + outBWTotal
		}
	}

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
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, uuid, "remove", "adjust-bw")

	// 确定文件路径
	var inRulePath, outRulePath, alertPath string
	if group.Owner == "admin" {
		inRulePath = fmt.Sprintf("%s/bw-in-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		outRulePath = fmt.Sprintf("%s/bw-out-adjust-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, uuid)
	} else {
		inRulePath = fmt.Sprintf("%s/bw-in-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
		outRulePath = fmt.Sprintf("%s/bw-out-adjust-%s-%s.yml", routes.RulesSpecial, group.Owner, uuid)
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

	// 清理计算节点上的调整状态指标
	log.Printf("[ADJUST-INFO] Cleaning up bandwidth adjustment metrics for rule: %s", uuid)
	if err := a.cleanupRuleMetricsOnNodes(c.Request.Context(), uuid, "bandwidth"); err != nil {
		log.Printf("[ADJUST-WARNING] Failed to cleanup rule metrics: %v", err)
		// 不影响规则删除的成功状态，只记录警告
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

// LinkAdjustRuleRequest 链接调整规则请求
type LinkAdjustRuleRequest struct {
	GroupUUID string `json:"group_uuid" binding:"required"`
	LinkedVMs []struct {
		VMUUID       string `json:"vm_uuid" binding:"required"`
		TargetDevice string `json:"target_device,omitempty"`
	} `json:"linked_vms" binding:"required"`
}

// UnlinkAdjustRuleRequest 取消链接调整规则请求
type UnlinkAdjustRuleRequest struct {
	GroupUUID    string   `json:"group_uuid" binding:"required"`
	VMUUIDs      []string `json:"vm_uuids,omitempty"`
	TargetDevice string   `json:"target_device,omitempty"`
}

// LinkedVMInfo 链接的VM信息
type LinkedVMInfo struct {
	VMUUID       string `json:"vm_uuid"`
	Domain       string `json:"domain"`
	TargetDevice string `json:"target_device"`
	RuleID       string `json:"rule_id"`
}

// LinkAdjustRule 将VM链接到调整规则组
func (a *AdjustAPI) LinkAdjustRule(c *gin.Context) {
	var req struct {
		GroupUUID string `json:"group_uuid" binding:"required"`
		VMUUID    string `json:"vm_uuid" binding:"required"`
		Interface string `json:"interface"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Invalid request parameters: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters: " + err.Error()})
		return
	}

	log.Printf("[ADJUST-INFO] Linking VM to adjustment rule: group_uuid=%s, vm_uuid=%s, interface=%s",
		req.GroupUUID, req.VMUUID, req.Interface)

	// 检查规则组是否存在
	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get adjustment rule group: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "adjustment rule group not found"})
		return
	}

	// 检查VM是否已经链接到该规则组
	alarmOperator := &routes.AlarmOperator{}
	existingLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), req.GroupUUID)
	if err == nil {
		for _, link := range existingLinks {
			if link.VMUUID == req.VMUUID {
				log.Printf("[ADJUST-WARN] VM already linked to rule group: vm_uuid=%s, group_uuid=%s", req.VMUUID, req.GroupUUID)
				c.JSON(http.StatusConflict, gin.H{"error": "VM already linked to this rule group"})
				return
			}
		}
	}

	// 创建链接
	err = alarmOperator.BatchLinkVMs(c.Request.Context(), req.GroupUUID, []string{req.VMUUID}, req.Interface)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to create VM link: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create VM link: " + err.Error()})
		return
	}

	log.Printf("[ADJUST-INFO] Successfully linked VM to adjustment rule: group_uuid=%s, vm_uuid=%s", req.GroupUUID, req.VMUUID)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "VM successfully linked to adjustment rule",
		"data": gin.H{
			"group_uuid": req.GroupUUID,
			"vm_uuid":    req.VMUUID,
			"interface":  req.Interface,
			"rule_type":  group.Type,
		},
	})
}

// UnlinkAdjustRule 将VM从调整规则中取消链接
func (a *AdjustAPI) UnlinkAdjustRule(c *gin.Context) {
	var req struct {
		GroupUUID string `json:"group_uuid" binding:"required"`
		VMUUID    string `json:"vm_uuid" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] Invalid request parameters: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters: " + err.Error()})
		return
	}

	log.Printf("[ADJUST-INFO] Unlinking VM from adjustment rule: group_uuid=%s, vm_uuid=%s", req.GroupUUID, req.VMUUID)

	// 检查规则组是否存在
	_, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get adjustment rule group: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "adjustment rule group not found"})
		return
	}

	// 检查VM是否链接到该规则组
	alarmOperator := &routes.AlarmOperator{}
	existingLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), req.GroupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get linked VMs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get linked VMs: " + err.Error()})
		return
	}

	var linkExists bool
	for _, link := range existingLinks {
		if link.VMUUID == req.VMUUID {
			linkExists = true
			break
		}
	}

	if !linkExists {
		log.Printf("[ADJUST-WARN] VM not linked to rule group: vm_uuid=%s, group_uuid=%s", req.VMUUID, req.GroupUUID)
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not linked to this rule group"})
		return
	}

	// 删除链接
	_, err = alarmOperator.DeleteVMLink(c.Request.Context(), req.GroupUUID, req.VMUUID, "")
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to delete VM link: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete VM link: " + err.Error()})
		return
	}

	log.Printf("[ADJUST-INFO] Successfully unlinked VM from adjustment rule: group_uuid=%s, vm_uuid=%s", req.GroupUUID, req.VMUUID)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "VM successfully unlinked from adjustment rule",
		"data": gin.H{
			"group_uuid": req.GroupUUID,
			"vm_uuid":    req.VMUUID,
		},
	})
}

// GetLinkAdjustRule 获取调整规则的VM链接信息
func (a *AdjustAPI) GetLinkAdjustRule(c *gin.Context) {
	groupUUID := c.Query("group_uuid")
	if groupUUID == "" {
		log.Printf("[ADJUST-ERROR] Missing group_uuid parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_uuid parameter is required"})
		return
	}

	log.Printf("[ADJUST-INFO] Getting adjustment rule links: group_uuid=%s", groupUUID)

	// 检查规则组是否存在
	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get adjustment rule group: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "adjustment rule group not found"})
		return
	}

	// 获取链接的VM列表
	alarmOperator := &routes.AlarmOperator{}
	vmLinks, err := alarmOperator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("[ADJUST-ERROR] Failed to get linked VMs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get linked VMs: " + err.Error()})
		return
	}

	// 构建返回数据
	linkedVMs := make([]gin.H, 0, len(vmLinks))
	for _, link := range vmLinks {
		// 获取domain信息
		domain, err := routes.GetDomainByInstanceUUID(c.Request.Context(), link.VMUUID)
		if err != nil {
			log.Printf("[ADJUST-WARN] Failed to get domain for VM %s: %v", link.VMUUID, err)
			domain = "unknown"
		}

		// 生成rule_id
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

// cleanupRuleMetricsOnNodes 清理计算节点上的规则相关指标
func (a *AdjustAPI) cleanupRuleMetricsOnNodes(ctx context.Context, ruleGroupUUID, ruleType string) error {
	log.Printf("[ADJUST-INFO] Starting cleanup of %s metrics for rule group: %s", ruleType, ruleGroupUUID)

	// 获取与该规则组关联的VM列表
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

	// 获取这些VM所在的计算节点
	hyperNodes := make(map[int32]bool)
	instanceAdmin := &routes.InstanceAdmin{}
	for _, link := range vmLinks {
		// 获取VM实例信息以确定其所在的计算节点
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

	// 在每个计算节点上执行清理操作
	var cleanupErrors []string
	for hyperID := range hyperNodes {
		log.Printf("[ADJUST-INFO] Cleaning up %s metrics on compute node %d for rule %s", ruleType, hyperID, ruleGroupUUID)

		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/cleanup_rule_metrics.sh --rule-id '%s' --type '%s'",
			ruleGroupUUID, ruleType)

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
