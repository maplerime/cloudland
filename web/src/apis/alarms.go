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
//   - ruleType: rule type ("cpu" or "bw") for generating typed rule_id
func (a *AlarmAPI) updateMatchedVMsJSON(ctx context.Context, vmUUIDs []string, groupUUID, operation, ruleType string, targetDevice ...string) error {
	// Path to matched_vms.json file
	matchedVMsFile := "/etc/prometheus/lists/matched_vms.json"

	// Read existing matched_vms.json
	var matchedVMs []map[string]interface{}
	existingData, err := routes.ReadFile(matchedVMsFile)
	if err == nil && len(existingData) > 0 {
		if err := json.Unmarshal(existingData, &matchedVMs); err != nil {
			log.Printf("Failed to parse existing matched_vms.json: %v", err)
			// Even if parsing fails, initialize an empty array to avoid losing operations
			matchedVMs = []map[string]interface{}{}
		}
	} else {
		// File doesn't exist or is empty, create new array
		matchedVMs = []map[string]interface{}{}
		log.Printf("Creating new matched_vms.json file")
	}

	// Process based on operation type
	if operation == "add" {
		log.Printf("Adding/updating VM mappings for rule group %s, VM count: %d", groupUUID, len(vmUUIDs))
		// Add or update VM entries
		for _, instanceid := range vmUUIDs {
			domain, err := routes.GetDomainByInstanceUUID(ctx, instanceid)
			if err != nil {
				log.Printf("Failed to get domain for instanceid=%s: %v", instanceid, err)
				continue
			}

			// Create new entry with instance_id field and typed rule_id
			ruleID := fmt.Sprintf("%s-%s-%s", ruleType, domain, groupUUID)

			// Extract target_device value from variadic parameter
			var targetDeviceValue string
			if len(targetDevice) > 0 {
				targetDeviceValue = targetDevice[0]
			}

			newEntry := map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels": map[string]interface{}{
					"domain":        domain,
					"rule_id":       ruleID,
					"instance_id":   instanceid,
					"target_device": targetDeviceValue, // Add target_device field
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
				existingRuleID, hasRuleID := labels["rule_id"].(string)
				expectedRuleID := ruleID

				if hasDomain && hasRuleID && domainVal == domain && existingRuleID == expectedRuleID {
					// Update existing entry
					entryExists = true
					matchedVMs[i] = newEntry
					log.Printf("Updating existing mapping: domain=%s, rule_id=%s, instance_id=%s", domain, expectedRuleID, instanceid)
					break
				}
			}

			// If it doesn't exist, add a new entry
			if !entryExists {
				matchedVMs = append(matchedVMs, newEntry)
				log.Printf("Adding new mapping: domain=%s, rule_id=%s-%s, instance_id=%s", domain, domain, groupUUID, instanceid)
			}
		}
	} else if operation == "remove" {
		log.Printf("Removing VM mappings for rule group %s", groupUUID)
		// 如果传入了 targetDevice（可选变参非空），则按三元组精确删除；
		// 否则沿用原有“按 groupUUID 全量删除”的逻辑（保持不变）。
		hasDeviceFilter := len(targetDevice) > 0

		filteredVMs := []map[string]interface{}{}
		removedCount := 0

		for _, vm := range matchedVMs {
			labels, ok := vm["labels"].(map[string]interface{})
			if !ok {
				filteredVMs = append(filteredVMs, vm)
				continue
			}

			ruleID, ok := labels["rule_id"].(string)
			if !ok {
				filteredVMs = append(filteredVMs, vm)
				continue
			}

			// 未带设备过滤：保持原逻辑，按 group 全量删除
			if !hasDeviceFilter {
				if strings.HasSuffix(ruleID, "-"+groupUUID) {
					domain, _ := labels["domain"].(string)
					instanceID, _ := labels["instance_id"].(string)
					log.Printf("Removing mapping: domain=%s, rule_id=%s, instance_id=%s", domain, ruleID, instanceID)
					removedCount++
					continue
				}
				filteredVMs = append(filteredVMs, vm)
				continue
			}

			// 带设备过滤：要求同时命中
			// 1) 属于该 group
			// 2) instance_id 在 vmUUIDs 中
			// 3) target_device 在 targetDevice 中
			if !strings.HasSuffix(ruleID, "-"+groupUUID) {
				filteredVMs = append(filteredVMs, vm)
				continue
			}

			instanceID, _ := labels["instance_id"].(string)
			devStr, _ := labels["target_device"].(string)

			inVM := false
			for _, id := range vmUUIDs {
				if id == instanceID {
					inVM = true
					break
				}
			}
			inDev := false
			for _, d := range targetDevice {
				if d == devStr {
					inDev = true
					break
				}
			}

			if inVM && inDev {
				domain, _ := labels["domain"].(string)
				log.Printf("Removing mapping by triple: domain=%s, rule_id=%s, instance_id=%s, target_device=%s",
					domain, ruleID, instanceID, devStr)
				removedCount++
				continue
			}

			// 未命中三元组条件，则保留
			filteredVMs = append(filteredVMs, vm)
		}

		matchedVMs = filteredVMs
		log.Printf("Removed %d mappings for rule group %s", removedCount, groupUUID)
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

	// Force reload Prometheus configuration
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Warning: Failed to reload Prometheus after updating matched_vms.json: %v", err)
		// Don't return error as the file update was successful
	} else {
		log.Printf("Successfully reloaded Prometheus configuration after updating matched_vms.json")
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
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.VMUUIDs, group.UUID, "add", "alarm-"+group.Type)
	} else {
		// If no VMs, remove related entries from matched_vms.json
		_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{}, group.UUID, "remove", "alarm-"+group.Type)
	}

	// Query latest linked VMs
	vmLinks, _ := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	linkedVMs := []string{}
	for _, link := range vmLinks {
		linkedVMs = append(linkedVMs, link.VMUUID)
	}

	// 强制重新加载Prometheus配置，确保规则和匹配列表同时生效
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Warning: Failed to reload Prometheus after linking VMs: %v", err)
		// 不返回错误，因为VM链接操作已经成功
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
		Name            string           `json:"name" binding:"required"`
		Owner           string           `json:"owner" binding:"required"`
		Rules           []routes.CPURule `json:"rules" binding:"required,min=1"`
		LinkedVMs       []string         `json:"linkedvms"`
		RegionID        string           `json:"region_id"`
		Level           string           `json:"level"`
		DurationMinutes int              `json:"duration_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := &model.RuleGroupV2{
		Name:            req.Name,
		Type:            routes.RuleTypeCPU,
		Owner:           req.Owner,
		Enabled:         true,
		RegionID:        req.RegionID,
		Level:           req.Level,
		DurationMinutes: req.DurationMinutes,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operator failed: " + err.Error()})
		return
	}
	for _, rule := range req.Rules {
		detail := &model.CPURuleDetail{
			GroupUUID:    group.UUID,
			Name:         rule.Name,
			Limit:        rule.Limit,    // 新增：存储阈值
			Rule:         rule.Rule,     // 新增：存储比较操作符(gt/lt)
			Duration:     rule.Duration, // 持续时间(分钟)
			Over:         rule.Over,     // 保持兼容性
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
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add", "alarm-cpu")
	}
	// 校验：一次只能创建一个规则
	if len(req.Rules) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only one rule can be created at a time."})
		return
	}

	// 只处理第一个规则
	rule := req.Rules[0]
	// 将gt/lt转换为>/< 符号用于模板
	var ruleOperator string
	switch rule.Rule {
	case "gt":
		ruleOperator = ">"
	case "lt":
		ruleOperator = "<"
	default:
		ruleOperator = ">" // 默认为大于
	}

	ruleData := map[string]interface{}{
		"owner":      req.Owner,
		"rule_group": group.UUID,
		"name":       rule.Name,
		// 模板需要的字段
		"rule_operator":    ruleOperator,                                          // 转换后的比较操作符 >/<
		"limit_value":      rule.Limit,                                            // 阈值
		"duration_minutes": rule.Duration,                                         // 持续时间(分钟)
		"rule_id":          fmt.Sprintf("alarm-cpu-%s-%s", req.Owner, group.UUID), // 规则ID
		"region_id":        req.RegionID,
		"level":            req.Level,
		// 保持兼容性的旧字段
		"over":          rule.Over,
		"duration":      rule.Duration,
		"down_to":       rule.DownTo,
		"down_duration": rule.DownDuration,
	}

	templateFile := "VM-cpu-rule.yml.j2"
	outputFile := fmt.Sprintf("cpu-%s-%s.yml", req.Owner, group.UUID) // 只传文件名
	if err := routes.ProcessTemplate(templateFile, outputFile, ruleData); err != nil {
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

func (a *AlarmAPI) CreateMemoryRule(c *gin.Context) {
	var req struct {
		Name            string              `json:"name" binding:"required"`
		Owner           string              `json:"owner" binding:"required"`
		Rules           []routes.MemoryRule `json:"rules" binding:"required,min=1"`
		LinkedVMs       []string            `json:"linkedvms"`
		RegionID        string              `json:"region_id"`
		Level           string              `json:"level"`
		DurationMinutes int                 `json:"duration_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := &model.RuleGroupV2{
		Name:            req.Name,
		Type:            routes.RuleTypeMemory,
		Owner:           req.Owner,
		Enabled:         true,
		RegionID:        req.RegionID,
		Level:           req.Level,
		DurationMinutes: req.DurationMinutes,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operator failed: " + err.Error()})
		return
	}
	for _, rule := range req.Rules {
		detail := &model.MemoryRuleDetail{
			GroupUUID:    group.UUID,
			Name:         rule.Name,
			Limit:        rule.Limit,    // 新增：存储阈值
			Rule:         rule.Rule,     // 新增：存储比较操作符(gt/lt)
			Duration:     rule.Duration, // 持续时间(分钟)
			Over:         rule.Over,     // 保持兼容性
			DownDuration: rule.DownDuration,
			DownTo:       rule.DownTo,
		}
		if err := a.operator.CreateMemoryRuleDetail(c.Request.Context(), detail); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}
	}
	if len(req.LinkedVMs) > 0 {
		// Full overwrite of VMRuleLink
		_, _ = a.operator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")
		_ = a.operator.BatchLinkVMs(c.Request.Context(), group.UUID, req.LinkedVMs, "")

		// Update matched_vms.json with VM information
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add", "alarm-memory")
	}
	// 校验：一次只能创建一个规则
	if len(req.Rules) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only one rule can be created at a time."})
		return
	}

	// 只处理第一个规则
	rule := req.Rules[0]
	// 将gt/lt转换为>/< 符号用于模板
	var ruleOperator string
	switch rule.Rule {
	case "gt":
		ruleOperator = ">"
	case "lt":
		ruleOperator = "<"
	default:
		ruleOperator = ">" // 默认为大于
	}

	ruleData := map[string]interface{}{
		"owner":      req.Owner,
		"rule_group": group.UUID,
		"name":       rule.Name,
		// 模板需要的字段
		"rule_operator":    ruleOperator,                                             // 转换后的比较操作符 >/<
		"limit_value":      rule.Limit,                                               // 阈值
		"duration_minutes": rule.Duration,                                            // 持续时间(分钟)
		"rule_id":          fmt.Sprintf("alarm-memory-%s-%s", req.Owner, group.UUID), // 规则ID
		"region_id":        req.RegionID,
		"level":            req.Level,
		// 保持兼容性的旧字段
		"over":          rule.Over,
		"duration":      rule.Duration,
		"down_to":       rule.DownTo,
		"down_duration": rule.DownDuration,
	}

	templateFile := "VM-memory-rule.yml.j2"
	outputFile := fmt.Sprintf("memory-%s-%s.yml", req.Owner, group.UUID) // 只传文件名
	if err := routes.ProcessTemplate(templateFile, outputFile, ruleData); err != nil {
		log.Printf("Failed to render memory rule template: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render memory rule template"})
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
				"id":        d.ID,
				"rule_uuid": d.UUID,
				"name":      d.Name,
				"limit":     d.Limit,    // 新增：阈值
				"rule":      d.Rule,     // 新增：比较操作符(gt/lt)
				"duration":  d.Duration, // 持续时间(分钟)
				// 保持兼容性的旧字段
				"over":          d.Over,
				"down_to":       d.DownTo,
				"down_duration": d.DownDuration,
			})
		}

		responseData = append(responseData, gin.H{
			"id":               group.ID,
			"group_uuid":       group.UUID,
			"name":             group.Name,
			"trigger_cnt":      group.TriggerCnt,
			"create_time":      group.CreatedAt.Format(time.RFC3339),
			"rules":            ruleDetails,
			"enabled":          group.Enabled,
			"linked_vms":       linkedVMs,
			"region_id":        group.RegionID,
			"level":            group.Level,
			"duration_minutes": group.DurationMinutes,
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

func (a *AlarmAPI) GetMemoryRules(c *gin.Context) {
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
		RuleType: routes.RuleTypeMemory,
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
		details, err := a.operator.GetMemoryRuleDetails(c.Request.Context(), group.UUID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "function get memory rule detailed failed: " + err.Error()})
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
				"id":        d.ID,
				"rule_uuid": d.UUID,
				"name":      d.Name,
				"limit":     d.Limit,    // 新增：阈值
				"rule":      d.Rule,     // 新增：比较操作符(gt/lt)
				"duration":  d.Duration, // 持续时间(分钟)
				// 保持兼容性的旧字段
				"over":          d.Over,
				"down_to":       d.DownTo,
				"down_duration": d.DownDuration,
			})
		}

		responseData = append(responseData, gin.H{
			"id":               group.ID,
			"group_uuid":       group.UUID,
			"name":             group.Name,
			"trigger_cnt":      group.TriggerCnt,
			"create_time":      group.CreatedAt.Format(time.RFC3339),
			"rules":            ruleDetails,
			"enabled":          group.Enabled,
			"linked_vms":       linkedVMs,
			"region_id":        group.RegionID,
			"level":            group.Level,
			"duration_minutes": group.DurationMinutes,
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
	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "Rule not found: The specified rule does not exist",
				"code":  "NOT_FOUND",
				"uuid":  groupUUID,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to retrieve rule information",
				"code":  "INTERNAL_ERROR",
				"uuid":  groupUUID,
			})
		}
		return
	}

	// 确认规则类型是否正确
	if group.Type != routes.RuleTypeCPU {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Rule type mismatch: Expected CPU rule but found " + group.Type,
			"code":  "INVALID_RULE_TYPE",
			"uuid":  groupUUID,
		})
		return
	}
	owner := group.Owner

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
	mappingFile := ""
	_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{}, groupUUID, "remove", "alarm-cpu")

	// 删除软链和规则文件（路径与创建时一致）
	fileName := fmt.Sprintf("cpu-%s-%s.yml", owner, groupUUID)
	linkPath := filepath.Join(routes.RulesEnabled, fileName)

	// 根据owner决定规则文件位置
	var rulePath string
	if owner == "admin" {
		rulePath = filepath.Join(routes.RulesGeneral, fileName)
	} else {
		rulePath = filepath.Join(routes.RulesSpecial, fileName)
	}

	// 记录删除的文件路径
	deletedFiles := []string{}

	// 删除软链
	if err := routes.RemoveFile(linkPath); err == nil {
		deletedFiles = append(deletedFiles, linkPath)
	}

	// 删除规则文件
	if err := routes.RemoveFile(rulePath); err == nil {
		deletedFiles = append(deletedFiles, rulePath)
	}

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

	// 如果有映射文件，也添加到删除列表
	if mappingFile != "" {
		deletedFiles = append(deletedFiles, mappingFile)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": deletedFiles,
			"linked_vms":    linkedVMs,
		},
	})
}

func (a *AlarmAPI) DeleteMemoryRule(c *gin.Context) {
	groupUUID := c.Param("uuid")
	if groupUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty UUID error."})
		return
	}

	// Verify rule group exists and is of correct type
	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
		return
	} else if err != nil {
		log.Printf("Failed to retrieve rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	if group.Type != routes.RuleTypeMemory {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Rule type mismatch: Expected Memory rule but found " + group.Type,
		})
		return
	}

	// Query linked VMs before deletion
	linkedVMs := make([]string, 0)
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err == nil {
		for _, link := range vmLinks {
			linkedVMs = append(linkedVMs, link.VMUUID)
		}
	}

	// Remove from matched_vms.json
	if len(linkedVMs) > 0 {
		_ = a.updateMatchedVMsJSON(c.Request.Context(), linkedVMs, groupUUID, "remove", "alarm-memory")
	}

	owner := group.Owner
	deletedFiles := make([]string, 0)

	// Generate file paths
	fileName := fmt.Sprintf("memory-%s-%s.yml", owner, groupUUID)
	rulePath := filepath.Join(routes.RulesEnabled, fileName)

	// Generate mapping file path
	mappingFile := ""
	if len(linkedVMs) > 0 {
		mappingFileName := fmt.Sprintf("alarm-memory-%s-%s.json", owner, groupUUID)
		mappingFile = filepath.Join(routes.RulesEnabled, mappingFileName)
	}

	// Delete mapping file first (if exists)
	if mappingFile != "" {
		if err := routes.RemoveFile(mappingFile); err == nil {
			deletedFiles = append(deletedFiles, mappingFile)
		}
	}

	// Delete link file (if exists)
	linkPath := filepath.Join(routes.RulesEnabled, fmt.Sprintf("link-memory-%s-%s.yml", owner, groupUUID))
	if err := routes.RemoveFile(linkPath); err == nil {
		deletedFiles = append(deletedFiles, linkPath)
	}

	// Delete rule file
	if err := routes.RemoveFile(rulePath); err == nil {
		deletedFiles = append(deletedFiles, rulePath)
	}

	// Delete rule-related table data
	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, routes.RuleTypeMemory); err != nil {
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

	// If there's a mapping file, also add it to the deletion list
	if mappingFile != "" {
		deletedFiles = append(deletedFiles, mappingFile)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": deletedFiles,
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
		// 打印所有标签信息
		log.Printf("ProcessAlertWebhook All labels for alert: %v", alert.Labels)

		alert_type := alert.Labels["alert_type"]
		alertName := alert.Labels["alertname"]
		severity := alert.Labels["severity"]
		owner := alert.Labels["owner"]
		domain := alert.Labels["domain"]
		rule_group_uuid := alert.Labels["rule_group"]
		matched := alert.Labels["matched"]

		log.Printf("ProcessAlertWebhook Processing alert: alert_type=%s alertName=%s severity=%s", alert_type, alertName, severity)
		log.Printf("ProcessAlertWebhook Processing alert: domain=%s rule_group_uuid=%s", domain, rule_group_uuid)
		log.Printf("ProcessAlertWebhook Processing alert: owner=%s matched=%s", owner, matched)

		description := alert.Annotations["description"]
		summary := alert.Annotations["summary"]
		log.Printf("ProcessAlertWebhook Processing alert: summary=%s description=%s", summary, description)

		target_device := ""
		if alert_type == "bw" {
			target_device = alert.Labels["target_device"]
			log.Printf("ProcessAlertWebhook Processing alert: target_device=%s", target_device)
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
			log.Printf("ProcessAlertWebhook Alert FIRING: domain=%s matched=%s", domain, matched)
			// 通知实时告警系统
			if err := a.notifyRealtimeAlert(alertRecord); err != nil {
				log.Printf("Failed to notify realtime alert: %v", err)
			}
		} else {
			// Resolved: alert resolved
			log.Printf("ProcessAlertWebhook Alert RESOLVED: domain=%s matched=%s", domain, matched)
			log.Printf("ProcessAlertWebhook alert resolved alert: summary=%s alertRecord=%v", summary, alertRecord)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "processed",
		"alerts":  len(notification.Alerts),
		"message": "alarm process completed",
	})
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
		_ = a.updateMatchedVMsJSON(c.Request.Context(), req.LinkedVMs, group.UUID, "add", "alarm-bw")
	}
	// Render templates for in/out directions
	for _, rule := range req.Rules {
		if rule.InEnabled {
			data := map[string]interface{}{
				"owner":            req.Owner,
				"rule_group":       group.UUID,
				"in_threshold":     rule.InThreshold,
				"in_duration":      rule.InDuration,
				"in_down_to":       rule.InDownTo,
				"in_down_duration": rule.InDownDuration,
				"target_device":    req.TargetDevice,
			}
			templateFile := "VM-in-bw-rule.yml.j2"
			outputFile := fmt.Sprintf("bw-in-%s-%s.yml", req.Owner, group.UUID)
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
				"out_threshold":     rule.OutThreshold,
				"out_duration":      rule.OutDuration,
				"out_down_to":       rule.OutDownTo,
				"out_down_duration": rule.OutDownDuration,
				"target_device":     req.TargetDevice,
			}
			templateFile := "VM-out-bw-rule.yml.j2"
			outputFile := fmt.Sprintf("bw-out-%s-%s.yml", req.Owner, group.UUID)
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
	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "Rule not found: The specified rule does not exist",
				"code":  "NOT_FOUND",
				"uuid":  groupUUID,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to retrieve rule information",
				"code":  "INTERNAL_ERROR",
				"uuid":  groupUUID,
			})
		}
		return
	}

	// 确认规则类型是否正确
	if group.Type != routes.RuleTypeBW {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Rule type mismatch: Expected BW rule but found " + group.Type,
			"code":  "INVALID_RULE_TYPE",
			"uuid":  groupUUID,
		})
		return
	}
	owner := group.Owner

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
	_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{}, groupUUID, "remove", "alarm-bw")

	// 删除软链和规则文件（路径与创建时一致）
	// 记录删除的文件路径
	deletedFiles := []string{}

	// 不再需要遍历规则名，直接使用固定格式的文件名
	inFile := fmt.Sprintf("bw-in-%s-%s.yml", owner, groupUUID)
	outFile := fmt.Sprintf("bw-out-%s-%s.yml", owner, groupUUID)
	linkIn := filepath.Join(routes.RulesEnabled, inFile)
	linkOut := filepath.Join(routes.RulesEnabled, outFile)

	// 根据owner决定规则文件位置
	var ruleIn, ruleOut string
	if owner == "admin" {
		ruleIn = filepath.Join(routes.RulesGeneral, inFile)
		ruleOut = filepath.Join(routes.RulesGeneral, outFile)
	} else {
		ruleIn = filepath.Join(routes.RulesSpecial, inFile)
		ruleOut = filepath.Join(routes.RulesSpecial, outFile)
	}

	// 删除软链和规则文件，记录成功删除的文件路径
	if err := routes.RemoveFile(linkIn); err == nil {
		deletedFiles = append(deletedFiles, linkIn)
	}
	if err := routes.RemoveFile(ruleIn); err == nil {
		deletedFiles = append(deletedFiles, ruleIn)
	}
	if err := routes.RemoveFile(linkOut); err == nil {
		deletedFiles = append(deletedFiles, linkOut)
	}
	if err := routes.RemoveFile(ruleOut); err == nil {
		deletedFiles = append(deletedFiles, ruleOut)
	}

	// 记录映射文件路径（如果有）
	mappingFile := ""
	if mappingFile != "" {
		deletedFiles = append(deletedFiles, mappingFile)
	}

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
			"deleted_files": deletedFiles,
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

// SyncAllVMRuleMappings synchronizes all VM-rule mappings to matched_vms.json
// This ensures consistency between database and the mapping file
// @Summary Synchronize all VM rule mappings
// @Description Perform a full synchronization of all VM rule mappings to ensure matched_vms.json is consistent with the database
// @Tags alarm
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{} "Synchronization successful"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /api/v1/metrics/alarm/sync-mappings [post]
func (a *AlarmAPI) SyncAllVMRuleMappings(c *gin.Context) {
	log.Printf("Starting full synchronization of VM rule mappings")

	ctx := c.Request.Context()
	matchedVMsFile := "/etc/prometheus/lists/matched_vms.json"

	// Get all rule groups (both alarm and adjust rules)
	var allRuleGroups []string

	// Get CPU rule groups
	cpuParams := routes.ListRuleGroupsParams{
		RuleType: routes.RuleTypeCPU,
		Page:     1,
		PageSize: 1000,
	}
	cpuGroups, _, err := a.operator.ListRuleGroups(ctx, cpuParams)
	if err != nil {
		log.Printf("Failed to get CPU rule groups: %v", err)
	} else {
		for _, group := range cpuGroups {
			allRuleGroups = append(allRuleGroups, group.UUID)
		}
	}

	// Get BW rule groups
	bwParams := routes.ListRuleGroupsParams{
		RuleType: routes.RuleTypeBW,
		Page:     1,
		PageSize: 1000,
	}
	bwGroups, _, err := a.operator.ListRuleGroups(ctx, bwParams)
	if err != nil {
		log.Printf("Failed to get BW rule groups: %v", err)
	} else {
		for _, group := range bwGroups {
			allRuleGroups = append(allRuleGroups, group.UUID)
		}
	}

	// Get adjust rule groups
	adjustOperator := &routes.AdjustOperator{}

	// Get CPU adjust rule groups
	cpuAdjustParams := routes.ListAdjustRuleGroupsParams{
		RuleType: model.RuleTypeAdjustCPU,
		Page:     1,
		PageSize: 1000,
	}
	cpuAdjustGroups, _, err := adjustOperator.ListAdjustRuleGroups(ctx, cpuAdjustParams)
	if err != nil {
		log.Printf("Failed to get CPU adjust rule groups: %v", err)
	}

	// Get BW adjust rule groups (both in and out)
	inBWAdjustParams := routes.ListAdjustRuleGroupsParams{
		RuleType: model.RuleTypeAdjustInBW,
		Page:     1,
		PageSize: 1000,
	}
	inBWAdjustGroups, _, err := adjustOperator.ListAdjustRuleGroups(ctx, inBWAdjustParams)
	if err != nil {
		log.Printf("Failed to get inbound BW adjust rule groups: %v", err)
	}

	outBWAdjustParams := routes.ListAdjustRuleGroupsParams{
		RuleType: model.RuleTypeAdjustOutBW,
		Page:     1,
		PageSize: 1000,
	}
	outBWAdjustGroups, _, err := adjustOperator.ListAdjustRuleGroups(ctx, outBWAdjustParams)
	if err != nil {
		log.Printf("Failed to get outbound BW adjust rule groups: %v", err)
	}

	// Build complete mapping data
	var allMappings []map[string]interface{}

	// Process CPU rule groups
	for _, group := range cpuGroups {
		groupUUID := group.UUID
		// Get all VMs linked to this rule group
		vmLinks, err := a.operator.GetLinkedVMs(ctx, groupUUID)
		if err != nil {
			log.Printf("Failed to get linked VMs for group %s: %v", groupUUID, err)
			continue
		}

		for _, link := range vmLinks {
			instanceID := link.VMUUID
			domain, err := routes.GetDomainByInstanceUUID(ctx, instanceID)
			if err != nil {
				log.Printf("Failed to get domain for instance %s: %v", instanceID, err)
				continue
			}

			// Create mapping entry for CPU alarm rules
			mapping := map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels": map[string]interface{}{
					"domain":      domain,
					"rule_id":     fmt.Sprintf("alarm-cpu-%s-%s", domain, groupUUID),
					"instance_id": instanceID,
				},
			}

			allMappings = append(allMappings, mapping)
			log.Printf("Added CPU alarm mapping: domain=%s, rule_id=alarm-cpu-%s-%s, instance_id=%s",
				domain, domain, groupUUID, instanceID)
		}
	}

	// Process BW rule groups
	for _, group := range bwGroups {
		groupUUID := group.UUID
		// Get all VMs linked to this rule group
		vmLinks, err := a.operator.GetLinkedVMs(ctx, groupUUID)
		if err != nil {
			log.Printf("Failed to get linked VMs for group %s: %v", groupUUID, err)
			continue
		}

		for _, link := range vmLinks {
			instanceID := link.VMUUID
			domain, err := routes.GetDomainByInstanceUUID(ctx, instanceID)
			if err != nil {
				log.Printf("Failed to get domain for instance %s: %v", instanceID, err)
				continue
			}

			// Create mapping entry for BW alarm rules
			mapping := map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels": map[string]interface{}{
					"domain":        domain,
					"rule_id":       fmt.Sprintf("alarm-bw-%s-%s", domain, groupUUID),
					"instance_id":   instanceID,
					"target_device": link.Interface, // BW rules need target_device
				},
			}

			allMappings = append(allMappings, mapping)
			log.Printf("Added BW alarm mapping: domain=%s, rule_id=alarm-bw-%s-%s, instance_id=%s, target_device=%s",
				domain, domain, groupUUID, instanceID, link.Interface)
		}
	}

	// Process CPU adjust rule groups
	for _, group := range cpuAdjustGroups {
		groupUUID := group.UUID
		// Get all VMs linked to this adjust rule group
		vmLinks, err := a.operator.GetLinkedVMs(ctx, groupUUID)
		if err != nil {
			log.Printf("Failed to get linked VMs for CPU adjust group %s: %v", groupUUID, err)
			continue
		}

		for _, link := range vmLinks {
			instanceID := link.VMUUID
			domain, err := routes.GetDomainByInstanceUUID(ctx, instanceID)
			if err != nil {
				log.Printf("Failed to get domain for instance %s: %v", instanceID, err)
				continue
			}

			// Create mapping entry for CPU adjust rules
			mapping := map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels": map[string]interface{}{
					"domain":      domain,
					"rule_id":     fmt.Sprintf("adjust-cpu-%s-%s", domain, groupUUID),
					"instance_id": instanceID,
				},
			}

			allMappings = append(allMappings, mapping)
			log.Printf("Added CPU adjust mapping: domain=%s, rule_id=adjust-cpu-%s-%s, instance_id=%s",
				domain, domain, groupUUID, instanceID)
		}
	}

	// Process inbound BW adjust rule groups
	for _, group := range inBWAdjustGroups {
		groupUUID := group.UUID
		// Get all VMs linked to this adjust rule group
		vmLinks, err := a.operator.GetLinkedVMs(ctx, groupUUID)
		if err != nil {
			log.Printf("Failed to get linked VMs for inbound BW adjust group %s: %v", groupUUID, err)
			continue
		}

		for _, link := range vmLinks {
			instanceID := link.VMUUID
			domain, err := routes.GetDomainByInstanceUUID(ctx, instanceID)
			if err != nil {
				log.Printf("Failed to get domain for instance %s: %v", instanceID, err)
				continue
			}

			// Create mapping entry for inbound BW adjust rules
			mapping := map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels": map[string]interface{}{
					"domain":        domain,
					"rule_id":       fmt.Sprintf("adjust-bw-%s-%s", domain, groupUUID),
					"instance_id":   instanceID,
					"target_device": link.Interface, // BW adjust rules need target_device
				},
			}

			allMappings = append(allMappings, mapping)
			log.Printf("Added inbound BW adjust mapping: domain=%s, rule_id=adjust-bw-%s-%s, instance_id=%s, target_device=%s",
				domain, domain, groupUUID, instanceID, link.Interface)
		}
	}

	// Process outbound BW adjust rule groups
	for _, group := range outBWAdjustGroups {
		groupUUID := group.UUID
		// Get all VMs linked to this adjust rule group
		vmLinks, err := a.operator.GetLinkedVMs(ctx, groupUUID)
		if err != nil {
			log.Printf("Failed to get linked VMs for outbound BW adjust group %s: %v", groupUUID, err)
			continue
		}

		for _, link := range vmLinks {
			instanceID := link.VMUUID
			domain, err := routes.GetDomainByInstanceUUID(ctx, instanceID)
			if err != nil {
				log.Printf("Failed to get domain for instance %s: %v", instanceID, err)
				continue
			}

			// Create mapping entry for outbound BW adjust rules
			mapping := map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels": map[string]interface{}{
					"domain":        domain,
					"rule_id":       fmt.Sprintf("adjust-bw-%s-%s", domain, groupUUID),
					"instance_id":   instanceID,
					"target_device": link.Interface, // BW adjust rules need target_device
				},
			}

			allMappings = append(allMappings, mapping)
			log.Printf("Added outbound BW adjust mapping: domain=%s, rule_id=adjust-bw-%s-%s, instance_id=%s, target_device=%s",
				domain, domain, groupUUID, instanceID, link.Interface)
		}
	}

	// Save complete mapping data
	mappingData, err := json.MarshalIndent(allMappings, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal matched_vms.json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "Failed to marshal mapping data",
		})
		return
	}

	err = routes.WriteFile(matchedVMsFile, mappingData, 0644)
	if err != nil {
		log.Printf("Failed to write matched_vms.json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "Failed to write mapping file",
		})
		return
	}

	// Force reload Prometheus configuration
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Warning: Failed to reload Prometheus after full sync: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"status":  "partial_success",
			"message": "Mappings synchronized but failed to reload Prometheus",
			"count":   len(allMappings),
		})
		return
	}

	log.Printf("Successfully synchronized all VM rule mappings, total entries: %d", len(allMappings))
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "VM rule mappings synchronized successfully",
		"count":   len(allMappings),
	})
}

// VMAlarmMapping is used for serialization to vm_alarm_mapping.yml
type VMAlarmMapping struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}
