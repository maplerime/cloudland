package apis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"web/src/model"
	"web/src/routes"
	"io"
	"bytes"
)

// AdjustAPI 资源自动调整API
type AdjustAPI struct {
	operator *routes.AdjustOperator
}

// 全局变量
var adjustAPI = &AdjustAPI{
	operator: routes.NewAdjustOperator(),
}

// CreateCPUAdjustRule 创建CPU调整规则
func (a *AdjustAPI) CreateCPUAdjustRule(c *gin.Context) {
	var req struct {
		Name            string   `json:"name" binding:"required"`
		Owner           string   `json:"owner" binding:"required"`
		Email           string   `json:"email"`
		AdjustEnabled   bool     `json:"adjust_enabled"`
		HighThreshold   float64  `json:"high_threshold"`
		LowThreshold    float64  `json:"low_threshold"`
		SmoothWindow    int      `json:"smooth_window"`
		TriggerDuration int      `json:"trigger_duration"`
		RestoreDuration int      `json:"restore_duration"`
		LinkedVMs       []string `json:"linkedvms"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// 创建规则组
	group := &model.AdjustRuleGroup{
		Name:         req.Name,
		Type:         model.RuleTypeAdjustCPU,
		Owner:        req.Owner,
		Enabled:      true,
		Email:        req.Email,
		AdjustEnabled: req.AdjustEnabled,
	}
	
	if err := a.operator.CreateAdjustRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule group failed: " + err.Error()})
		return
	}
	
	// 创建规则详情
	detail := &model.CPUAdjustRuleDetail{
		GroupUUID:       group.UUID,
		Name:            req.Name,
		HighThreshold:   req.HighThreshold,
		LowThreshold:    req.LowThreshold,
		SmoothWindow:    req.SmoothWindow,
		TriggerDuration: req.TriggerDuration,
		RestoreDuration: req.RestoreDuration,
	}
	
	// 应用默认值
	if detail.HighThreshold == 0 {
		detail.HighThreshold = 80
	}
	if detail.LowThreshold == 0 {
		detail.LowThreshold = 40
	}
	if detail.SmoothWindow == 0 {
		detail.SmoothWindow = 5
	}
	if detail.TriggerDuration == 0 {
		detail.TriggerDuration = 30
	}
	if detail.RestoreDuration == 0 {
		detail.RestoreDuration = 300
	}
	
	if err := a.operator.CreateCPUAdjustRuleDetail(c.Request.Context(), detail); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
		return
	}
	
	// 链接VM
	if len(req.LinkedVMs) > 0 {
		// 使用现有的链接函数
		alarmOperator := routes.NewAlarmOperator()
		_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")
		
		// 更新matched_vms.json
		alarmAPI := &AlarmAPI{operator: routes.NewAlarmOperator()}
		_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add")
	}
	
	// 生成规则文件
	ruleData := map[string]interface{}{
		"rule_group":       group.UUID,
		"owner":            req.Owner,
		"email":            req.Email,
		"adjust_enabled":   req.AdjustEnabled,
		"high_threshold":   detail.HighThreshold,
		"low_threshold":    detail.LowThreshold,
		"smooth_window":    detail.SmoothWindow,
		"trigger_duration": detail.TriggerDuration,
		"restore_duration": detail.RestoreDuration,
	}
	
	// 生成记录规则
	if err := routes.ProcessTemplate(CPUAdjustRuleTemplate, fmt.Sprintf("cpu-adjust-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
		log.Printf("Failed to render CPU adjust rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render CPU adjust rule"})
		return
	}
	
	// 生成告警规则
	if err := routes.ProcessTemplate(ResourceAdjustAlertsTemplate, fmt.Sprintf("resource-adjust-alerts-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
		log.Printf("Failed to render resource adjustment alerts: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render resource adjustment alerts"})
		return
	}
	
	// 重新加载Prometheus
	routes.ReloadPrometheus()
	
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": group.UUID,
			"enabled":    true,
			"linkedvms": req.LinkedVMs,
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
		alarmOperator := routes.NewAlarmOperator()
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
	
	// 删除链接的VM
	alarmOperator := routes.NewAlarmOperator()
	_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), uuid, "", "")
	
	// 更新matched_vms.json
	alarmAPI := &AlarmAPI{operator: routes.NewAlarmOperator()}
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, uuid, "remove")
	
	// 确定文件路径
	var rulePath, alertPath string
	if group.Owner == "admin" {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
	} else {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
	}
	
	// 确定symlink路径
	ruleLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(alertPath))
	
	// 删除symlink和规则文件
	deletedFiles := []string{}
	if err := os.Remove(ruleLinkPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, ruleLinkPath)
	}
	if err := os.Remove(alertLinkPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, alertLinkPath)
	}
	if err := os.Remove(rulePath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, rulePath)
	}
	if err := os.Remove(alertPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, alertPath)
	}
	
	// 删除数据库记录
	err = a.operator.DeleteAdjustRuleGroupWithDependencies(c.Request.Context(), uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete rule: " + err.Error()})
		return
	}
	
	// 重新加载Prometheus
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Warning: Failed to reload Prometheus: %v", err)
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
	
	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}
	
	// 更新启用状态
	group.Enabled = true
	group.AdjustEnabled = true
	
	if err := routes.DB().Save(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enable rule"})
		return
	}
	
	// 重新创建符号链接
	var rulePath, alertPath string
	if group.Owner == "admin" {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
	} else {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
	}
	
	// 创建符号链接
	ruleLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(alertPath))
	
	_ = os.Symlink(rulePath, ruleLinkPath)
	_ = os.Symlink(alertPath, alertLinkPath)
	
	// 重新加载Prometheus
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
	
	// 更新启用状态
	group.Enabled = false
	group.AdjustEnabled = false
	
	if err := routes.DB().Save(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disable rule"})
		return
	}
	
	// 删除符号链接
	var rulePath, alertPath string
	if group.Owner == "admin" {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
	} else {
		rulePath = fmt.Sprintf("%s/cpu-adjust-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
	}
	
	// 删除符号链接
	ruleLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(rulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(alertPath))
	
	_ = os.Remove(ruleLinkPath)
	_ = os.Remove(alertLinkPath)
	
	// 重新加载Prometheus
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
	
	log.Printf("[ADJUST-DEBUG] 收到资源调整Webhook请求: IP=%s, Method=%s, URI=%s, UserAgent=%s", 
		requestIP, requestMethod, requestURI, requestUA)
	
	// 读取和记录原始请求数据
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("[ADJUST-ERROR] 读取请求体失败: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败", "code": "REQUEST_READ_ERROR"})
		return
	}
	
	// 记录请求信息，限制日志大小
	bodyStr := string(body)
	if len(bodyStr) > 2000 {
		log.Printf("[ADJUST-DEBUG] 原始请求体(截取): %s...(已截断，完整长度: %d)", bodyStr[:2000], len(bodyStr))
	} else {
		log.Printf("[ADJUST-DEBUG] 原始请求体: %s", bodyStr)
	}
	
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	
	// 解析告警数据
	var req routes.AlertWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] 解析请求体失败: %v", err)
		log.Printf("[ADJUST-ERROR] 请求体内容: %s", bodyStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的告警格式", "code": "INVALID_FORMAT"})
		return
	}
	
	log.Printf("[ADJUST-INFO] 收到调整请求, 状态: %s, 告警数量: %d", req.Status, len(req.Alerts))
	
	// 记录处理的告警数量
	successCount := 0
	failedCount := 0
	
	// 处理每个告警
	for i, alert := range req.Alerts {
		log.Printf("[ADJUST-INFO] 开始处理第%d个告警", i+1)
		
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
	
	log.Printf("[ADJUST-INFO] 资源调整处理完成: 总数=%d, 成功=%d, 失败=%d", 
		len(req.Alerts), successCount, failedCount)
	
	c.JSON(http.StatusOK, gin.H{
		"status":         "success",
		"total_alerts":   len(req.Alerts),
		"success_count":  successCount,
		"failed_count":   failedCount,
		"message":        "资源调整处理完成",
		"processed_at":   time.Now().Format(time.RFC3339),
	})
}

// 从数据库获取冷却时间配置
func (a *AdjustAPI) getAdjustmentCooldownConfig(ctx context.Context, actionType string, ruleGroupUUID string) int {
	log.Printf("[ADJUST-DEBUG] 获取资源调整冷却期配置: actionType=%s, ruleGroupUUID=%s", actionType, ruleGroupUUID)
	
	// 使用AdjustOperator中的方法获取规则特定的冷却期
	cooldownSeconds := a.operator.GetAdjustmentCooldownConfig(ctx, actionType, ruleGroupUUID)
	
	log.Printf("[ADJUST-DEBUG] 资源调整冷却期: %d秒", cooldownSeconds)
	return cooldownSeconds
}

// 处理单个告警的调整逻辑
func (a *AdjustAPI) processAlertAdjustment(ctx context.Context, alert routes.Alert, requestID string) bool {
	startTime := time.Now()
	
	// 提取所有标签供后续使用
	domain := alert.Labels["domain"]
	ruleID := alert.Labels["rule_id"]
	actionType := alert.Labels["action_type"]
	ruleGroup := alert.Labels["rule_group"]
	adjustEnabled := alert.Labels["adjust_enabled"] == "true"
	
	log.Printf("[ADJUST-%s] 处理告警: domain=%s, ruleID=%s, actionType=%s, adjustEnabled=%v", 
		requestID, domain, ruleID, actionType, adjustEnabled)
	
	// 参数验证
	if domain == "" || ruleID == "" || actionType == "" {
		log.Printf("[ADJUST-%s] 缺少必要参数: domain=%s, ruleID=%s, actionType=%s",
			requestID, domain, ruleID, actionType)
		return false
	}
	
	fmt.Printf("[处理告警] 域名: %s, 规则: %s, 操作: %s\n", domain, ruleID, actionType)
	
	// 记录详细的标签信息
	log.Printf("[ADJUST-%s] 告警状态: %s", requestID, alert.Status)
	log.Printf("[ADJUST-%s] 告警标签:", requestID)
	for key, value := range alert.Labels {
		log.Printf("[ADJUST-%s]   %s = %s", requestID, key, value)
	}
	log.Printf("[ADJUST-%s] 告警注释:", requestID)
	for key, value := range alert.Annotations {
		log.Printf("[ADJUST-%s]   %s = %s", requestID, key, value)
	}
	
	// 如果调整未启用，则跳过
	if !adjustEnabled {
		log.Printf("[ADJUST-%s] 资源调整未启用，跳过处理", requestID)
		return true
	}
	
	// 获取冷却期配置
	cooldownSeconds := a.getAdjustmentCooldownConfig(ctx, actionType, ruleGroup)
	
	// 检查是否处于冷却期
	inCooldown, err := a.operator.IsInCooldown(ctx, domain, ruleID, actionType, cooldownSeconds)
	if err != nil {
		log.Printf("[ADJUST-%s] 检查冷却期失败: %v", requestID, err)
	} else if inCooldown {
		log.Printf("[ADJUST-%s] 资源在冷却期内，跳过处理", requestID)
		return true
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
		Details:    fmt.Sprintf("处理 %s (domain: %s)", actionType, domain),
		AdjustTime: time.Now(),
	}
	
	// 发送邮件通知(如果配置了邮件)
	if email := alert.Labels["email"]; email != "" {
		log.Printf("[ADJUST-%s] 发送邮件通知到: %s", requestID, email)
		if err := a.operator.SendAdjustEmail(email, record, domain); err != nil {
			log.Printf("[ADJUST-%s] 发送邮件通知失败: %v", requestID, err)
		}
	}
	
	// 根据操作类型执行相应的资源调整
	var err error
	switch actionType {
	case "limit_cpu":
		log.Printf("[ADJUST-%s] 执行CPU限制操作", requestID)
		err = a.operator.AdjustCPUResource(ctx, record, domain, true)
	case "restore_cpu":
		log.Printf("[ADJUST-%s] 执行CPU恢复操作", requestID)
		err = a.operator.RestoreCPUResource(ctx, record, domain)
	case "limit_in_bw":
		log.Printf("[ADJUST-%s] 执行入站带宽限制操作, 设备: %s", requestID, record.TargetDevice)
		err = a.operator.AdjustBandwidthResource(ctx, record, domain, record.TargetDevice, true)
	case "restore_in_bw":
		log.Printf("[ADJUST-%s] 执行入站带宽恢复操作, 设备: %s", requestID, record.TargetDevice)
		err = a.operator.RestoreBandwidthResource(ctx, record, domain, record.TargetDevice)
	case "limit_out_bw":
		log.Printf("[ADJUST-%s] 执行出站带宽限制操作, 设备: %s", requestID, record.TargetDevice)
		err = a.operator.AdjustBandwidthResource(ctx, record, domain, record.TargetDevice, true)
	case "restore_out_bw":
		log.Printf("[ADJUST-%s] 执行出站带宽恢复操作, 设备: %s", requestID, record.TargetDevice)
		err = a.operator.RestoreBandwidthResource(ctx, record, domain, record.TargetDevice)
	default:
		log.Printf("[ADJUST-%s] 未知的调整类型: %s", requestID, actionType)
		history.Status = "failed"
		history.Details = fmt.Sprintf("未知的调整类型: %s", actionType)
		if dbErr := a.operator.SaveAdjustmentHistory(ctx, history); dbErr != nil {
			log.Printf("[ADJUST-%s] 保存调整历史记录失败: %v", requestID, dbErr)
		}
		return false
	}
	
	// 更新历史记录状态
	if err != nil {
		log.Printf("[ADJUST-%s] 处理失败: %v", requestID, err)
		history.Status = "failed"
		history.Details = fmt.Sprintf("处理 %s 失败: %v", actionType, err)
	} else {
		log.Printf("[ADJUST-%s] 处理成功", requestID)
		history.Status = "completed"
		history.Details = fmt.Sprintf("成功处理 %s (domain: %s)", actionType, domain)
	}
	
	// 保存调整历史
	if dbErr := a.operator.SaveAdjustmentHistory(ctx, history); dbErr != nil {
		log.Printf("[ADJUST-%s] 保存调整历史记录失败: %v", requestID, dbErr)
	}
	
	elapsed := time.Since(startTime)
	log.Printf("[ADJUST-%s] 处理完成，耗时: %v", requestID, elapsed)
	fmt.Printf("[处理完成] 域名: %s, 结果: %v, 耗时: %v\n", domain, err == nil, elapsed)
	
	return err == nil
}

// 资源调整配置文件路径常量
const (
	PrometheusBasePath = "/etc/prometheus"
	RulesGeneralPath  = PrometheusBasePath + "/general_rules"
	RulesSpecialPath  = PrometheusBasePath + "/special_rules"
	RulesEnabledPath  = PrometheusBasePath + "/rules_enabled"
	
	// 模板文件
	CPUAdjustRuleTemplate = "VM-cpu-adjust-rule.yml.j2"
	ResourceAdjustAlertsTemplate = "resource-adjustment-alerts.yml.j2"
	InBWAdjustRuleTemplate = "VM-in-bw-adjust-rule.yml.j2"
	OutBWAdjustRuleTemplate = "VM-out-bw-adjust-rule.yml.j2"
)

// CreateBWAdjustRule 创建带宽调整规则
func (a *AdjustAPI) CreateBWAdjustRule(c *gin.Context) {
	var req struct {
		Name             string   `json:"name" binding:"required"`
		Owner            string   `json:"owner" binding:"required"`
		Email            string   `json:"email"`
		AdjustEnabled    bool     `json:"adjust_enabled"`
		InEnabled        bool     `json:"in_enabled"`
		InHighThreshold  int64    `json:"in_high_threshold"`
		InLowThreshold   int64    `json:"in_low_threshold"`
		OutEnabled       bool     `json:"out_enabled"`
		OutHighThreshold int64    `json:"out_high_threshold"`
		OutLowThreshold  int64    `json:"out_low_threshold"`
		SmoothWindow     int      `json:"smooth_window"`
		TriggerDuration  int      `json:"trigger_duration"`
		RestoreDuration  int      `json:"restore_duration"`
		TargetDevice     string   `json:"target_device"`
		LinkedVMs        []string `json:"linkedvms"`
	}
	
	log.Printf("[ADJUST-INFO] 创建带宽调整规则，请求IP: %s", c.ClientIP())
	
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ADJUST-ERROR] 参数解析失败: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// 验证规则有效性
	if !req.InEnabled && !req.OutEnabled {
		log.Printf("[ADJUST-ERROR] 入站和出站带宽调整都未启用")
		c.JSON(http.StatusBadRequest, gin.H{"error": "入站和出站带宽调整必须至少启用一个"})
		return
	}
	
	// 创建规则组
	ruleType := model.RuleTypeAdjustInBW
	if req.OutEnabled && !req.InEnabled {
		ruleType = model.RuleTypeAdjustOutBW
	} else if req.OutEnabled && req.InEnabled {
		ruleType = model.RuleTypeAdjustInBW // 如果两者都启用，优先用入站类型
	}
	
	group := &model.AdjustRuleGroup{
		Name:          req.Name,
		Type:          ruleType,
		Owner:         req.Owner,
		Enabled:       true,
		Email:         req.Email,
		AdjustEnabled: req.AdjustEnabled,
	}
	
	log.Printf("[ADJUST-INFO] 创建带宽调整规则组: name=%s, type=%s", req.Name, ruleType)
	if err := a.operator.CreateAdjustRuleGroup(c.Request.Context(), group); err != nil {
		log.Printf("[ADJUST-ERROR] 创建规则组失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule group failed: " + err.Error()})
		return
	}
	
	// 创建规则详情
	detail := &model.BWAdjustRuleDetail{
		GroupUUID:        group.UUID,
		Name:             req.Name,
		InHighThreshold:  req.InHighThreshold,
		InLowThreshold:   req.InLowThreshold,
		OutHighThreshold: req.OutHighThreshold,
		OutLowThreshold:  req.OutLowThreshold,
		SmoothWindow:     req.SmoothWindow,
		TriggerDuration:  req.TriggerDuration,
		RestoreDuration:  req.RestoreDuration,
	}
	
	// 应用默认值
	if detail.InHighThreshold == 0 {
		detail.InHighThreshold = 10485760 // 默认10MB/s
	}
	if detail.InLowThreshold == 0 {
		detail.InLowThreshold = 5242880 // 默认5MB/s
	}
	if detail.OutHighThreshold == 0 {
		detail.OutHighThreshold = 10485760 // 默认10MB/s
	}
	if detail.OutLowThreshold == 0 {
		detail.OutLowThreshold = 5242880 // 默认5MB/s
	}
	if detail.SmoothWindow == 0 {
		detail.SmoothWindow = 5
	}
	if detail.TriggerDuration == 0 {
		detail.TriggerDuration = 30
	}
	if detail.RestoreDuration == 0 {
		detail.RestoreDuration = 300
	}
	
	log.Printf("[ADJUST-INFO] 创建带宽调整规则详情: inEnabled=%v, outEnabled=%v", req.InEnabled, req.OutEnabled)
	if err := a.operator.CreateBWAdjustRuleDetail(c.Request.Context(), detail); err != nil {
		log.Printf("[ADJUST-ERROR] 创建规则详情失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
		return
	}
	
	// 链接VM
	if len(req.LinkedVMs) > 0 {
		log.Printf("[ADJUST-INFO] 链接虚拟机: count=%d", len(req.LinkedVMs))
		// 使用现有的链接函数
		alarmOperator := routes.NewAlarmOperator()
		_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = alarmOperator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")
		
		// 更新matched_vms.json
		alarmAPI := &AlarmAPI{operator: routes.NewAlarmOperator()}
		_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add")
	}
	
	// 生成规则文件
	ruleData := map[string]interface{}{
		"rule_group":        group.UUID,
		"owner":             req.Owner,
		"email":             req.Email,
		"adjust_enabled":    req.AdjustEnabled,
		"target_device":     req.TargetDevice,
		"smooth_window":     detail.SmoothWindow,
		"trigger_duration":  detail.TriggerDuration,
		"restore_duration":  detail.RestoreDuration,
	}
	
	// 生成入站带宽调整规则
	if req.InEnabled {
		log.Printf("[ADJUST-INFO] 生成入站带宽调整规则模板")
		inRuleData := make(map[string]interface{})
		for k, v := range ruleData {
			inRuleData[k] = v
		}
		inRuleData["in_high_threshold"] = detail.InHighThreshold
		inRuleData["in_low_threshold"] = detail.InLowThreshold
		
		if err := routes.ProcessTemplate(InBWAdjustRuleTemplate, fmt.Sprintf("in-bw-adjust-%s-%s.yml", req.Owner, group.UUID), inRuleData); err != nil {
			log.Printf("[ADJUST-ERROR] 生成入站带宽调整规则失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render in-bw adjust rule"})
			return
		}
	}
	
	// 生成出站带宽调整规则
	if req.OutEnabled {
		log.Printf("[ADJUST-INFO] 生成出站带宽调整规则模板")
		outRuleData := make(map[string]interface{})
		for k, v := range ruleData {
			outRuleData[k] = v
		}
		outRuleData["out_high_threshold"] = detail.OutHighThreshold
		outRuleData["out_low_threshold"] = detail.OutLowThreshold
		
		if err := routes.ProcessTemplate(OutBWAdjustRuleTemplate, fmt.Sprintf("out-bw-adjust-%s-%s.yml", req.Owner, group.UUID), outRuleData); err != nil {
			log.Printf("[ADJUST-ERROR] 生成出站带宽调整规则失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render out-bw adjust rule"})
			return
		}
	}
	
	// 生成告警规则
	log.Printf("[ADJUST-INFO] 生成资源调整告警规则")
	if err := routes.ProcessTemplate(ResourceAdjustAlertsTemplate, fmt.Sprintf("resource-adjust-alerts-%s-%s.yml", req.Owner, group.UUID), ruleData); err != nil {
		log.Printf("[ADJUST-ERROR] 生成资源调整告警规则失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render resource adjustment alerts"})
		return
	}
	
	// 重新加载Prometheus
	routes.ReloadPrometheus()
	
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": group.UUID,
			"enabled":    true,
			"linkedvms": req.LinkedVMs,
			"in_enabled": req.InEnabled,
			"out_enabled": req.OutEnabled,
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
	
	log.Printf("[ADJUST-INFO] 获取带宽调整规则: page=%d, pageSize=%d, uuid=%s", page, pageSize, groupUUID)
	
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
		log.Printf("[ADJUST-ERROR] 查询带宽调整规则组失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query rules group failed: " + err.Error()})
		return
	}
	
	responseData := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		details, err := a.operator.GetBWAdjustRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			log.Printf("[ADJUST-ERROR] 获取带宽调整规则详情失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "get bw adjust rule details failed: " + err.Error()})
			return
		}
		
		linkedVMs := make([]string, 0)
		alarmOperator := routes.NewAlarmOperator()
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
				"name":              detail.Name,
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
	
	log.Printf("[ADJUST-INFO] 删除带宽调整规则: uuid=%s", uuid)
	
	group, err := a.operator.GetAdjustRulesByGroupUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[ADJUST-ERROR] 带宽调整规则不存在: %s", uuid)
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found", "code": "NOT_FOUND"})
		return
	} else if err != nil {
		log.Printf("[ADJUST-ERROR] 查询带宽调整规则失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule information"})
		return
	}
	
	if group.Type != model.RuleTypeAdjustInBW && group.Type != model.RuleTypeAdjustOutBW {
		log.Printf("[ADJUST-ERROR] 无效的规则类型: %s", group.Type)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type", "code": "INVALID_RULE_TYPE"})
		return
	}
	
	// 删除链接的VM
	alarmOperator := routes.NewAlarmOperator()
	_, _ = alarmOperator.DeleteVMLink(c.Request.Context(), uuid, "", "")
	
	// 更新matched_vms.json
	alarmAPI := &AlarmAPI{operator: routes.NewAlarmOperator()}
	_ = alarmAPI.updateMatchedVMsJSON(c.Request.Context(), []string{}, uuid, "remove")
	
	// 确定文件路径
	var inRulePath, outRulePath, alertPath string
	if group.Owner == "admin" {
		inRulePath = fmt.Sprintf("%s/in-bw-adjust-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
		outRulePath = fmt.Sprintf("%s/out-bw-adjust-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesGeneralPath, group.Owner, uuid)
	} else {
		inRulePath = fmt.Sprintf("%s/in-bw-adjust-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
		outRulePath = fmt.Sprintf("%s/out-bw-adjust-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
		alertPath = fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", RulesSpecialPath, group.Owner, uuid)
	}
	
	// 确定symlink路径
	inRuleLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(inRulePath))
	outRuleLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(outRulePath))
	alertLinkPath := fmt.Sprintf("%s/%s", RulesEnabledPath, filepath.Base(alertPath))
	
	// 删除symlink和规则文件
	deletedFiles := []string{}
	// 删除入站规则文件
	if err := os.Remove(inRuleLinkPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, inRuleLinkPath)
	}
	if err := os.Remove(inRulePath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, inRulePath)
	}
	
	// 删除出站规则文件
	if err := os.Remove(outRuleLinkPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, outRuleLinkPath)
	}
	if err := os.Remove(outRulePath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, outRulePath)
	}
	
	// 删除告警规则文件
	if err := os.Remove(alertLinkPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, alertLinkPath)
	}
	if err := os.Remove(alertPath); err == nil || os.IsNotExist(err) {
		deletedFiles = append(deletedFiles, alertPath)
	}
	
	// 删除数据库记录
	err = a.operator.DeleteAdjustRuleGroupWithDependencies(c.Request.Context(), uuid)
	if err != nil {
		log.Printf("[ADJUST-ERROR] 删除规则组及其依赖失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete rule: " + err.Error()})
		return
	}
	
	// 重新加载Prometheus
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("[ADJUST-WARN] 重新加载Prometheus失败: %v", err)
	}
	
	log.Printf("[ADJUST-INFO] 删除带宽调整规则成功: uuid=%s", uuid)
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_files": deletedFiles,
			"group_uuid":    uuid,
		},
	})
} 