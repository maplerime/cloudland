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

			// Check if the same domain+group combination already exists with the same target_device
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
		// If targetDevice is provided (optional variadic parameter), delete by triple match;
		// Otherwise, use the original "delete all by groupUUID" logic (unchanged).
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

			// No device filter: maintain original logic, delete all by group
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

			// Device filter: require all three conditions to match
			// 1) Belongs to the group
			// 2) instance_id in vmUUIDs
			// 3) target_device in targetDevice
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

			// Keep if triple condition not matched
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

// LinkRuleToVMWithType returns a closure that handles VM linking based on rule category
// This supports incremental addition of VMs to rules (alarm or adjust)
func (a *AlarmAPI) LinkRuleToVMWithType(ruleCategory string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			GroupUUID string `json:"group_uuid,omitempty"`
			RuleID    string `json:"rule_id,omitempty"`
			VMLinks   []struct {
				VMUUID    string `json:"vm_uuid" binding:"required"`
				Interface string `json:"interface"`
			} `json:"vm_links" binding:"required,min=1"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// Validate that either group_uuid or rule_id must be provided
		if req.GroupUUID == "" && req.RuleID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "either group_uuid or rule_id must be provided"})
			return
		}

		var group *model.RuleGroupV2
		var err error

		// Prioritize rule_id, fallback to group_uuid if not provided
		if req.RuleID != "" {
			group, err = a.operator.GetRulesByRuleID(c.Request.Context(), req.RuleID)
		} else {
			group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
		}

		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
			return
		} else if err != nil {
			log.Printf("Error retrieving rule group: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
			return
		}

		groupUUID := group.UUID
		if !group.Enabled {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "Rule group is not enabled",
				"code":       "RULE_GROUP_DISABLED",
				"group_uuid": groupUUID,
			})
			return
		}

		// Incremental DB insertion with deduplication
		type VMInterfacePair struct {
			VMUUID    string
			Interface string
		}
		var newlyAdded []VMInterfacePair

		for _, link := range req.VMLinks {
			exists := a.operator.CheckVMLinkExists(
				c.Request.Context(),
				groupUUID,
				link.VMUUID,
				link.Interface,
			)

			if !exists {
				err := a.operator.CreateVMLink(
					c.Request.Context(),
					groupUUID,
					link.VMUUID,
					link.Interface,
				)
				if err == nil {
					newlyAdded = append(newlyAdded, VMInterfacePair{
						VMUUID:    link.VMUUID,
						Interface: link.Interface,
					})
				} else {
					log.Printf("Failed to create VM link: %v", err)
				}
			}
		}

		// Construct alarm type based on rule category
		alarmType := ruleCategory + "-" + group.Type

		// Incremental file update (only add newly added VMs)
		// Group by interface for batch processing
		interfaceGroups := make(map[string][]string)
		for _, pair := range newlyAdded {
			interfaceGroups[pair.Interface] = append(interfaceGroups[pair.Interface], pair.VMUUID)
		}

		for iface, vmUUIDs := range interfaceGroups {
			_ = a.updateMatchedVMsJSON(
				c.Request.Context(),
				vmUUIDs,
				groupUUID,
				"add",
				alarmType,
				iface,
			)
		}

		// Query final linked VMs for response
		vmLinks, _ := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
		var linkedVMsList []map[string]string
		for _, link := range vmLinks {
			linkedVMsList = append(linkedVMsList, map[string]string{
				"vm_uuid":   link.VMUUID,
				"interface": link.Interface,
			})
		}

		// Force reload Prometheus configuration
		if err := routes.ReloadPrometheus(); err != nil {
			log.Printf("Warning: Failed to reload Prometheus: %v", err)
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data": gin.H{
				"rule_category":    ruleCategory,
				"group_uuid":       groupUUID,
				"rule_id":          group.RuleID,
				"added_count":      len(newlyAdded),
				"total_linked_vms": linkedVMsList,
			},
		})
	}
}

// UnlinkRuleFromVMWithType returns a closure that handles VM unlinking based on rule category
func (a *AlarmAPI) UnlinkRuleFromVMWithType(ruleCategory string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			GroupUUID string `json:"group_uuid,omitempty"`
			RuleID    string `json:"rule_id,omitempty"`
			VMLinks   []struct {
				VMUUID    string `json:"vm_uuid" binding:"required"`
				Interface string `json:"interface"`
			} `json:"vm_links" binding:"required,min=1"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// Validate that either group_uuid or rule_id must be provided
		if req.GroupUUID == "" && req.RuleID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "either group_uuid or rule_id must be provided"})
			return
		}

		var group *model.RuleGroupV2
		var err error

		// Prioritize rule_id, fallback to group_uuid if not provided
		if req.RuleID != "" {
			group, err = a.operator.GetRulesByRuleID(c.Request.Context(), req.RuleID)
		} else {
			group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
		}

		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
			return
		} else if err != nil {
			log.Printf("Error retrieving rule group: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
			return
		}

		groupUUID := group.UUID

		// Check if this is batch delete (all interfaces are empty) or specific delete
		isBatchDelete := len(req.VMLinks) > 0 && req.VMLinks[0].Interface == ""

		type DeletedLink struct {
			VMUUID    string
			Interface string
		}
		var successfulDeletes []DeletedLink
		var failedVMs []map[string]interface{}
		totalDeleted := int64(0)

		if isBatchDelete {
			// Batch delete: delete all interfaces for each VM
			for _, link := range req.VMLinks {
				deletedCount, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, link.VMUUID, "")
				if err != nil {
					log.Printf("VM unlinking failed for %s: %v", link.VMUUID, err)
					failedVMs = append(failedVMs, map[string]interface{}{
						"vm_uuid": link.VMUUID,
						"error":   "failed to operate vm link db: " + err.Error(),
					})
					continue
				}

				if deletedCount == 0 {
					failedVMs = append(failedVMs, map[string]interface{}{
						"vm_uuid": link.VMUUID,
						"error":   "VM link not found",
					})
					continue
				}

				successfulDeletes = append(successfulDeletes, DeletedLink{
					VMUUID:    link.VMUUID,
					Interface: "",
				})
				totalDeleted += deletedCount
			}
		} else {
			// Specific delete: delete specific (vm_uuid, interface) pairs
			for _, link := range req.VMLinks {
				deletedCount, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, link.VMUUID, link.Interface)
				if err != nil {
					log.Printf("VM unlinking failed for %s (interface: %s): %v", link.VMUUID, link.Interface, err)
					failedVMs = append(failedVMs, map[string]interface{}{
						"vm_uuid":   link.VMUUID,
						"interface": link.Interface,
						"error":     "failed to operate vm link db: " + err.Error(),
					})
					continue
				}

				if deletedCount == 0 {
					failedVMs = append(failedVMs, map[string]interface{}{
						"vm_uuid":   link.VMUUID,
						"interface": link.Interface,
						"error":     "VM link not found",
					})
					continue
				}

				successfulDeletes = append(successfulDeletes, DeletedLink{
					VMUUID:    link.VMUUID,
					Interface: link.Interface,
				})
				totalDeleted += deletedCount
			}
		}

		// If all VMs failed to unlink, return error
		if len(successfulDeletes) == 0 {
			c.JSON(http.StatusNotFound, gin.H{
				"error":      "No VMs were unlinked",
				"group_uuid": groupUUID,
				"failed_vms": failedVMs,
			})
			return
		}

		// Construct alarm type based on rule category
		alarmType := ruleCategory + "-" + group.Type

		// Remove successfully unlinked VMs from matched_vms.json
		// Group by interface for batch processing
		interfaceGroups := make(map[string][]string)
		for _, deleted := range successfulDeletes {
			interfaceGroups[deleted.Interface] = append(interfaceGroups[deleted.Interface], deleted.VMUUID)
		}

		for iface, vmUUIDs := range interfaceGroups {
			_ = a.updateMatchedVMsJSON(c.Request.Context(), vmUUIDs, groupUUID, "remove", alarmType, iface)
		}

		// Query remaining linked VMs for response
		vmLinks, _ := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
		var remainingVMsList []map[string]string
		for _, link := range vmLinks {
			remainingVMsList = append(remainingVMsList, map[string]string{
				"vm_uuid":   link.VMUUID,
				"interface": link.Interface,
			})
		}

		// Force reload Prometheus configuration
		if err := routes.ReloadPrometheus(); err != nil {
			log.Printf("Warning: Failed to reload Prometheus: %v", err)
		}

		// Build response data
		var unlinkedList []map[string]string
		for _, deleted := range successfulDeletes {
			unlinkedList = append(unlinkedList, map[string]string{
				"vm_uuid":   deleted.VMUUID,
				"interface": deleted.Interface,
			})
		}

		responseData := gin.H{
			"rule_category":   ruleCategory,
			"group_uuid":      groupUUID,
			"rule_id":         group.RuleID,
			"unlinked_vms":    unlinkedList,
			"unlinked_count":  len(successfulDeletes),
			"remaining_vms":   remainingVMsList,
			"total_deleted":   totalDeleted,
			"is_batch_delete": isBatchDelete,
		}

		// Add failed VMs info if any
		if len(failedVMs) > 0 {
			responseData["failed_vms"] = failedVMs
			responseData["failed_count"] = len(failedVMs)
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   responseData,
		})
	}
}

func (a *AlarmAPI) CreateCPURule(c *gin.Context) {
	var req struct {
		RuleID          string           `json:"rule_id"`
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
		RuleID:          req.RuleID,
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
			Limit:        rule.Limit,
			Rule:         rule.Rule,
			Duration:     rule.Duration,
			Over:         rule.Over,
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
	// Validate: only one rule can be created at a time
	if len(req.Rules) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only one rule can be created at a time."})
		return
	}

	// Process only the first rule
	rule := req.Rules[0]
	// Convert gt/lt to >/< symbols for template
	var ruleOperator string
	switch rule.Rule {
	case "gt":
		ruleOperator = ">"
	case "lt":
		ruleOperator = "<"
	default:
		ruleOperator = ">"
	}

	ruleData := map[string]interface{}{
		"owner":            req.Owner,
		"rule_group":       group.UUID,
		"name":             rule.Name,
		"rule_operator":    ruleOperator,
		"limit_value":      rule.Limit,
		"duration_minutes": rule.Duration,
		"rule_id":          fmt.Sprintf("alarm-cpu-%s-%s", req.Owner, group.UUID),
		"global_rule_id":   group.RuleID,
		"region_id":        req.RegionID,
		"level":            req.Level,
		"over":             rule.Over,
		"duration":         rule.Duration,
		"down_to":          rule.DownTo,
		"down_duration":    rule.DownDuration,
	}

	templateFile := "VM-cpu-rule.yml.j2"
	outputFile := fmt.Sprintf("cpu-%s-%s.yml", req.Owner, group.UUID)
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
			"rule_id":    group.RuleID,
			"enabled":    true,
			"linkedvms":  req.LinkedVMs,
			"region_id":  req.RegionID,
		},
	})
}

func (a *AlarmAPI) CreateMemoryRule(c *gin.Context) {
	var req struct {
		RuleID          string              `json:"rule_id"`
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
		RuleID:          req.RuleID,
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
			Limit:        rule.Limit,
			Rule:         rule.Rule,
			Duration:     rule.Duration,
			Over:         rule.Over,
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
	// Validate: only one rule can be created at a time
	if len(req.Rules) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only one rule can be created at a time."})
		return
	}

	// Process only the first rule
	rule := req.Rules[0]
	// Convert gt/lt to >/< symbols for template
	var ruleOperator string
	switch rule.Rule {
	case "gt":
		ruleOperator = ">"
	case "lt":
		ruleOperator = "<"
	default:
		ruleOperator = ">"
	}

	ruleData := map[string]interface{}{
		"owner":            req.Owner,
		"rule_group":       group.UUID,
		"name":             rule.Name,
		"rule_operator":    ruleOperator,
		"limit_value":      rule.Limit,
		"duration_minutes": rule.Duration,
		"rule_id":          fmt.Sprintf("alarm-memory-%s-%s", req.Owner, group.UUID),
		"global_rule_id":   group.RuleID,
		"region_id":        req.RegionID,
		"level":            req.Level,
		"over":             rule.Over,
		"duration":         rule.Duration,
		"down_to":          rule.DownTo,
		"down_duration":    rule.DownDuration,
	}

	templateFile := "VM-memory-rule.yml.j2"
	outputFile := fmt.Sprintf("memory-%s-%s.yml", req.Owner, group.UUID)
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
			"region_id":  req.RegionID,
			"rule_id":    group.RuleID,
		},
	})
}

func (a *AlarmAPI) GetCPURules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")
	ruleID := c.Query("rule_id")
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
	if ruleID != "" {
		queryParams.RuleID = ruleID
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
				"name":     d.Name,
				"rule":     d.Rule,
				"limit":    d.Limit,
				"duration": d.Duration,
			})
		}

		responseData = append(responseData, gin.H{
			"rule_id":   group.RuleID,
			"name":      group.Name,
			"owner":     group.Owner,
			"rules":     ruleDetails,
			"linkedvms": linkedVMs,
			"region_id": group.RegionID,
			"level":     group.Level,
			"enable":    group.Enabled,
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
	ruleID := c.Query("rule_id")
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
		RuleID:   ruleID,
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
				"name":     d.Name,
				"rule":     d.Rule,
				"limit":    d.Limit,
				"duration": d.Duration,
			})
		}

		responseData = append(responseData, gin.H{
			"rule_id":   group.RuleID,
			"name":      group.Name,
			"owner":     group.Owner,
			"rules":     ruleDetails,
			"linkedvms": linkedVMs,
			"region_id": group.RegionID,
			"level":     group.Level,
			"enable":    group.Enabled,
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
	identifier := c.Param("uuid")
	if identifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty identifier error."})
		return
	}

	// Adaptive query: try RuleID first, fallback to UUID
	var group *model.RuleGroupV2
	params := routes.ListRuleGroupsParams{
		RuleID:   identifier,
		RuleType: routes.RuleTypeCPU,
		PageSize: 1,
	}
	groups, _, err := a.operator.ListRuleGroups(c.Request.Context(), params)
	if err == nil && len(groups) > 0 {
		// RuleID query succeeded
		group = &groups[0]
	} else {
		// RuleID query failed, try UUID query
		group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), identifier)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":      "Rule not found: The specified rule does not exist",
				"code":       "NOT_FOUND",
				"identifier": identifier,
			})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      "Failed to retrieve rule information",
				"code":       "INTERNAL_ERROR",
				"identifier": identifier,
			})
			return
		}
	}

	// Use actual GroupUUID for subsequent operations
	groupUUID := group.UUID

	// Verify rule type is correct
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

	// Delete symlink and rule file (paths consistent with creation)
	fileName := fmt.Sprintf("cpu-%s-%s.yml", owner, groupUUID)
	linkPath := filepath.Join(routes.RulesEnabled, fileName)
	rulePath := filepath.Join(routes.RulesGeneral, fileName) // All rules now stored in general_rules

	// Track deleted file paths
	deletedFiles := []string{}

	// Delete symlink
	if err := routes.RemoveFile(linkPath); err == nil {
		deletedFiles = append(deletedFiles, linkPath)
	}

	// Delete rule file
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

	// Add mapping file to deletion list if exists
	if mappingFile != "" {
		deletedFiles = append(deletedFiles, mappingFile)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid":    group.UUID,
			"rule_id":       group.RuleID,
			"deleted_files": deletedFiles,
			"linked_vms":    linkedVMs,
		},
	})
}

func (a *AlarmAPI) DeleteMemoryRule(c *gin.Context) {
	identifier := c.Param("uuid") // 可以是RuleID或UUID
	if identifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty identifier error."})
		return
	}

	// 自适应查询：先尝试RuleID，再尝试UUID
	var group *model.RuleGroupV2
	params := routes.ListRuleGroupsParams{
		RuleID:   identifier,
		RuleType: routes.RuleTypeMemory,
		PageSize: 1,
	}
	groups, _, err := a.operator.ListRuleGroups(c.Request.Context(), params)
	if err == nil && len(groups) > 0 {
		// RuleID query succeeded
		group = &groups[0]
	} else {
		// RuleID query failed, try UUID query
		group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), identifier)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":      "Rule not found: The specified rule does not exist",
				"code":       "NOT_FOUND",
				"identifier": identifier,
			})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      "Failed to retrieve rule information",
				"code":       "INTERNAL_ERROR",
				"identifier": identifier,
			})
			return
		}
	}

	// Use actual GroupUUID for subsequent operations
	groupUUID := group.UUID

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
			"group_uuid":    group.UUID,
			"rule_id":       group.RuleID,
			"deleted_files": deletedFiles,
			"linked_vms":    linkedVMs,
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
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    time.Time         `json:"startsAt"`
			EndsAt      time.Time         `json:"endsAt"`
		} `json:"alerts"`
	}
	body, _ := io.ReadAll(c.Request.Body)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	if err := c.ShouldBindJSON(&notification); err != nil {
		log.Printf("Alert webhook parsing failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid warning msg format"})
		return
	}

	status := notification.Status
	log.Printf("Processing %d alert(s) with status: %s", len(notification.Alerts), status)

	for _, alert := range notification.Alerts {
		alert_type := alert.Labels["alert_type"]
		alertName := alert.Labels["alertname"]
		severity := alert.Labels["severity"]
		owner := alert.Labels["owner"]
		rule_group_uuid := alert.Labels["rule_group"]
		global_rule_id := alert.Labels["global_rule_id"]
		region_id := alert.Labels["region_id"]
		instance_id := alert.Labels["instance_id"]
		description := alert.Annotations["description"]
		summary := alert.Annotations["summary"]

		target_device := ""
		if alert_type == "bw" {
			target_device = alert.Labels["target_device"]
		}

		alertRecord := &routes.Alert{
			Name:          alertName,
			Status:        status,
			RuleGroupUUID: rule_group_uuid,
			GlobalRuleID:  global_rule_id,
			Severity:      severity,
			Summary:       summary,
			Description:   description,
			StartsAt:      alert.StartsAt,
			AlertType:     alert_type,
			TargetDevice:  target_device,
			RegionID:      region_id,
			InstanceID:    instance_id,
		}

		log.Printf("Alert %s: %s (type=%s, severity=%s, owner=%s, rule_id=%s)",
			status, alertName, alert_type, severity, owner, global_rule_id)

		// Notify realtime alert system
		if err := a.notifyRealtimeAlert(alertRecord); err != nil {
			log.Printf("Failed to notify realtime alert %s: %v", alertName, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "processed",
		"alerts":  len(notification.Alerts),
		"message": "alarm process completed",
	})
}

// notifyRealtimeAlert sends alert notifications to configured remote services
func (a *AlarmAPI) notifyRealtimeAlert(alert *routes.Alert) error {
	status := alert.Status

	var endsAt time.Time
	var summary, description string

	if status == "resolved" {
		endsAt = time.Now()
		summary = fmt.Sprintf("RESOLVED: %s", alert.Summary)
		description = fmt.Sprintf("Alert resolved: %s", alert.Description)
	} else {
		endsAt = alert.EndsAt
		summary = alert.Summary
		description = alert.Description
	}

	// Construct notification parameters
	notifyParams := routes.NotifyParams{
		Alerts: []struct {
			State       string            `json:"state"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    time.Time         `json:"startsAt"`
			EndsAt      time.Time         `json:"endsAt"`
		}{
			{
				State: status,
				Labels: map[string]string{
					"alert_type":    alert.AlertType,
					"alertname":     alert.Name,
					"rule_id":       alert.GlobalRuleID,
					"region_id":     alert.RegionID,
					"instance_id":   alert.InstanceID,
					"severity":      alert.Severity,
					"target_device": alert.TargetDevice,
				},
				Annotations: map[string]string{
					"description": description,
					"summary":     summary,
				},
				StartsAt: alert.StartsAt,
				EndsAt:   endsAt,
			},
		},
	}

	// Send notification to all configured services
	if err := a.operator.SendNotificationToAllServices(context.Background(), notifyParams); err != nil {
		log.Printf("Failed to send %s notification for alert %s: %v", status, alert.Name, err)
		return err
	}

	log.Printf("Notification sent successfully for alert %s (%s)", alert.Name, status)
	return nil
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
		Name     string `json:"name" binding:"required"`
		Owner    string `json:"owner" binding:"required"`
		Enable   bool   `json:"enable"`
		RegionID string `json:"region_id" binding:"required"`
		RuleID   string `json:"rule_id" binding:"required"`
		Level    string `json:"level"` // critical/warning/info
		Rules    []struct {
			Direction string `json:"direction" binding:"required,oneof=in out"`
			Name      string `json:"name"`
			Limit     int    `json:"limit" binding:"required,min=1"` // Mbps
			Duration  int    `json:"duration" binding:"required,min=1"`
		} `json:"rules" binding:"required,min=1"`
		LinkedVMs []struct {
			InstanceID   string `json:"instance_id" binding:"required"`
			TargetDevice string `json:"target_device" binding:"required"`
		} `json:"linkedvms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := &model.RuleGroupV2{
		RuleID:   req.RuleID,
		Name:     req.Name,
		Type:     routes.RuleTypeBW,
		Owner:    req.Owner,
		Enabled:  req.Enable,
		RegionID: req.RegionID,
		Level:    req.Level,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operator failed: " + err.Error()})
		return
	}
	for _, rule := range req.Rules {
		detail := &model.BWRuleDetail{
			GroupUUID: group.UUID,
			Name:      rule.Name,
			Direction: rule.Direction,
			Limit:     rule.Limit,
			Duration:  rule.Duration,
			// Set legacy fields to -1 for backward compatibility
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
		if err := a.operator.CreateBWRuleDetail(c.Request.Context(), detail); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
			return
		}
	}
	if len(req.LinkedVMs) > 0 {
		// Convert new format to existing format for VM linking
		var vmUUIDs []string
		for _, vm := range req.LinkedVMs {
			vmUUIDs = append(vmUUIDs, vm.InstanceID) // InstanceID 映射到 VMUUID
		}

		_, _ = a.operator.DeleteVMLink(c.Request.Context(), group.UUID, "", "")

		// Link VMs with target_device (Interface field)
		for _, vm := range req.LinkedVMs {
			_ = a.operator.BatchLinkVMs(c.Request.Context(), group.UUID, []string{vm.InstanceID}, vm.TargetDevice)
		}

		// Update matched VMs JSON for each VM with its target_device
		for _, vm := range req.LinkedVMs {
			_ = a.updateMatchedVMsJSON(c.Request.Context(), []string{vm.InstanceID}, group.UUID, "add", "alarm-bw", vm.TargetDevice)
		}
	}
	// Render templates for each rule direction
	for _, rule := range req.Rules {
		data := map[string]interface{}{
			"owner":          req.Owner,
			"rule_group":     group.UUID,
			"global_rule_id": req.RuleID,
			"region_id":      req.RegionID,
			"level":          req.Level,
		}

		var templateFile, outputFile string

		if rule.Direction == "in" {
			data["rule_id"] = fmt.Sprintf("alarm-bw-in-%s-%s", req.Owner, group.UUID)
			data["in_threshold"] = rule.Limit
			data["in_duration"] = rule.Duration
			templateFile = "VM-in-bw-rule.yml.j2"
			outputFile = fmt.Sprintf("bw-in-%s-%s.yml", req.Owner, group.UUID)
		} else if rule.Direction == "out" {
			data["rule_id"] = fmt.Sprintf("alarm-bw-out-%s-%s", req.Owner, group.UUID)
			data["out_threshold"] = rule.Limit
			data["out_duration"] = rule.Duration
			templateFile = "VM-out-bw-rule.yml.j2"
			outputFile = fmt.Sprintf("bw-out-%s-%s.yml", req.Owner, group.UUID)
		}

		if err := routes.ProcessTemplate(templateFile, outputFile, data); err != nil {
			log.Printf("Failed to render %s-bw rule template: %v", rule.Direction, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to render %s-bw rule template", rule.Direction)})
			return
		}
	}
	routes.ReloadPrometheus()
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"group_uuid": group.UUID,
			"rule_id":    req.RuleID,
			"enabled":    req.Enable,
			"linkedvms":  req.LinkedVMs,
		},
	})
}

func (a *AlarmAPI) GetBWRules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")
	ruleID := c.Query("rule_id")
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
		RuleID:   ruleID,
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
		linkedVMs := make([]gin.H, 0)
		vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), group.UUID)
		if err == nil {
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, gin.H{
					"instance_id":   link.VMUUID,
					"target_device": link.Interface,
				})
			}
		}

		ruleDetails := make([]gin.H, 0, len(details))
		for _, d := range details {
			ruleDetails = append(ruleDetails, gin.H{
				"direction": d.Direction,
				"name":      d.Name,
				"limit":     d.Limit,
				"duration":  d.Duration,
			})
		}

		responseData = append(responseData, gin.H{
			"name":      group.Name,
			"owner":     group.Owner,
			"enable":    group.Enabled,
			"region_id": group.RegionID,
			"rule_id":   group.RuleID,
			"level":     group.Level,
			"rules":     ruleDetails,
			"linkedvms": linkedVMs,
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
	identifier := c.Param("uuid")
	if identifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty identifier error."})
		return
	}

	// Adaptive query: try RuleID first, fallback to UUID
	var group *model.RuleGroupV2
	params := routes.ListRuleGroupsParams{
		RuleID:   identifier,
		RuleType: routes.RuleTypeBW,
		PageSize: 1,
	}
	groups, _, err := a.operator.ListRuleGroups(c.Request.Context(), params)
	if err == nil && len(groups) > 0 {
		// RuleID query succeeded
		group = &groups[0]
	} else {
		// RuleID query failed, try UUID query
		group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), identifier)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":      "Rule not found: The specified rule does not exist",
				"code":       "NOT_FOUND",
				"identifier": identifier,
			})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      "Failed to retrieve rule information",
				"code":       "INTERNAL_ERROR",
				"identifier": identifier,
			})
			return
		}
	}

	// Use actual GroupUUID for subsequent operations
	groupUUID := group.UUID

	// Verify rule type is correct
	if group.Type != routes.RuleTypeBW {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "Rule type mismatch: Expected BW rule but found " + group.Type,
			"code":       "INVALID_RULE_TYPE",
			"identifier": identifier,
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

	// Get BW rule details to determine which files to delete
	details, err := a.operator.GetBWRuleDetails(c.Request.Context(), groupUUID)
	if err != nil {
		log.Printf("[BW-WARNING] Failed to get rule details for file cleanup: %v", err)
		details = []model.BWRuleDetail{}
	}

	// Delete symlink and rule file (paths consistent with creation)
	// Track deleted file paths
	deletedFiles := []string{}

	// Only delete files for directions that actually exist in the rule
	for _, detail := range details {
		var filename string
		switch detail.Direction {
		case "in":
			filename = fmt.Sprintf("bw-in-%s-%s.yml", owner, groupUUID)
		case "out":
			filename = fmt.Sprintf("bw-out-%s-%s.yml", owner, groupUUID)
		default:
			log.Printf("[BW-WARNING] Unknown direction: %s", detail.Direction)
			continue
		}

		linkPath := filepath.Join(routes.RulesEnabled, filename)
		rulePath := filepath.Join(routes.RulesGeneral, filename) // All rules now stored in general_rules

		// Delete symlink
		if err := routes.RemoveFile(linkPath); err == nil {
			deletedFiles = append(deletedFiles, linkPath)
		}
		// Delete rule file
		if err := routes.RemoveFile(rulePath); err == nil {
			deletedFiles = append(deletedFiles, rulePath)
		}
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
			"group_uuid":    group.UUID,
			"rule_id":       group.RuleID,
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
// processRuleMappings processes rule groups and generates VM mappings
func (a *AlarmAPI) processRuleMappings(ctx context.Context, groups interface{}, ruleType string, needsInterface bool) []map[string]interface{} {
	var mappings []map[string]interface{}
	var groupList []struct{ UUID string }

	// Convert groups to common format
	switch v := groups.(type) {
	case []model.RuleGroupV2:
		for _, g := range v {
			groupList = append(groupList, struct{ UUID string }{UUID: g.UUID})
		}
	case []model.AdjustRuleGroup:
		for _, g := range v {
			groupList = append(groupList, struct{ UUID string }{UUID: g.UUID})
		}
	}

	for _, group := range groupList {
		vmLinks, err := a.operator.GetLinkedVMs(ctx, group.UUID)
		if err != nil {
			log.Printf("Failed to get linked VMs for %s group %s: %v", ruleType, group.UUID, err)
			continue
		}

		for _, link := range vmLinks {
			domain, err := routes.GetDomainByInstanceUUID(ctx, link.VMUUID)
			if err != nil {
				log.Printf("Failed to get domain for instance %s: %v", link.VMUUID, err)
				continue
			}

			labels := map[string]interface{}{
				"domain":      domain,
				"rule_id":     fmt.Sprintf("%s-%s-%s", ruleType, domain, group.UUID),
				"instance_id": link.VMUUID,
			}
			if needsInterface {
				labels["target_device"] = link.Interface
			}

			mappings = append(mappings, map[string]interface{}{
				"targets": []string{"localhost:9090"},
				"labels":  labels,
			})
		}
	}
	return mappings
}

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

	// Define all rule types to process
	type ruleConfig struct {
		name       string
		ruleType   interface{}
		isAdjust   bool
		needsIface bool
	}

	configs := []ruleConfig{
		{"alarm-cpu", routes.RuleTypeCPU, false, false},
		{"alarm-memory", routes.RuleTypeMemory, false, false},
		{"alarm-bw", routes.RuleTypeBW, false, true},
		{"adjust-cpu", model.RuleTypeAdjustCPU, true, false},
		{"adjust-bw", model.RuleTypeAdjustInBW, true, true},
		{"adjust-bw", model.RuleTypeAdjustOutBW, true, true},
	}

	var allMappings []map[string]interface{}
	stats := make(map[string]int)
	adjustOperator := &routes.AdjustOperator{}

	// Process each rule type
	for _, cfg := range configs {
		var groups interface{}
		var count int

		if cfg.isAdjust {
			g, _, err := adjustOperator.ListAdjustRuleGroups(ctx, routes.ListAdjustRuleGroupsParams{
				RuleType: cfg.ruleType.(string),
				Page:     1,
				PageSize: 1000,
			})
			if err != nil {
				log.Printf("Failed to get %s rule groups: %v", cfg.name, err)
				continue
			}
			groups = g
			count = len(g)
		} else {
			g, _, err := a.operator.ListRuleGroups(ctx, routes.ListRuleGroupsParams{
				RuleType: cfg.ruleType.(string),
				Page:     1,
				PageSize: 1000,
			})
			if err != nil {
				log.Printf("Failed to get %s rule groups: %v", cfg.name, err)
				continue
			}
			groups = g
			count = len(g)
		}

		mappings := a.processRuleMappings(ctx, groups, cfg.name, cfg.needsIface)
		allMappings = append(allMappings, mappings...)
		stats[cfg.name] += count
	}

	// Write mappings to file
	mappingData, err := json.MarshalIndent(allMappings, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal matched_vms.json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "Failed to marshal mapping data"})
		return
	}

	if err := routes.WriteFile("/etc/prometheus/lists/matched_vms.json", mappingData, 0644); err != nil {
		log.Printf("Failed to write matched_vms.json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "Failed to write mapping file"})
		return
	}

	// Reload Prometheus
	if err := routes.ReloadPrometheus(); err != nil {
		log.Printf("Warning: Failed to reload Prometheus: %v", err)
		c.JSON(http.StatusOK, gin.H{"status": "partial_success", "message": "Mappings synchronized but failed to reload Prometheus", "count": len(allMappings), "stats": stats})
		return
	}

	log.Printf("Successfully synchronized VM mappings: total=%d, stats=%+v", len(allMappings), stats)
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "VM rule mappings synchronized successfully", "count": len(allMappings), "stats": stats})
}

// VMAlarmMapping is used for serialization to vm_alarm_mapping.yml
type VMAlarmMapping struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}

// Remote Notify Config API

// CreateRemoteNotifyConfig creates a remote notification configuration
func (a *AlarmAPI) CreateRemoteNotifyConfig(c *gin.Context) {
	var req model.RemoteNotifyConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if req.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	if req.NotifyURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notify_url is required"})
		return
	}

	// Validate configuration type
	validTypes := []string{
		model.RemoteConfigTypeNotify,
		model.RemoteConfigTypeWebhook,
		model.RemoteConfigTypeSync,
		model.RemoteConfigTypeMetrics,
		model.RemoteConfigTypeLog,
	}
	isValidType := false
	for _, validType := range validTypes {
		if req.Type == validType {
			isValidType = true
			break
		}
	}
	if !isValidType {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid type, must be one of: " + strings.Join(validTypes, ", "),
		})
		return
	}

	// Check if name already exists (防止常规重复)
	existing, err := a.operator.GetRemoteNotifyConfigByName(c.Request.Context(), req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing config"})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Config with this name already exists"})
		return
	}

	if err := a.operator.CreateRemoteNotifyConfig(c.Request.Context(), &req); err != nil {
		// 检查是否是唯一约束冲突（处理并发竞态条件）
		if strings.Contains(err.Error(), "duplicate key") ||
			strings.Contains(err.Error(), "unique constraint") ||
			strings.Contains(err.Error(), "UNIQUE constraint failed") {
			c.JSON(http.StatusConflict, gin.H{"error": "Config with this name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create config: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   req,
	})
}

// GetRemoteNotifyConfigs retrieves list of remote notification configurations
func (a *AlarmAPI) GetRemoteNotifyConfigs(c *gin.Context) {
	// Support filtering by type
	configType := c.Query("type")

	var configs []model.RemoteNotifyConfig
	var err error

	if configType != "" {
		// Get configurations by type
		configs, err = a.operator.GetRemoteNotifyConfigsByType(c.Request.Context(), configType)
	} else {
		// Get all configurations
		configs, err = a.operator.GetRemoteNotifyConfigs(c.Request.Context())
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get configs: " + err.Error()})
		return
	}

	response := gin.H{
		"status": "success",
		"data":   configs,
		"count":  len(configs),
	}

	if configType != "" {
		response["filter_type"] = configType
	}

	c.JSON(http.StatusOK, response)
}

// GetRemoteNotifyConfig retrieves a single remote notification configuration
func (a *AlarmAPI) GetRemoteNotifyConfig(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name parameter is required"})
		return
	}

	config, err := a.operator.GetRemoteNotifyConfigByName(c.Request.Context(), name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get config: " + err.Error()})
		return
	}
	if config == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   config,
	})
}

// DeleteRemoteNotifyConfig deletes a remote notification configuration
func (a *AlarmAPI) DeleteRemoteNotifyConfig(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name parameter is required"})
		return
	}

	if err := a.operator.DeleteRemoteNotifyConfig(c.Request.Context(), name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete config: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Config deleted successfully",
	})
}

// ToggleRuleStatus - Toggle rule status (enable/disable) by managing Prometheus symlinks
// ruleType: "alarm" or "adjust"
// action: "enable" or "disable"
func (a *AlarmAPI) ToggleRuleStatus(ruleType, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uuid := c.Param("id")
		if uuid == "" {
			uuid = c.Param("uuid")
		}
		if uuid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ID or UUID is required"})
			return
		}

		// Get group
		db := c.MustGet("db").(*gorm.DB)
		var group *model.RuleGroupV2
		var err error

		if ruleType == "alarm" {
			// Smart recognition: try as rule_id first, then as group_uuid
			if ruleID, parseErr := strconv.ParseInt(uuid, 10, 64); parseErr == nil {
				// Check if it's a CPU rule detail
				var cpuRule model.CPURuleDetail
				if err = db.Where("id = ?", ruleID).First(&cpuRule).Error; err == nil {
					uuid = cpuRule.GroupUUID
				}
			}
		}

		// Find group by UUID
		if err = db.Where("uuid = ?", uuid).First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query rule group"})
			}
			return
		}

		// Determine rule paths and alert path based on rule type
		var rulePaths []string
		var alertPath string

		if ruleType == "alarm" {
			// Alarm rules in node_rules directory
			rulePathPrefix := fmt.Sprintf("%s/rule-%s-%s", routes.RulesNode, group.Owner, group.UUID)
			for _, rType := range []string{routes.RuleTypeCPU, routes.RuleTypeMemory, routes.RuleTypeBW, routes.RuleTypeCompute, routes.RuleTypeControl, routes.RuleTypeAvailable} {
				rulePaths = append(rulePaths, fmt.Sprintf("%s-%s.yml", rulePathPrefix, rType))
			}
			alertPath = fmt.Sprintf("%s/alert-%s-%s.yml", routes.RulesNode, group.Owner, group.UUID)
		} else {
			// Adjust rules in general_rules directory
			rulePathPrefix := fmt.Sprintf("%s/resource-adjustment-rules-%s-%s", routes.RulesGeneral, group.Owner, group.UUID)
			for _, rType := range []string{"cpu", "memory", "disk"} {
				rulePaths = append(rulePaths, fmt.Sprintf("%s-%s.yml", rulePathPrefix, rType))
			}
			alertPath = fmt.Sprintf("%s/resource-adjustment-alerts-%s-%s.yml", routes.RulesGeneral, group.Owner, group.UUID)
		}

		// Execute enable or disable action
		isEnable := (action == "enable")
		var createdLinks []string

		for _, rulePath := range rulePaths {
			ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(rulePath))

			if isEnable {
				if err := routes.CreateSymlink(rulePath, ruleLinkPath); err != nil && !os.IsExist(err) {
					log.Printf("[%s-%s-ERROR] Failed to create rule symlink: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
					for _, link := range createdLinks {
						routes.RemoveSymlink(link)
					}
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create rule symlink"})
					return
				}
				createdLinks = append(createdLinks, ruleLinkPath)
				log.Printf("[%s-%s-INFO] Created rule symlink: %s -> %s", strings.ToUpper(ruleType), strings.ToUpper(action), ruleLinkPath, rulePath)
			} else {
				if err := routes.RemoveSymlink(ruleLinkPath); err != nil {
					log.Printf("[%s-%s-ERROR] Failed to remove rule symlink: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
				} else {
					log.Printf("[%s-%s-INFO] Removed rule symlink: %s", strings.ToUpper(ruleType), strings.ToUpper(action), ruleLinkPath)
				}
			}
		}

		// Handle alert file
		alertLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filepath.Base(alertPath))
		if isEnable {
			if err := routes.CreateSymlink(alertPath, alertLinkPath); err != nil && !os.IsExist(err) {
				log.Printf("[%s-%s-ERROR] Failed to create alert symlink: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
				for _, link := range createdLinks {
					routes.RemoveSymlink(link)
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create alert symlink"})
				return
			}
			log.Printf("[%s-%s-INFO] Created alert symlink: %s -> %s", strings.ToUpper(ruleType), strings.ToUpper(action), alertLinkPath, alertPath)
		} else {
			if err := routes.RemoveSymlink(alertLinkPath); err != nil {
				log.Printf("[%s-%s-ERROR] Failed to remove alert symlink: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
			} else {
				log.Printf("[%s-%s-INFO] Removed alert symlink: %s", strings.ToUpper(ruleType), strings.ToUpper(action), alertLinkPath)
			}
		}

		// Reload Prometheus
		log.Printf("[%s-%s-INFO] Reloading Prometheus configuration", strings.ToUpper(ruleType), strings.ToUpper(action))
		if err := routes.ReloadPrometheus(); err != nil {
			log.Printf("[%s-%s-WARNING] Failed to reload Prometheus: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
		}

		// Update database status
		group.Enabled = isEnable
		if err := db.Save(group).Error; err != nil {
			log.Printf("[%s-%s-ERROR] Failed to update group status: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update rule status"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("%s rule %sd successfully", ruleType, action)})
	}
}
