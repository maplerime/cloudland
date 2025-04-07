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
	"unsafe"
	"web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

type AlarmAPI struct {
	operator *common.AlarmOperator
}

var alarmAPI = &AlarmAPI{
	operator: &common.AlarmOperator{},
}

func (a *AlarmAPI) LinkRuleToVM(c *gin.Context) {
	var req struct {
		GroupUUID string `json:"group_uuid" binding:"required"`
		VMUUID    string `json:"vm_uuid" binding:"required"`
		Interface string `json:"interface"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	groupUUID := req.GroupUUID
	vmUUID := req.VMUUID
	Interface := req.Interface
	// Retrieve rule group using operator method
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

	vmName, err := routes.GetDBIndexByInstanceUUID(c, vmUUID)
	log.Printf("[LinkRuleToVM] vmName: %d with vmUUID: %s)\n", vmName, vmUUID)
	if err != nil {
		log.Printf("VM UUID convert failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid vm uuid"})
		return
	}
	existingLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err == nil {
		for _, link := range existingLinks {
			if link.VMUUID == vmUUID && link.Interface == Interface {
				log.Printf("[LinkRuleToVM] same link.VMUUID: %s\n", link.VMUUID)
				c.JSON(http.StatusOK, gin.H{
					"status": "success",
					"data": gin.H{
						"exists":     true,
						"group_uuid": groupUUID,
						"vm_uuid":    vmUUID,
						"vm_name":    vmName,
						"interface":  Interface,
					},
				})
				return
			}
		}
	}

	// Use operator instead of direct DB operations
	if err = a.operator.BatchLinkVMs(c.Request.Context(), groupUUID, []string{vmUUID}, req.Interface); err != nil {
		log.Printf("Failed to link VM to rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create VM association"})
		return
	}

	// Get associated VMs using operator
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("Error getting linked VMs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get VM list"})
		return
	}

	// Build exclusion list
	var vmList []string
	for _, l := range vmLinks {
		log.Printf("convert VMUUID: %s\n", l.VMUUID)
		instanceid, err := routes.GetDBIndexByInstanceUUID(c, l.VMUUID)
		vmName := "inst-" + strconv.Itoa(instanceid)
		if err != nil {
			log.Printf("VM UUID convert failed uuid=%s error=%v", l.VMUUID, err)
			continue
		}
		if group.Type == common.RuleTypeBW && l.Interface != "" {
			vmName = fmt.Sprintf("%s:%s", vmName, l.Interface)
		}
		vmList = append(vmList, vmName)
	}

	var generalContent, specialContent string

	// 根据规则类型生成不同的规则内容
	if group.Type == common.RuleTypeCPU {
		type ExtendedGroup struct {
			model.RuleGroupV2
			Details []model.CPURuleDetail
		}
		details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
		rules := make([]common.CPURule, 0, len(details))
		for _, d := range details {
			rules = append(rules, common.CPURule{
				Name:         d.Name,
				Duration:     d.Duration,
				Over:         d.Over,
				DownDuration: d.DownDuration,
				DownTo:       d.DownTo,
			})
		}

		// Generate CPU rule content
		generalContent, err = generateCPURuleContentV2(rules, group.Name, groupUUID, vmList, true)
		if err != nil {
			log.Printf("Rule content generation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule content generation failed"})
			return
		}
		specialContent, err = generateCPURuleContentV2(rules, group.Name, groupUUID, vmList, false)
		if err != nil {
			log.Printf("Special rule generation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate special rules"})
			return
		}
	} else if group.Type == common.RuleTypeBW {
		type ExtendedGroup struct {
			model.RuleGroupV2
			Details []model.BWRuleDetail
		}
		details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
		rules := make([]common.BWRule, 0, len(details))
		for _, d := range details {
			inEnabled := d.InThreshold >= 0 && d.InDuration >= 0
			outEnabled := d.OutThreshold >= 0 && d.OutDuration >= 0

			rules = append(rules, common.BWRule{
				Name:            d.Name,
				InEnabled:       inEnabled,
				InThreshold:     d.InThreshold,
				InDuration:      d.InDuration,
				InOverType:      d.InOverType,
				InDownTo:        d.InDownTo,
				InDownDuration:  d.InDownDuration,
				OutEnabled:      outEnabled,
				OutThreshold:    d.OutThreshold,
				OutDuration:     d.OutDuration,
				OutOverType:     d.OutOverType,
				OutDownTo:       d.OutDownTo,
				OutDownDuration: d.OutDownDuration,
			})
		}
		var linkedVMInterfaces []string
		for _, l := range vmLinks {
			instanceid, err := routes.GetDBIndexByInstanceUUID(c, l.VMUUID)
			if err != nil {
				log.Printf("VM UUID convert failed uuid=%s error=%v", l.VMUUID, err)
				continue
			}
			vmName := "inst-" + strconv.Itoa(instanceid)
			if l.Interface != "" {
				vmName = fmt.Sprintf("%s:%s", vmName, l.Interface)
			}
			linkedVMInterfaces = append(linkedVMInterfaces, vmName)
		}
		specialContent, err = generateBWRuleContent(rules, group.Name, groupUUID, linkedVMInterfaces, true, "")
		if err != nil {
			log.Printf("general BW rule content generation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "BW rule content generation failed"})
			return
		}

		generalContent, err = generateBWRuleContent(rules, group.Name, groupUUID, linkedVMInterfaces, false, "")
		if err != nil {
			log.Printf("Special BW rule generation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate special BW rules"})
			return
		}
	} else {
		log.Printf("Unsupported rule type: %s", group.Type)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported rule type"})
		return
	}

	// Generate rule content
	generalPath, specialPath := common.RulePaths(group.Type, groupUUID)

	// Write rule files
	if err = common.WriteFile(generalPath, []byte(generalContent), 0640); err != nil {
		log.Printf("Failed to write general rules: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "General rule file reflash failed"})
		return
	}

	if err := common.WriteFile(specialPath, []byte(specialContent), 0640); err != nil {
		log.Printf("Failed to write special rules: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Special rule file reflash failed"})
		return
	}

	// Activate rules
	enabledPath := filepath.Join(common.RulesEnabled, fmt.Sprintf("%s-special-%s.yml", group.Type, groupUUID))
	if err := common.CreateSymlink(specialPath, enabledPath); err != nil && !os.IsExist(err) {
		log.Printf("Rule activation failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule activation failed"})
		return
	}

	// Reload Prometheus configuration
	if err := common.ReloadPrometheus(); err != nil {
		log.Printf("Prometheus reload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": groupUUID,
			"linked_vm":  vmName,
		},
	})
}

func (a *AlarmAPI) UnlinkRuleFromVM(c *gin.Context) {
	var req struct {
		GroupUUID string `json:"group_uuid" binding:"required"`
		VMUUID    string `json:"vm_uuid" binding:"required"`
		Interface string `json:"interface"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	groupUUID := req.GroupUUID
	vmUUID := req.VMUUID

	group, err := a.operator.GetRulesByGroupUUID(c.Request.Context(), groupUUID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
		return
	} else if err != nil {
		log.Printf("Error retrieving rule group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
		return
	}

	deletedCount, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, vmUUID, req.Interface)
	if err != nil {
		log.Printf("VM unlinking failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to operate vm link db: " + err.Error()})
		return
	}
	if deletedCount == 0 {
		log.Printf("VM unlinking failed: no matching record")
		c.JSON(http.StatusNotFound, gin.H{"error": "vm association not found"})
		return
	}

	// Get updated VM list
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("Failed to get updated VM list: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve VM associations"})
		return
	}

	ruleType := group.Type
	// Build inclusion list for special rules
	vmList := make([]string, 0, len(vmLinks))
	for _, link := range vmLinks {
		instanceid, err := routes.GetDBIndexByInstanceUUID(c, link.VMUUID)
		vmName := "inst-" + strconv.Itoa(instanceid)
		if err != nil {
			log.Printf("VM UUID convert failed uuid=%s error=%v", link.VMUUID, err)
			continue
		}
		if ruleType == common.RuleTypeBW && link.Interface != "" {
			vmName = fmt.Sprintf("%s:%s", vmName, link.Interface)
		}
		vmList = append(vmList, vmName)
	}

	var generalContent, specialContent string
	generalPath, specialPath := common.RulePaths(ruleType, groupUUID)

	if ruleType == common.RuleTypeCPU {
		type ExtendedGroup struct {
			model.RuleGroupV2
			Details []model.CPURuleDetail
		}
		details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
		rules := make([]common.CPURule, 0, len(details))
		for _, d := range details {
			rules = append(rules, common.CPURule{
				Name:         d.Name,
				Duration:     d.Duration,
				Over:         d.Over,
				DownDuration: d.DownDuration,
				DownTo:       d.DownTo,
			})
		}

		generalContent, err = generateCPURuleContentV2(rules, group.Name, groupUUID, vmList, true)
		if err != nil {
			log.Printf("Rule generation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule content generation failed"})
			return
		}

		if len(vmList) > 0 {
			specialContent, err = generateCPURuleContentV2(rules, group.Name, groupUUID, vmList, false)
			if err != nil {
				log.Printf("Special rule generation failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate special rules"})
				return
			}
		}
	} else if ruleType == common.RuleTypeBW {
		type ExtendedGroup struct {
			model.RuleGroupV2
			Details []model.BWRuleDetail
		}
		details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
		rules := make([]common.BWRule, 0, len(details))
		for _, d := range details {
			inEnabled := d.InThreshold >= 0 && d.InDuration >= 0
			outEnabled := d.OutThreshold >= 0 && d.OutDuration >= 0

			rules = append(rules, common.BWRule{
				Name:            d.Name,
				InEnabled:       inEnabled,
				InThreshold:     d.InThreshold,
				InDuration:      d.InDuration,
				InOverType:      d.InOverType,
				InDownTo:        d.InDownTo,
				InDownDuration:  d.InDownDuration,
				OutEnabled:      outEnabled,
				OutThreshold:    d.OutThreshold,
				OutDuration:     d.OutDuration,
				OutOverType:     d.OutOverType,
				OutDownTo:       d.OutDownTo,
				OutDownDuration: d.OutDownDuration,
			})
		}

		generalContent, err = generateBWRuleContent(rules, group.Name, groupUUID, vmList, true, req.Interface)
		if err != nil {
			log.Printf("BW rule generation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "BW rule content generation failed"})
			return
		}

		if len(vmList) > 0 {
			specialContent, err = generateBWRuleContent(rules, group.Name, groupUUID, vmList, false, req.Interface)
			if err != nil {
				log.Printf("Special BW rule generation failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate special BW rules"})
				return
			}
		}
	} else {
		log.Printf("Unsupported rule type: %s", ruleType)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported rule type"})
		return
	}

	if err = common.WriteFile(generalPath, []byte(generalContent), 0640); err != nil {
		log.Printf("General rule update failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "General rule update failed"})
		return
	}
	enabledPath := filepath.Join(common.RulesEnabled, fmt.Sprintf("%s-special-%s.yml", ruleType, groupUUID))
	if len(vmList) > 0 {
		if err := common.WriteFile(specialPath, []byte(specialContent), 0640); err != nil {
			log.Printf("Special rule update failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Special rule update failed"})
			return
		}
	} else {
		if err := common.RemoveFile(specialPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove empty special rule file: %v", err)
		}
		if err := common.RemoveFile(enabledPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove enabled rule link: %v", err)
		}
	}

	// Reload Prometheus configuration
	if err := common.ReloadPrometheus(); err != nil {
		log.Printf("Config reload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"unlinked_group": groupUUID,
			"unlinked_vm":    vmUUID,
			"remaining_vms":  vmList,
		},
	})
}

func (a *AlarmAPI) CreateCPURule(c *gin.Context) {
	var req struct {
		Name  string           `json:"name" binding:"required"`
		Owner string           `json:"owner" binding:"required"`
		Rules []common.CPURule `json:"rules" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := &model.RuleGroupV2{
		Name:    req.Name,
		Type:    common.RuleTypeCPU,
		Owner:   req.Owner,
		Enabled: true,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": "operator failed: " + err.Error()},
		)
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
	groupUUID := group.UUID
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"call get link vm error": err.Error()})
		return
	}
	var excludeVMs []string
	if len(vmLinks) > 0 {
		for _, link := range vmLinks {
			vmName, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), link.VMUUID)
			if err != nil {
				log.Printf("VM UUID convert failed uuid=%s error=%v", link.VMUUID, err)
				continue
			}
			excludeVMs = append(excludeVMs, vmName)
		}
	}
	//generalRaw, err := generateCPURuleContent(req.Rules, group.Name, groupUUID, excludeVMs...)
	generalRaw, err := generateCPURuleContentV2(req.Rules, group.Name, groupUUID, excludeVMs, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"generate general rules failed": err.Error()})
		return
	}
	generalPath, _ := common.RulePaths(common.RuleTypeCPU, groupUUID)
	if err := common.WriteFile(generalPath, []byte(generalRaw), 0640); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"write general rules filed failed": err.Error()})
		return
	}
	enabledPath := filepath.Join(common.RulesEnabled, fmt.Sprintf("cpu-general-%s.yml", groupUUID))
	if err := common.CreateSymlink(generalPath, enabledPath); err != nil && !os.IsExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "enable general rules filed failed: " + err.Error()})
		return
	}
	common.ReloadPrometheus()
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": groupUUID,
			"enabled":    true,
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

	queryParams := common.ListRuleGroupsParams{
		RuleType: common.RuleTypeCPU,
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
	if _, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, common.RuleTypeCPU); err != nil {
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
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	var excludeVMs []string
	if err == nil {
		for _, link := range vmLinks {
			_, err := routes.GetDBIndexByInstanceUUID(c, link.VMUUID)
			if err != nil {
				log.Printf("convert UUID to vm name failed uuid=%s error=%v", link.VMUUID, err)
				continue
			}
			//vmName := "inst-" + strconv.Itoa(instanceid)
			excludeVMs = append(excludeVMs, link.VMUUID)
		}
	}
	//generalPath, specialPath := common.RulePaths(common.RuleTypeCPU, groupUUID)
	if len(excludeVMs) > 0 {
		log.Printf("Cannot delete CPU rule group %s: still linked to VMs: %v", groupUUID, excludeVMs)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "CPU rule group is still bound to active VMs. Please unlink them first.",
			"code":       "RULE_GROUP_LINKED",
			"linked_vms": excludeVMs,
			"uuid":  groupUUID,
		})
		return
	}

	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, common.RuleTypeCPU); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}
	patterns := []string{
		fmt.Sprintf("%s/cpu-general-%s.yml", common.RulesGeneral, groupUUID),
		fmt.Sprintf("%s/cpu-general-%s.yml", common.RulesEnabled, groupUUID),
	}
	deletedpath := []string{}
	for _, pattern := range patterns {
		status, err := common.CheckFileExists(pattern)
		if err != nil {
			log.Printf("CheckFileExists failed for %s: %v", pattern, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check rule file"})
			return
		}
		if status {
			if err := common.RemoveFile(pattern); err != nil {
				log.Printf("[Cleanup] Failed to remove %s: %v", pattern, err)
			}
			deletedpath = append(deletedpath, pattern)
		}
	}
	if err := common.ReloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "reload Prometheus failed",
			"code":  "PROMETHEUS_RELOAD_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": deletedpath,
		},
	})
}

func generateCPURuleContentV2(rules []common.CPURule, groupName string, groupUUID string, vmList []string, isExclude bool) (string, error) {
	var sb strings.Builder

	ruleTypePrefix := "general"
	if !isExclude {
		ruleTypePrefix = "special"
	}

	fullGroupName := fmt.Sprintf("cpu_%s_%s", ruleTypePrefix, groupUUID)
	sb.WriteString(fmt.Sprintf("groups:\n- name: %s\n  rules:", fullGroupName))

	filter := ""
	if len(vmList) > 0 && len(vmList[0]) > 0 {
		if isExclude {
			filter = fmt.Sprintf(`{domain!~"%s"}`, strings.Join(vmList, "|"))
		} else {
			filter = fmt.Sprintf(`{domain=~"%s"}`, strings.Join(vmList, "|"))
		}
	}

	log.Printf("[generateCPURuleContentV2] filter: %s (isExclude: %v)\n", filter, isExclude)

	for i, rule := range rules {
		if rule.Over <= 0 || rule.DownTo <= 0 {
			return "", fmt.Errorf("rule #%d verify failed：must be greater than 0", i)
		}
		if rule.Over <= rule.DownTo {
			return "", fmt.Errorf("rule #%d verify failed：trigger (%d%%) must be greater than(%d%%)",
				i, rule.Over, rule.DownTo)
		}

		sb.WriteString(fmt.Sprintf(`
  - alert: HighCPUUsage_%s_%d
    expr: |-
      (sum by (domain) (rate(libvirt_domain_info_cpu_time_seconds_total%s[1m]))
        / on (domain) group_left() libvirt_domain_info_virtual_cpus) * 100 > %d
    for: %ds
    labels:
      severity: warning
      rule_group: "%s" 
      alert_type: cpu
    annotations:
      summary: "High VM Usage ({{ $value }})"
      description: "VM {{ $labels.domain }} has high CPU usage for %d seconds"
  - alert: CPUUsageRecovered_%s_%d
    expr: |-
      (sum by (domain) (rate(libvirt_domain_info_cpu_time_seconds_total%s[1m]))
        / on (domain) group_left() libvirt_domain_info_virtual_cpus) * 100 < %d
    for: %ds
    labels:
      severity: info
      rule_group: "%s"
      alert_type: cpu
    annotations:
      summary: "VM CPU Usage Recovered ({{ $value }})"
      description: "VM {{ $labels.domain }} CPU usage has recovered below threshold for %d seconds"`,
			rule.Name, i, filter,
			rule.Over,
			rule.Duration,
			groupUUID,
			rule.Duration,
			rule.Name, i, filter,
			rule.DownTo,
			rule.DownDuration,
			groupUUID,
			rule.DownDuration))
	}

	return sb.String() + "\n", nil
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
	generalPath, specialPath := common.RulePaths(group.Type, groupUUID)

	// Create symbolic links
	enabledLinks := make([]string, 0, 2)


	// link general rules
	generalLink := filepath.Join(common.RulesEnabled, fmt.Sprintf("%s-general-%s.yml", group.Type, groupUUID))
	if err = common.CreateSymlink(generalPath, generalLink); err != nil && !os.IsExist(err) {
		log.Printf("General rule activation failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate general rules"})
		return
	}
	//link special links
	enabledLinks = append(enabledLinks, generalLink)
	status, err := common.CheckFileExists(specialPath)
	if err != nil {
		log.Printf("CheckFileExists failed for %s: %v", specialPath, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check rule file"})
		return
	}
	if status {
		specialLink := filepath.Join(common.RulesEnabled, fmt.Sprintf("%s-special-%s.yml", group.Type, groupUUID))
			if err = common.CreateSymlink(specialPath, specialLink); err != nil && !os.IsExist(err) {
				log.Printf("Special rule activation failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate special rules"})
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
	if err := common.ReloadPrometheus(); err != nil {
		log.Printf("Config reload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":    groupUUID,
			"enabled_links": enabledLinks,
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
	specialLink := filepath.Join(common.RulesEnabled, fmt.Sprintf("%s-special-%s.yml", group.Type, groupUUID))
	//unlink special rules
	status, err := common.CheckFileExists(specialLink)
	if err != nil {
		log.Printf("CheckFileExists failed for %s: %v", specialLink, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check rule file"})
		return
	}
	if status {
		if err := common.RemoveFile(specialLink); err != nil && !os.IsNotExist(err) {
			log.Printf("specialLink rule deactivation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate general rules"})
			return
		}
		deletedFiles = append(deletedFiles, specialLink)
	}

	generalLink :=  filepath.Join(common.RulesEnabled, fmt.Sprintf("%s-general-%s.yml", group.Type, groupUUID))
	// unlink general rules
	status, err = common.CheckFileExists(generalLink)
	if err != nil {
		log.Printf("CheckFileExists failed for %s: %v", generalLink, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check rule file"})
		return
	}
	if status {
		if err := common.RemoveFile(generalLink); err != nil && !os.IsNotExist(err) {
			log.Printf("generalLink rule deactivation failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate general rules"})
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
	if err := common.ReloadPrometheus(); err != nil {
		log.Printf("Prometheus reload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
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
	targetURL := fmt.Sprintf("http://%s:%d/api/v1/alerts", common.GetPrometheusIP(), common.GetPrometheusPort())
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
								labels["instance_uuid"] = "" // 确保空值
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
	baseURL := fmt.Sprintf("http://%s:%d/api/v1/query_range", common.GetPrometheusIP(), common.GetPrometheusPort())

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
		Status  string `json:"status"`
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
	log.Printf("ProcessAlertWebhook Processing alert: status=%s \n",status)
	for _, alert := range notification.Alerts {
		alert_type := alert.Labels["alert_type"]
		alertName := alert.Labels["alertname"]
		severity := alert.Labels["severity"]

		domain := alert.Labels["domain"]
		rule_group_uuid := alert.Labels["rule_group"]
		log.Printf("ProcessAlertWebhook Processing alert: alert_type=%s alertName=%s severity=%s\n", alert_type, alertName, severity)
		log.Printf("ProcessAlertWebhook Processing alert: domain=%s rule_group_uuid=%s\n", domain, rule_group_uuid)
        description := alert.Annotations["description"]
        summary := alert.Annotations["summary"]
		log.Printf("ProcessAlertWebhook Processing alert: summary=%s description=%s \n",summary, description)
		target_device := ""
		if alert_type == "bw" {
			target_device = alert.Labels["target_device"]
		}
		alertRecord := &common.Alert{
			Name:         alertName,
			RuleGroupUUID: rule_group_uuid,
			Severity:     severity,
			Summary:      summary,
			Description:  description,
			StartsAt:     alert.StartsAt,
			AlertType: alert_type,
			TargetDevice: target_device,
		}
		if status == "firing" {
			if err := a.notifyRealtimeAlert(alertRecord); err != nil {
				log.Printf("Failed to notify realtime alert: %v", err)
			}
		} else {
			log.Printf("ProcessAlertWebhook alert resolved alert: summary=%s alertRecord=%v \n",summary, alertRecord)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "processed",
		"alerts":  len(notification.Alerts),
		"message": "alarm process completed",
	})
}

func (a *AlarmAPI) notifyRealtimeAlert(alert *common.Alert) error {
	log.Printf("notifyRealtimeAlert input: %v", alert)
	return nil
	// notify message to ui
}

// GetActiveRules retrieves active rules from Prometheus
func (a *AlarmAPI) GetActiveRules(c *gin.Context) {
	// Build Prometheus API URL from config
	apiURL := fmt.Sprintf("http://%s:%d/api/v1/rules", common.GetPrometheusIP(), common.GetPrometheusPort())

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

func generateBWRuleContent(rules []common.BWRule, groupName string, groupUUID string, vms []string, isSpecial bool, targetDevice string) (string, error) {
	var sb strings.Builder

	ruleTypePrefix := "general"
	if isSpecial {
		ruleTypePrefix = "special"
	}

	fullGroupName := fmt.Sprintf("bw_%s_%s", ruleTypePrefix, groupUUID)
	sb.WriteString(fmt.Sprintf("groups:\n- name: %s\n  rules:", fullGroupName))

	processedVMs := make([]string, 0, len(vms))
	vmInterfaces := make(map[string]string)

	for _, vm := range vms {
		parts := strings.Split(vm, ":")
		instanceName := parts[0]
		processedVMs = append(processedVMs, instanceName)
		if len(parts) > 1 {
			vmInterfaces[instanceName] = parts[1]
		}
	}

	for i, rule := range rules {
		var inExpr string
		if isSpecial {
			var expressions []string
			for _, vm := range processedVMs {
				iface := targetDevice
				if v, ok := vmInterfaces[vm]; ok && v != "" {
					iface = v
				}
				if iface != "" {
					expressions = append(expressions,
						fmt.Sprintf("rate(libvirt_domain_interface_stats_receive_bytes_total{domain=\"%s\",device=\"%s\"}[1m])", vm, iface))
				}
			}
			if len(expressions) > 0 {
				inExpr = strings.Join(expressions, " + ")
			} else {
				inExpr = "rate(libvirt_domain_interface_stats_receive_bytes_total[1m])"
			}
		} else if len(processedVMs) > 0 {
			var exclusionExprs []string
			for _, vm := range processedVMs {
				iface := targetDevice
				if v, ok := vmInterfaces[vm]; ok && v != "" {
					iface = v
				}
				expr := fmt.Sprintf("rate(libvirt_domain_interface_stats_receive_bytes_total{domain=\"%s\",device!=\"%s\"}[1m])", vm, iface)
				exclusionExprs = append(exclusionExprs, expr)
			}
			inExprBase := fmt.Sprintf("rate(libvirt_domain_interface_stats_receive_bytes_total{domain!~\"%s\"}[1m])", strings.Join(processedVMs, "|"))
			if len(exclusionExprs) > 0 {
				inExpr = fmt.Sprintf("%s + %s", inExprBase, strings.Join(exclusionExprs, " + "))
			} else {
				inExpr = inExprBase
			}
		} else {
			inExpr = "rate(libvirt_domain_interface_stats_receive_bytes_total[1m])"
		}

		if rule.InOverType == "percent" {
			inExpr = fmt.Sprintf("(%s) / on (domain) group_left() node_network_speed_bytes * 100 > %d", inExpr, rule.InThreshold)
		} else {
			inExpr = fmt.Sprintf("(%s) > %d", inExpr, rule.InThreshold)
		}

		sb.WriteString(fmt.Sprintf(`
  - alert: HighBWInUsage_%s_%d
    expr: |-
      %s
    for: %ds
    labels:
      severity: warning
      rule_group: "%s"
      alert_type: bw
    annotations:
      summary: "High Network In Usage ({{ $value }})"
      description: "VM {{ $labels.domain }} has high network in usage for %d seconds"`,
			rule.Name, i, inExpr, rule.InDuration, groupUUID, rule.InDuration))

		if rule.InDownTo > 0 {
			downExpr := strings.Replace(inExpr, ">", "<", 1)
			sb.WriteString(fmt.Sprintf(`
  - alert: BWInUsageRecovered_%s_%d
    expr: |-
      %s
    for: %ds
    labels:
      severity: info
      rule_group: "%s"
      alert_type: bw
    annotations:
      summary: "Network In Usage Recovered ({{ $value }})"
      description: "VM {{ $labels.domain }} network in usage has recovered below threshold for %d seconds"`,
				rule.Name, i, downExpr, rule.InDownDuration, groupUUID, rule.InDownDuration))
		}

		var outExpr string
		if isSpecial {
			var expressions []string
			for _, vm := range processedVMs {
				iface := targetDevice
				if v, ok := vmInterfaces[vm]; ok && v != "" {
					iface = v
				}
				if iface != "" {
					expressions = append(expressions,
						fmt.Sprintf("rate(libvirt_domain_interface_stats_transmit_bytes_total{domain=\"%s\",device=\"%s\"}[1m])", vm, iface))
				}
			}
			if len(expressions) > 0 {
				outExpr = strings.Join(expressions, " + ")
			} else {
				outExpr = "rate(libvirt_domain_interface_stats_transmit_bytes_total[1m])"
			}
		} else if len(processedVMs) > 0 {
			var exclusionExprs []string
			for _, vm := range processedVMs {
				iface := targetDevice
				if v, ok := vmInterfaces[vm]; ok && v != "" {
					iface = v
				}
				expr := fmt.Sprintf("rate(libvirt_domain_interface_stats_transmit_bytes_total{domain=\"%s\",device!=\"%s\"}[1m])", vm, iface)
				exclusionExprs = append(exclusionExprs, expr)
			}
			outExprBase := fmt.Sprintf("rate(libvirt_domain_interface_stats_transmit_bytes_total{domain!~\"%s\"}[1m])", strings.Join(processedVMs, "|"))
			if len(exclusionExprs) > 0 {
				outExpr = fmt.Sprintf("%s + %s", outExprBase, strings.Join(exclusionExprs, " + "))
			} else {
				outExpr = outExprBase
			}
		} else {
			outExpr = "rate(libvirt_domain_interface_stats_transmit_bytes_total[1m])"
		}

		if rule.OutOverType == "percent" {
			outExpr = fmt.Sprintf("(%s) / on (domain) group_left() node_network_speed_bytes * 100 > %d", outExpr, rule.OutThreshold)
		} else {
			outExpr = fmt.Sprintf("(%s) > %d", outExpr, rule.OutThreshold)
		}

		sb.WriteString(fmt.Sprintf(`
  - alert: HighBWOutUsage_%s_%d
    expr: |-
      %s
    for: %ds
    labels:
      severity: warning
      rule_group: "%s"
      alert_type: bw
    annotations:
      summary: "High Network Out Usage ({{ $value }})"
      description: "VM {{ $labels.domain }} has high network out usage for %d seconds"`,
			rule.Name, i, outExpr, rule.OutDuration, groupUUID, rule.OutDuration))

		if rule.OutDownTo > 0 {
			downExpr := strings.Replace(outExpr, ">", "<", 1)
			sb.WriteString(fmt.Sprintf(`
  - alert: BWOutUsageRecovered_%s_%d
    expr: |-
      %s
    for: %ds
    labels:
      severity: info
      rule_group: "%s"
      alert_type: bw
    annotations:
      summary: "Network Out Usage Recovered ({{ $value }})"
      description: "VM {{ $labels.domain }} network out usage has recovered below threshold for %d seconds"`,
				rule.Name, i, downExpr, rule.OutDownDuration, groupUUID, rule.OutDownDuration))
		}
	}

	return sb.String() + "\n", nil
}

func (a *AlarmAPI) CreateBWRule(c *gin.Context) {
	var req struct {
		Name         string          `json:"name" binding:"required"`
		Owner        string          `json:"owner" binding:"required"`
		Rules        []common.BWRule `json:"rules" binding:"required,min=1"`
		TargetDevice string          `json:"target_device"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create rule group
	group := &model.RuleGroupV2{
		Name:    req.Name,
		Type:    common.RuleTypeBW,
		Owner:   req.Owner,
		Enabled: true,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": "operator failed: " + err.Error()},
		)
		return
	}

	groupUUID := group.UUID

	// Create rule details
	for _, rule := range req.Rules {
		detail := &model.BWRuleDetail{
			GroupUUID: groupUUID,
			Name:      rule.Name,

			// Inbound parameters - default to -1 (disabled)
			InThreshold:    -1,
			InDuration:     -1,
			InOverType:     "absolute",
			InDownTo:       -1,
			InDownDuration: -1,

			// Outbound parameters - default to -1 (disabled)
			OutThreshold:    -1,
			OutDuration:     -1,
			OutOverType:     "absolute",
			OutDownTo:       -1,
			OutDownDuration: -1,
		}

		// Set inbound parameters if enabled
		if rule.InEnabled {
			detail.InThreshold = rule.InThreshold
			detail.InDuration = rule.InDuration
			detail.InOverType = rule.InOverType
			detail.InDownTo = rule.InDownTo
			detail.InDownDuration = rule.InDownDuration
		}

		// Set outbound parameters if enabled
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

	// Get linked VMs
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"call get link vm error": err.Error()})
		return
	}

	// Process VM list
	var vmList []string
	if len(vmLinks) > 0 {
		for _, link := range vmLinks {
			vmName, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), link.VMUUID)
			if err != nil {
				log.Printf("VM UUID convert failed uuid=%s error=%v", link.VMUUID, err)
				continue
			}
			vmList = append(vmList, vmName)
		}
	}

	// Generate rule content
	generalRaw, err := generateBWRuleContent(req.Rules, group.Name, groupUUID, vmList, false, req.TargetDevice)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"generate general rules failed": err.Error()})
		return
	}

	// Write rule file
	generalPath := fmt.Sprintf("%s/bw-general-%s.yml", common.RulesGeneral, groupUUID)
	if err := common.WriteFile(generalPath, []byte(generalRaw), 0640); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"write general rules filed failed": err.Error()})
		return
	}

	// Create symlink
	enabledPath := filepath.Join(common.RulesEnabled, fmt.Sprintf("bw-general-%s.yml", groupUUID))
	if err := common.CreateSymlink(generalPath, enabledPath); err != nil && !os.IsExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "enable general rules filed failed: " + err.Error()})
		return
	}

	// Reload Prometheus
	common.ReloadPrometheus()

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": groupUUID,
			"enabled":    true,
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

	queryParams := common.ListRuleGroupsParams{
		RuleType: common.RuleTypeBW,
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
	if _, err := a.operator.GetBWRulesByGroupUUID(c.Request.Context(), groupUUID, common.RuleTypeBW); err != nil {
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
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	var excludeVMs []string
	if err == nil {
		for _, link := range vmLinks {
			_, err := routes.GetDBIndexByInstanceUUID(c, link.VMUUID)
			if err != nil {
				log.Printf("convert UUID to vm name failed uuid=%s error=%v", link.VMUUID, err)
				continue
			}
			//vmName := "inst-" + strconv.Itoa(instanceid)
			excludeVMs = append(excludeVMs, link.VMUUID)
		}
	}
	log.Printf("existing excludeVMs: %v", excludeVMs)
	if len(excludeVMs) > 0 {
		log.Printf("Cannot delete BW rule group %s: still linked to VMs: %v", groupUUID, excludeVMs)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "BW rule group is still bound to active VMs. Please unlink them first.",
			"code":       "RULE_GROUP_LINKED",
			"linked_vms": excludeVMs,
			"uuid":       groupUUID,
		})
		return
	}

	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, common.RuleTypeBW); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}
	patterns := []string{
		fmt.Sprintf("%s/bw-general-%s.yml", common.RulesGeneral, groupUUID),
		fmt.Sprintf("%s/bw-general-%s.yml", common.RulesEnabled, groupUUID),
	}

	deletedpath := []string{}
	for _, pattern := range patterns {
		status, err := common.CheckFileExists(pattern)
		if err != nil {
			log.Printf("CheckFileExists failed for %s: %v", pattern, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check rule file"})
			return
		}
		if status {
			if err := common.RemoveFile(pattern); err != nil {
				log.Printf("[Cleanup] Failed to remove %s: %v", pattern, err)
			}
			deletedpath = append(deletedpath, pattern)
		}
	}

	if err := common.ReloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "reload Prometheus failed",
			"code":  "PROMETHEUS_RELOAD_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": deletedpath,
		},
	})
}
