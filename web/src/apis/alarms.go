package apis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"web/src/model"
	"web/src/routes"

	"context"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

// ============================================================================
// Operator constants for alert rule comparison direction
// Use these symbolic names in API requests instead of raw PromQL symbols
// to avoid shell escaping issues with '>' and '<'.
// ============================================================================
const (
	OperatorGT  = "gt"  // greater than         → >
	OperatorLT  = "lt"  // less than            → <
	OperatorGTE = "gte" // greater than or equal → >=
	OperatorLTE = "lte" // less than or equal   → <=
	OperatorEQ  = "eq"  // equal                → ==
)

// resolveOperator converts a symbolic operator name (gt/lt/gte/lte/eq) to the
// actual PromQL comparison operator string used in templates and stored in DB.
// Defaults to ">" when the input is empty or unrecognized.
func resolveOperator(op string) string {
	switch strings.ToLower(op) {
	case OperatorGT, ">":
		return ">"
	case OperatorLT, "<":
		return "<"
	case OperatorGTE, ">=":
		return ">="
	case OperatorLTE, "<=":
		return "<="
	case OperatorEQ, "==":
		return "=="
	default:
		return ">"
	}
}

var businessGroupNamePattern = regexp.MustCompile(`[^A-Za-z0-9]+`)

func normalizeBusinessGroupName(name string) (string, error) {
	normalized := businessGroupNamePattern.ReplaceAllString(strings.TrimSpace(name), "")
	if normalized == "" {
		err := fmt.Errorf("invalid business group name after normalization: %q", name)
		logger.Errorf("AlarmAPI.normalizeBusinessGroupName: %v", err)
		return "", err
	}
	return normalized, nil
}

type AlarmAPI struct {
	operator         *routes.AlarmOperator
	alarmAdmin       *routes.AlarmAdmin
	n9eOperator      *routes.N9EOperator
	n9eClient        *routes.N9EClient
	anchorManager    *routes.AnchorManager
	templateRenderer *routes.N9ETemplateRenderer
}

var alarmAPI = &AlarmAPI{
	operator:    &routes.AlarmOperator{},
	alarmAdmin:  &routes.AlarmAdmin{},
	n9eOperator: &routes.N9EOperator{},
}

// InitN9EComponents initializes N9E client and anchor manager
func (a *AlarmAPI) InitN9EComponents() {
	if a.n9eClient == nil {
		n9eURL := viper.GetString("n9e.cluster_url")
		n9eUsername := viper.GetString("n9e.api_username")
		n9ePassword := viper.GetString("n9e.api_password")
		n9eUserGroupID := viper.GetInt64("n9e.user_group_id")
		templatePath := viper.GetString("n9e.template_path")
		datasourceName := viper.GetString("n9e.n9e_data_source")
		notifyRuleName := viper.GetString("n9e.n9e_alarm_notify_group_name")

		if n9eURL != "" {
			a.n9eClient = routes.NewN9EClient(n9eURL, n9eUsername, n9ePassword, n9eUserGroupID, templatePath, datasourceName, notifyRuleName)
			log.Printf("N9E client initialized with URL: %s", n9eURL)
		}
	}

	if a.anchorManager == nil {
		vmQueryURL := viper.GetString("victoriametrics.query_url")
		vmImportURL := viper.GetString("victoriametrics.import_url")
		vmDeleteURL := viper.GetString("victoriametrics.delete_url")
		vmTimeout, _ := time.ParseDuration(viper.GetString("victoriametrics.api_timeout"))
		if vmTimeout == 0 {
			vmTimeout = 30 * time.Second
		}

		if vmQueryURL != "" {
			a.anchorManager = routes.NewAnchorManager(vmQueryURL, vmImportURL, vmDeleteURL, vmTimeout)
			log.Printf("Anchor manager initialized with VM query=%s import=%s delete=%s", vmQueryURL, vmImportURL, vmDeleteURL)
		}
	}

	if a.templateRenderer == nil {
		templatePath := viper.GetString("n9e.template_path")
		if templatePath == "" {
			templatePath = "/opt/n9etemplate"
		}
		a.templateRenderer = routes.NewN9ETemplateRenderer(templatePath)
		log.Printf("N9E template renderer initialized with path: %s", templatePath)
	}
}

// getSeverityLevel converts CloudLand level string to N9E severity integer
// Returns: 1=Critical, 2=Warning, 3=Info
func getSeverityLevel(level string) int {
	switch strings.ToLower(level) {
	case "critical":
		return 1
	case "warning":
		return 2
	case "info":
		return 3
	default:
		return 2 // Default to warning
	}
}

// getWebhookURL returns the CloudLand webhook URL for N9E alerts
func getWebhookURL() string {
	// Get from config or use default
	webhookURL := viper.GetString("n9e.webhook_url")
	if webhookURL == "" {
		// Default to CloudLand's alert processing endpoint
		webhookURL = "http://cloudland:5443/api/v1/alerts/process"
	}
	return webhookURL
}

// isValidUUID validates if a string is in UUID format
func isValidUUID(uuid string) bool {
	// UUID regex: 8-4-4-4-12 hex characters
	uuidRegex := `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	matched, _ := regexp.MatchString(uuidRegex, strings.ToLower(uuid))
	return matched
}

// UpdateMatchedVMsJSON updates the matched_vms.json file, supporting one VM matching multiple rule groups
// Parameters:
//   - ctx: context
//   - vmUUIDs: list of VM UUIDs
//   - groupUUID: rule group UUID
//   - operation: operation type, "add" for add/update, "remove" for delete
//   - ruleType: rule type ("cpu" or "bw") for generating typed rule_id
func (a *AlarmAPI) UpdateMatchedVMsJSON(ctx context.Context, vmUUIDs []string, groupUUID, operation, ruleType string, targetDevice ...string) error {
	// Call the public function in routes package
	return routes.UpdateMatchedVMsJSON(ctx, vmUUIDs, groupUUID, operation, ruleType, targetDevice...)
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

		// Resolve group info by category (alarm / adjust)
		var (
			groupUUID string
			ruleID    string
			groupType string
			enabled   bool
			err       error
		)

		if ruleCategory == "adjust" {
			adj := &routes.AdjustOperator{}
			identifier := req.RuleID
			if identifier == "" {
				identifier = req.GroupUUID
			}
			adjGroup, adjErr := adj.GetAdjustRulesByIdentifier(c.Request.Context(), identifier)
			if errors.Is(adjErr, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
				return
			} else if adjErr != nil {
				logger.Errorf("Error retrieving adjust rule group: %v", adjErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
				return
			}
			groupUUID = adjGroup.UUID
			ruleID = adjGroup.RuleID
			groupType = adjGroup.Type
			enabled = adjGroup.Enabled
		} else {
			var alarmGroup *model.RuleGroupV2
			if req.RuleID != "" {
				alarmGroup, err = a.operator.GetRulesByRuleID(c.Request.Context(), req.RuleID)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// optional fallback: try as group uuid
					alarmGroup, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), req.RuleID)
				}
			} else {
				alarmGroup, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
			}

			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
				return
			} else if err != nil {
				logger.Errorf("Error retrieving rule group: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
				return
			}
			groupUUID = alarmGroup.UUID
			ruleID = alarmGroup.RuleID
			groupType = alarmGroup.Type
			enabled = alarmGroup.Enabled
		}

		if !enabled {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "Rule group is not enabled",
				"code":       "RULE_GROUP_DISABLED",
				"group_uuid": groupUUID,
			})
			return
		}

		// Validate instances exist and collect results
		type VMInterfacePair struct {
			VMUUID    string
			Interface string
		}
		var alreadyLinked []string              // VMs that are already linked
		var notFoundInstances []string          // VMs that don't exist in instance table
		var successfullyAdded []VMInterfacePair // VMs that were successfully added

		logger.Debugf("Attempting to link VMs to %s rule, ruleID: %s", ruleCategory, ruleID)
		logger.Debugf("Found rule group: %s, Type: %s, RuleID: %s", groupUUID, groupType, ruleID)
		logger.Debugf("Processing VM links for %s rule: groupUUID=%s, vmCount=%d", ruleCategory, groupUUID, len(req.VMLinks))

		for _, link := range req.VMLinks {
			// Check if already linked
			exists := a.operator.CheckVMLinkExists(
				c.Request.Context(),
				groupUUID,
				link.VMUUID,
				link.Interface,
			)

			if exists {
				alreadyLinked = append(alreadyLinked, link.VMUUID)
				logger.Debugf("VM already linked: vm_uuid=%s, interface=%s", link.VMUUID, link.Interface)
				continue
			}

			// Verify instance exists before creating link
			_, err := routes.GetDomainByInstanceUUID(c.Request.Context(), link.VMUUID)
			if err != nil {
				notFoundInstances = append(notFoundInstances, link.VMUUID)
				logger.Errorf("Instance not found: vm_uuid=%s, error=%v", link.VMUUID, err)
				continue
			}

			// Create VM link in database
			err = a.operator.CreateVMLink(
				c.Request.Context(),
				groupUUID,
				link.VMUUID,
				link.Interface,
			)
			if err != nil {
				logger.Errorf("Failed to create VM link in database: vm_uuid=%s, error=%v", link.VMUUID, err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Failed to create VM link",
					"code":  "DATABASE_ERROR",
					"details": gin.H{
						"vm_uuid": link.VMUUID,
						"message": err.Error(),
					},
				})
				return
			}

			successfullyAdded = append(successfullyAdded, VMInterfacePair{
				VMUUID:    link.VMUUID,
				Interface: link.Interface,
			})
			logger.Infof("Successfully created VM link: vm_uuid=%s, interface=%s", link.VMUUID, link.Interface)
		}

		// If there are validation errors, return error response
		if len(notFoundInstances) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Some instances do not exist",
				"code":  "INSTANCES_NOT_FOUND",
				"details": gin.H{
					"not_found_instances": notFoundInstances,
					"already_linked":      alreadyLinked,
				},
			})
			return
		}

		if len(alreadyLinked) > 0 && len(successfullyAdded) == 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error": "All VMs are already linked to this rule",
				"code":  "VMS_ALREADY_LINKED",
				"details": gin.H{
					"already_linked": alreadyLinked,
				},
			})
			return
		}

		// Construct alarm type based on rule category
		alarmType := ruleCategory + "-" + groupType
		// Normalize adjust rule types to match creation-time rule_id format used by Prometheus rules
		if ruleCategory == "adjust" {
			if groupType == model.RuleTypeAdjustInBW || groupType == model.RuleTypeAdjustOutBW {
				alarmType = "adjust-bw"
			} else if groupType == model.RuleTypeAdjustCPU {
				alarmType = "adjust-cpu"
			}
		}
		// Normalize adjust rule types to match creation-time rule_id format used by Prometheus rules
		if ruleCategory == "adjust" {
			if groupType == model.RuleTypeAdjustInBW || groupType == model.RuleTypeAdjustOutBW {
				alarmType = "adjust-bw"
			} else if groupType == model.RuleTypeAdjustCPU {
				alarmType = "adjust-cpu"
			}
		}

		// Update matched_vms.json only for successfully added VMs
		if len(successfullyAdded) > 0 {
			// Group by interface for batch processing
			interfaceGroups := make(map[string][]string)
			for _, pair := range successfullyAdded {
				interfaceGroups[pair.Interface] = append(interfaceGroups[pair.Interface], pair.VMUUID)
			}

			logger.Debugf("Adding/updating VM mappings for rule group %s, VM count: %d", groupUUID, len(successfullyAdded))
			for iface, vmUUIDs := range interfaceGroups {
				err := a.UpdateMatchedVMsJSON(
					c.Request.Context(),
					vmUUIDs,
					groupUUID,
					"add",
					alarmType,
					iface,
				)
				if err != nil {
					logger.Errorf("Failed to update matched_vms.json: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": "Failed to update Prometheus configuration",
						"code":  "PROMETHEUS_CONFIG_UPDATE_FAILED",
						"details": gin.H{
							"message": err.Error(),
						},
					})
					return
				}
			}
			logger.Infof("VM links saved successfully: groupUUID=%s", groupUUID)
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
		if err := routes.ReloadPrometheusViaHTTP(); err != nil {
			logger.Errorf("Warning: Failed to reload Prometheus: %v", err)
		}

		logger.Infof("Successfully linked VMs to %s rule, rule_category: %s, group_uuid: %s", ruleCategory, ruleCategory, groupUUID)

		// Build response with details
		responseData := gin.H{
			"rule_category":    ruleCategory,
			"group_uuid":       groupUUID,
			"rule_id":          ruleID,
			"added_count":      len(successfullyAdded),
			"total_linked_vms": linkedVMsList,
		}

		// Include warnings if some VMs were already linked
		if len(alreadyLinked) > 0 {
			responseData["warnings"] = gin.H{
				"already_linked": alreadyLinked,
				"message":        "Some VMs were already linked and were skipped",
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   responseData,
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

		// Resolve group info by category (alarm / adjust)
		var (
			groupUUID string
			ruleID    string
			groupType string
			err       error
		)

		if ruleCategory == "adjust" {
			adj := &routes.AdjustOperator{}
			identifier := req.RuleID
			if identifier == "" {
				identifier = req.GroupUUID
			}
			adjGroup, adjErr := adj.GetAdjustRulesByIdentifier(c.Request.Context(), identifier)
			if errors.Is(adjErr, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
				return
			} else if adjErr != nil {
				logger.Errorf("Error retrieving adjust rule group: %v", adjErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
				return
			}
			groupUUID = adjGroup.UUID
			ruleID = adjGroup.RuleID
			groupType = adjGroup.Type
		} else {
			var group *model.RuleGroupV2
			if req.RuleID != "" {
				group, err = a.operator.GetRulesByRuleID(c.Request.Context(), req.RuleID)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// optional fallback: try as group uuid
					group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), req.RuleID)
				}
			} else {
				group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), req.GroupUUID)
			}

			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
				return
			} else if err != nil {
				logger.Errorf("Error retrieving rule group: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
				return
			}
			groupUUID = group.UUID
			ruleID = group.RuleID
			groupType = group.Type
		}

		logger.Debugf("Attempting to unlink VMs from %s rule, ruleID: %s", ruleCategory, ruleID)
		logger.Debugf("Found rule group: %s, Type: %s, RuleID: %s", groupUUID, groupType, ruleID)

		// Check if this is batch delete (all interfaces are empty) or specific delete
		isBatchDelete := len(req.VMLinks) > 0 && req.VMLinks[0].Interface == ""

		type DeletedLink struct {
			VMUUID    string
			Interface string
		}
		var successfulDeletes []DeletedLink
		var notLinkedVMs []map[string]interface{} // VMs that were not linked
		totalDeleted := int64(0)

		logger.Debugf("Processing VM unlinks for %s rule: groupUUID=%s, vmCount=%d, batchDelete=%v",
			ruleCategory, groupUUID, len(req.VMLinks), isBatchDelete)

		if isBatchDelete {
			// Batch delete: delete all interfaces for each VM
			for _, link := range req.VMLinks {
				deletedCount, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, link.VMUUID, "")
				if err != nil {
					logger.Debugf("VM unlinking database operation failed for %s: %v", link.VMUUID, err)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": "Failed to delete VM link from database",
						"code":  "DATABASE_ERROR",
						"details": gin.H{
							"vm_uuid": link.VMUUID,
							"message": err.Error(),
						},
					})
					return
				}

				if deletedCount == 0 {
					// VM was not linked - report this as an error
					notLinkedVMs = append(notLinkedVMs, map[string]interface{}{
						"vm_uuid": link.VMUUID,
					})
					logger.Debugf("VM was not linked to this rule: vm_uuid=%s", link.VMUUID)
					continue
				}

				successfulDeletes = append(successfulDeletes, DeletedLink{
					VMUUID:    link.VMUUID,
					Interface: "",
				})
				totalDeleted += deletedCount
				logger.Debugf("Successfully unlinked VM (batch): vm_uuid=%s, deleted_count=%d", link.VMUUID, deletedCount)
			}
		} else {
			// Specific delete: delete specific (vm_uuid, interface) pairs
			for _, link := range req.VMLinks {
				deletedCount, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, link.VMUUID, link.Interface)
				if err != nil {
					logger.Debugf("VM unlinking database operation failed for %s (interface: %s): %v", link.VMUUID, link.Interface, err)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": "Failed to delete VM link from database",
						"code":  "DATABASE_ERROR",
						"details": gin.H{
							"vm_uuid":   link.VMUUID,
							"interface": link.Interface,
							"message":   err.Error(),
						},
					})
					return
				}

				if deletedCount == 0 {
					// VM was not linked - report this as an error
					notLinkedVMs = append(notLinkedVMs, map[string]interface{}{
						"vm_uuid":   link.VMUUID,
						"interface": link.Interface,
					})
					logger.Debugf("VM was not linked to this rule: vm_uuid=%s, interface=%s", link.VMUUID, link.Interface)
					continue
				}

				successfulDeletes = append(successfulDeletes, DeletedLink{
					VMUUID:    link.VMUUID,
					Interface: link.Interface,
				})
				totalDeleted += deletedCount
				logger.Debugf("Successfully unlinked VM: vm_uuid=%s, interface=%s", link.VMUUID, link.Interface)
			}
		}

		// If no VMs were actually linked, return error
		if len(successfulDeletes) == 0 && len(notLinkedVMs) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "No VMs were linked to this rule",
				"code":  "VMS_NOT_LINKED",
				"details": gin.H{
					"not_linked_vms": notLinkedVMs,
					"group_uuid":     groupUUID,
				},
			})
			return
		}

		// Construct alarm type based on rule category
		alarmType := ruleCategory + "-" + groupType
		// Normalize adjust rule types to match creation-time rule_id format used by Prometheus rules
		if ruleCategory == "adjust" {
			if groupType == model.RuleTypeAdjustInBW || groupType == model.RuleTypeAdjustOutBW {
				alarmType = "adjust-bw"
			} else if groupType == model.RuleTypeAdjustCPU {
				alarmType = "adjust-cpu"
			}
		}

		// Remove successfully unlinked VMs from matched_vms.json
		if len(successfulDeletes) > 0 {
			// Group by interface for batch processing
			interfaceGroups := make(map[string][]string)
			for _, deleted := range successfulDeletes {
				interfaceGroups[deleted.Interface] = append(interfaceGroups[deleted.Interface], deleted.VMUUID)
			}

			logger.Debugf("Removing VM mappings from matched_vms.json for rule group %s, VM count: %d", groupUUID, len(successfulDeletes))
			for iface, vmUUIDs := range interfaceGroups {
				err := a.UpdateMatchedVMsJSON(c.Request.Context(), vmUUIDs, groupUUID, "remove", alarmType, iface)
				if err != nil {
					logger.Errorf("Failed to update matched_vms.json: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": "Failed to update Prometheus configuration",
						"code":  "PROMETHEUS_CONFIG_UPDATE_FAILED",
						"details": gin.H{
							"message": err.Error(),
						},
					})
					return
				}
			}
			logger.Infof("VM unlinks saved successfully: groupUUID=%s", groupUUID)
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
		if err := routes.ReloadPrometheusViaHTTP(); err != nil {
			logger.Errorf("Warning: Failed to reload Prometheus: %v", err)
		}

		logger.Infof("Successfully unlinked VMs from %s rule, rule_category: %s, group_uuid: %s, unlinked_count: %d",
			ruleCategory, ruleCategory, groupUUID, totalDeleted)

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
			"rule_id":         ruleID,
			"unlinked_vms":    unlinkedList,
			"unlinked_count":  len(successfulDeletes),
			"remaining_vms":   remainingVMsList,
			"total_deleted":   totalDeleted,
			"is_batch_delete": isBatchDelete,
		}

		// Include warnings if some VMs were not linked
		if len(notLinkedVMs) > 0 {
			responseData["warnings"] = gin.H{
				"not_linked_vms": notLinkedVMs,
				"message":        "Some VMs were not linked to this rule and were skipped",
			}
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
		Operator        string           `json:"operator"` // gt/lt/gte/lte/eq (default: gt)
		DurationMinutes int              `json:"duration_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate RuleID is in UUID format (ensures uniqueness)
	if req.RuleID == "" || !isValidUUID(req.RuleID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id must be a valid UUID format"})
		return
	}

	// Validate: only one rule can be created at a time (BEFORE any database operations)
	if len(req.Rules) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only one rule can be created at a time."})
		return
	}

	if _, err := a.n9eOperator.GetCPURuleByUUID(c.Request.Context(), req.RuleID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CPU rule already exists with rule_id: " + req.RuleID})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query existing CPU rule: " + err.Error()})
		return
	}

	// Step 1: Create N9E Business Group (or get existing one)
	regionName, err := normalizeBusinessGroupName(viper.GetString("console.host"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to normalize business group name: " + err.Error()})
		return
	}
	businessGroup, err := a.n9eOperator.GetBusinessGroupByName(c.Request.Context(), regionName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query business group: " + err.Error()})
		return
	}

	// If business group doesn't exist, create it
	if businessGroup == nil {
		businessGroup = &model.N9EBusinessGroup{
			Name:     regionName,
			Owner:    req.Owner,
			RegionID: req.RegionID,
			Level:    req.Level,
			Enabled:  true,
		}
		if err := a.n9eOperator.CreateBusinessGroup(c.Request.Context(), businessGroup); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create business group: " + err.Error()})
			return
		}
	}

	// Step 2: Create N9E CPU Rule
	rule := req.Rules[0]
	operator := resolveOperator(req.Operator)
	cpuRule := &model.N9ECPURule{
		RuleID:            req.RuleID,
		BusinessGroupUUID: businessGroup.UUID,
		Name:              rule.Name,
		Owner:             req.Owner,
		Duration:          rule.Duration,
		DurationMinutes:   req.DurationMinutes,
		Operator:          operator,
		Enabled:           true,
	}
	if err := a.n9eOperator.CreateCPURule(c.Request.Context(), cpuRule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create CPU rule: " + err.Error()})
		return
	}

	// Step 3: Initialize N9E components and render PromQL template
	a.InitN9EComponents()

	// Render PromQL template
	templateParams := map[string]interface{}{
		"duration": cpuRule.Duration,
		"operator": operator,
	}
	promql, templateErr := a.templateRenderer.RenderN9EPromQL("cpu", cpuRule.RuleID, templateParams)
	if templateErr != nil {
		log.Printf("Warning: Failed to render CPU PromQL template for rule %s: %v", cpuRule.RuleID, templateErr)
	}

	// Step 4: Create/get N9E business group and create alert rule (idempotent)
	log.Printf("[DEBUG-CPURULE] Step 4: Starting N9E integration for rule %s", cpuRule.UUID)
	if a.n9eClient != nil && promql != "" {
		log.Printf("[DEBUG-CPURULE] Calling GetOrCreateBusinessGroup for region: %s", regionName)
		n9eGroupID, bgErr := a.n9eClient.GetOrCreateBusinessGroup(c.Request.Context(), regionName)
		if bgErr != nil {
			log.Printf("[DEBUG-CPURULE] ERROR: Failed to get/create N9E business group for region %s: %v", regionName, bgErr)
			log.Printf("Warning: Failed to get/create N9E business group for region %s: %v", regionName, bgErr)
		} else {
			log.Printf("[DEBUG-CPURULE] N9E Business Group ID: %d", n9eGroupID)
			datasourceName := viper.GetString("n9e.n9e_data_source")
			log.Printf("[DEBUG-CPURULE] Datasource name from config: %s", datasourceName)
			datasourceID, dsErr := a.n9eClient.GetDataSourceByName(c.Request.Context(), datasourceName)
			if dsErr != nil {
				log.Printf("[DEBUG-CPURULE] ERROR: Failed to get N9E datasource %s: %v", datasourceName, dsErr)
				log.Printf("Warning: Failed to get N9E datasource %s: %v", datasourceName, dsErr)
			}
			log.Printf("[DEBUG-CPURULE] Datasource ID: %d", datasourceID)
			if n9eGroupID > 0 && datasourceID > 0 {
				n9eRule := routes.N9EAlertRule{
					GroupID:          n9eGroupID,
					RuleName:         cpuRule.Name,
					Severity:         getSeverityLevel(req.Level),
					Disabled:         0,
					DatasourceIDs:    []int64{datasourceID},
					PromForDuration:  req.DurationMinutes * 60,
					PromEvalInterval: 60,
					NotifyRepeatStep: 60,
					RuleConfig: routes.N9ERuleConfig{
						Queries: []routes.N9EQuery{
							{PromQL: promql, Severity: getSeverityLevel(req.Level)},
						},
					},
				}
				log.Printf("[DEBUG-CPURULE] Calling CreateAlertRule for rule name: %s, BG ID: %d", cpuRule.Name, n9eGroupID)
				n9eAlertRuleID, ruleErr := a.n9eClient.CreateAlertRule(c.Request.Context(), n9eRule)
				if ruleErr != nil {
					log.Printf("[DEBUG-CPURULE] ERROR: CreateAlertRule failed: %v", ruleErr)
					log.Printf("Warning: Failed to create N9E alert rule for CPU rule %s: %v", cpuRule.RuleID, ruleErr)
				} else if n9eAlertRuleID > 0 {
					log.Printf("[DEBUG-CPURULE] SUCCESS: N9E alert rule created with ID: %d", n9eAlertRuleID)
					log.Printf("[DEBUG-CPURULE] Calling UpdateCPURuleN9EID to store alert rule ID %d for CPU rule %s", n9eAlertRuleID, cpuRule.RuleID)
					if err := a.n9eOperator.UpdateCPURuleN9EID(c.Request.Context(), cpuRule.RuleID, n9eAlertRuleID); err != nil {
						log.Printf("[DEBUG-CPURULE] ERROR: UpdateCPURuleN9EID failed: %v", err)
						log.Printf("Warning: Failed to store N9E alert rule ID for CPU rule %s: %v", cpuRule.RuleID, err)
					} else {
						log.Printf("[DEBUG-CPURULE] SUCCESS: N9E alert rule ID %d stored in DB for rule %s", n9eAlertRuleID, cpuRule.RuleID)
					}
				} else {
					log.Printf("[DEBUG-CPURULE] WARNING: CreateAlertRule returned 0 for alert rule ID")
				}
			} else {
				log.Printf("[DEBUG-CPURULE] Skipping alert rule creation: n9eGroupID=%d, datasourceID=%d", n9eGroupID, datasourceID)
			}
		}
	} else {
		log.Printf("[DEBUG-CPURULE] Skipping N9E integration: n9eClient=%v, promql_empty=%v", a.n9eClient == nil, promql == "")
	}

	// Step 5: Create VM rule links in database
	// Note: anchor threshold metrics (vm_cpu_anchor) must be written via LinkVMsToRule,
	// since region/domain info is required for correct label matching in PromQL join.
	if len(req.LinkedVMs) > 0 {
		for _, vmUUID := range req.LinkedVMs {
			link := &model.N9EVMRuleLink{
				RuleType:          "cpu",
				RuleUUID:          cpuRule.RuleID,
				BusinessGroupUUID: businessGroup.UUID,
				VMUUID:            vmUUID,
				Interface:         "",
				Owner:             req.Owner,
			}
			if err := a.n9eOperator.CreateVMRuleLink(c.Request.Context(), link); err != nil {
				log.Printf("Warning: Failed to create VM rule link: %v", err)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"business_group_uuid": businessGroup.UUID,
			"rule_id":             cpuRule.RuleID,
			"enabled":             true,
			"linkedvms":           req.LinkedVMs,
			"region_id":           req.RegionID,
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
		Operator        string              `json:"operator"` // gt/lt/gte/lte/eq (default: gt)
		DurationMinutes int                 `json:"duration_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate RuleID is in UUID format (ensures uniqueness)
	if req.RuleID == "" || !isValidUUID(req.RuleID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id must be a valid UUID format"})
		return
	}

	// Validate: only one rule can be created at a time
	if len(req.Rules) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only one rule can be created at a time."})
		return
	}

	if _, err := a.n9eOperator.GetMemoryRuleByUUID(c.Request.Context(), req.RuleID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Memory rule already exists with rule_id: " + req.RuleID})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query existing memory rule: " + err.Error()})
		return
	}

	// Step 1: Create N9E Business Group (or get existing one)
	regionName, err := normalizeBusinessGroupName(viper.GetString("console.host"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to normalize business group name: " + err.Error()})
		return
	}
	businessGroup, err := a.n9eOperator.GetBusinessGroupByName(c.Request.Context(), regionName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query business group: " + err.Error()})
		return
	}

	// If business group doesn't exist, create it
	if businessGroup == nil {
		businessGroup = &model.N9EBusinessGroup{
			Name:     regionName,
			Owner:    req.Owner,
			RegionID: req.RegionID,
			Level:    req.Level,
			Enabled:  true,
		}
		if err := a.n9eOperator.CreateBusinessGroup(c.Request.Context(), businessGroup); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create business group: " + err.Error()})
			return
		}
	}

	// Step 2: Create N9E Memory Rule
	rule := req.Rules[0]
	memOperator := resolveOperator(req.Operator)
	memoryRule := &model.N9EMemoryRule{
		RuleID:            req.RuleID,
		BusinessGroupUUID: businessGroup.UUID,
		Name:              rule.Name,
		Owner:             req.Owner,
		Duration:          rule.Duration,
		DurationMinutes:   req.DurationMinutes,
		Operator:          memOperator,
		Enabled:           true,
	}
	if err := a.n9eOperator.CreateMemoryRule(c.Request.Context(), memoryRule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create memory rule: " + err.Error()})
		return
	}

	// Step 3: Initialize N9E components and render PromQL template
	a.InitN9EComponents()

	// Render PromQL template
	templateParams := map[string]interface{}{
		"duration": memoryRule.Duration,
		"operator": memOperator,
	}
	promql, templateErr := a.templateRenderer.RenderN9EPromQL("memory", memoryRule.RuleID, templateParams)
	if templateErr != nil {
		log.Printf("Warning: Failed to render memory PromQL template for rule %s: %v", memoryRule.RuleID, templateErr)
	}

	// Step 4: Create/get N9E business group and create alert rule (idempotent)
	if a.n9eClient != nil && promql != "" {
		n9eGroupID, bgErr := a.n9eClient.GetOrCreateBusinessGroup(c.Request.Context(), regionName)
		if bgErr != nil {
			log.Printf("Warning: Failed to get/create N9E business group for region %s: %v", regionName, bgErr)
		} else {
			datasourceName := viper.GetString("n9e.n9e_data_source")
			datasourceID, dsErr := a.n9eClient.GetDataSourceByName(c.Request.Context(), datasourceName)
			if dsErr != nil {
				log.Printf("Warning: Failed to get N9E datasource %s: %v", datasourceName, dsErr)
			}
			if n9eGroupID > 0 && datasourceID > 0 {
				n9eRule := routes.N9EAlertRule{
					GroupID:          n9eGroupID,
					RuleName:         memoryRule.Name,
					Severity:         getSeverityLevel(req.Level),
					Disabled:         0,
					DatasourceIDs:    []int64{datasourceID},
					PromForDuration:  req.DurationMinutes * 60,
					PromEvalInterval: 60,
					NotifyRepeatStep: 60,
					RuleConfig: routes.N9ERuleConfig{
						Queries: []routes.N9EQuery{
							{PromQL: promql, Severity: getSeverityLevel(req.Level)},
						},
					},
				}
				n9eAlertRuleID, ruleErr := a.n9eClient.CreateAlertRule(c.Request.Context(), n9eRule)
				if ruleErr != nil {
					log.Printf("Warning: Failed to create N9E alert rule for memory rule %s: %v", memoryRule.RuleID, ruleErr)
				} else if n9eAlertRuleID > 0 {
					if err := a.n9eOperator.UpdateMemoryRuleN9EID(c.Request.Context(), memoryRule.RuleID, n9eAlertRuleID); err != nil {
						log.Printf("Warning: Failed to store N9E alert rule ID for memory rule %s: %v", memoryRule.RuleID, err)
					}
				}
			}
		}
	}

	// Step 5: Create VM rule links in database
	// Note: anchor threshold metrics (vm_mem_anchor) must be written via LinkVMsToRule,
	// since region/domain info is required for correct label matching in PromQL join.
	if len(req.LinkedVMs) > 0 {
		for _, vmUUID := range req.LinkedVMs {
			link := &model.N9EVMRuleLink{
				RuleType:          "memory",
				RuleUUID:          memoryRule.RuleID,
				BusinessGroupUUID: businessGroup.UUID,
				VMUUID:            vmUUID,
				Interface:         "",
				Owner:             req.Owner,
			}
			if err := a.n9eOperator.CreateVMRuleLink(c.Request.Context(), link); err != nil {
				log.Printf("Warning: Failed to create VM rule link: %v", err)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"business_group_uuid": businessGroup.UUID,
			"rule_id":             memoryRule.RuleID,
			"enabled":             true,
			"linkedvms":           req.LinkedVMs,
			"region_id":           req.RegionID,
		},
	})
}

func (a *AlarmAPI) GetCPURules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	uuid := c.Param("uuid")
	owner := c.Query("owner")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	// If uuid is provided, query single rule
	if uuid != "" {
		cpuRule, err := a.n9eOperator.GetCPURuleByUUID(c.Request.Context(), uuid)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "CPU rule not found"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query CPU rule: " + err.Error()})
			return
		}

		// Get business group
		businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), cpuRule.BusinessGroupUUID)
		if err != nil {
			log.Printf("Warning: Failed to get business group for CPU rule %s: %v", uuid, err)
		}

		// Get linked VMs
		linkedVMs := make([]string, 0)
		vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), cpuRule.RuleID)
		if err == nil {
			for _, link := range vmLinks {
				if link.RuleType == "cpu" {
					linkedVMs = append(linkedVMs, link.VMUUID)
				}
			}
		}

		responseData := gin.H{
			"rule_id":           cpuRule.RuleID,
			"n9e_alert_rule_id": cpuRule.N9EAlertRuleID,
			"name":              cpuRule.Name,
			"owner":             cpuRule.Owner,
			"duration":          cpuRule.Duration,
			"linkedvms":         linkedVMs,
			"enabled":           cpuRule.Enabled,
			"region_id":         businessGroup.RegionID,
			"level":             businessGroup.Level,
		}

		c.JSON(http.StatusOK, gin.H{
			"data": []gin.H{responseData},
			"meta": gin.H{
				"total":        1,
				"current_page": 1,
				"per_page":     1,
				"total_pages":  1,
			},
		})
		return
	}

	// List rules with pagination
	queryParams := routes.ListN9ERulesParams{
		Page:     page,
		PageSize: pageSize,
		Owner:    owner,
	}
	queryParams.SetDefaults()

	rules, total, err := a.n9eOperator.ListCPURules(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query CPU rules: " + err.Error()})
		return
	}

	responseData := make([]gin.H, 0, len(rules))
	for _, rule := range rules {
		// Get business group
		businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), rule.BusinessGroupUUID)
		if err != nil {
			log.Printf("Warning: Failed to get business group for CPU rule %s: %v", rule.UUID, err)
		}

		var regionID, level, businessGroupName string
		if businessGroup != nil {
			regionID = businessGroup.RegionID
			level = businessGroup.Level
			businessGroupName = businessGroup.Name
		}

		// Get linked VMs
		linkedVMs := make([]string, 0)
		vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), rule.RuleID)
		if err == nil {
			for _, link := range vmLinks {
				if link.RuleType == "cpu" {
					linkedVMs = append(linkedVMs, link.VMUUID)
				}
			}
		}

		responseData = append(responseData, gin.H{
			"rule_id":             rule.RuleID,
			"n9e_alert_rule_id":   rule.N9EAlertRuleID,
			"name":                rule.Name,
			"owner":               rule.Owner,
			"duration":            rule.Duration,
			"linkedvms":           linkedVMs,
			"enabled":             rule.Enabled,
			"business_group_uuid": rule.BusinessGroupUUID,
			"business_group_name": businessGroupName,
			"region_id":           regionID,
			"level":               level,
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
	uuid := c.Param("uuid")
	owner := c.Query("owner")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	// If uuid is provided, query single rule
	if uuid != "" {
		memoryRule, err := a.n9eOperator.GetMemoryRuleByUUID(c.Request.Context(), uuid)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Memory rule not found"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query memory rule: " + err.Error()})
			return
		}

		// Get business group
		businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), memoryRule.BusinessGroupUUID)
		if err != nil {
			log.Printf("Warning: Failed to get business group for memory rule %s: %v", uuid, err)
		}

		// Get linked VMs
		linkedVMs := make([]string, 0)
		vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), memoryRule.RuleID)
		if err == nil {
			for _, link := range vmLinks {
				if link.RuleType == "memory" {
					linkedVMs = append(linkedVMs, link.VMUUID)
				}
			}
		}

		responseData := gin.H{
			"rule_id":           memoryRule.RuleID,
			"n9e_alert_rule_id": memoryRule.N9EAlertRuleID,
			"name":              memoryRule.Name,
			"owner":             memoryRule.Owner,
			"duration":          memoryRule.Duration,
			"linkedvms":         linkedVMs,
			"enabled":           memoryRule.Enabled,
			"region_id":         businessGroup.RegionID,
			"level":             businessGroup.Level,
		}

		c.JSON(http.StatusOK, gin.H{
			"data": []gin.H{responseData},
			"meta": gin.H{
				"total":        1,
				"current_page": 1,
				"per_page":     1,
				"total_pages":  1,
			},
		})
		return
	}

	// List rules with pagination
	queryParams := routes.ListN9ERulesParams{
		Page:     page,
		PageSize: pageSize,
		Owner:    owner,
	}
	queryParams.SetDefaults()

	rules, total, err := a.n9eOperator.ListMemoryRules(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query memory rules: " + err.Error()})
		return
	}

	responseData := make([]gin.H, 0, len(rules))
	for _, rule := range rules {
		// Get business group
		businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), rule.BusinessGroupUUID)
		if err != nil {
			log.Printf("Warning: Failed to get business group for memory rule %s: %v", rule.UUID, err)
		}

		var regionID, level, businessGroupName string
		if businessGroup != nil {
			regionID = businessGroup.RegionID
			level = businessGroup.Level
			businessGroupName = businessGroup.Name
		}

		// Get linked VMs
		linkedVMs := make([]string, 0)
		vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), rule.RuleID)
		if err == nil {
			for _, link := range vmLinks {
				if link.RuleType == "memory" {
					linkedVMs = append(linkedVMs, link.VMUUID)
				}
			}
		}

		responseData = append(responseData, gin.H{
			"rule_id":             rule.RuleID,
			"n9e_alert_rule_id":   rule.N9EAlertRuleID,
			"name":                rule.Name,
			"owner":               rule.Owner,
			"duration":            rule.Duration,
			"linkedvms":           linkedVMs,
			"enabled":             rule.Enabled,
			"business_group_uuid": rule.BusinessGroupUUID,
			"business_group_name": businessGroupName,
			"region_id":           regionID,
			"level":               level,
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
	uuid := c.Param("uuid")
	log.Printf("[DEBUG-CPURULE] ===== DeleteCPURule START for UUID: %s =====", uuid)
	if uuid == "" {
		log.Printf("[DEBUG-CPURULE] ERROR: Empty UUID")
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "empty uuid error.",
			"code":  "INVALID_UUID",
		})
		return
	}

	// Step 1: Get CPU rule
	log.Printf("[DEBUG-CPURULE] Step 1: Getting CPU rule from DB")
	cpuRule, err := a.n9eOperator.GetCPURuleByUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[DEBUG-CPURULE] ERROR: Rule not found in DB")
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Rule not found: The specified rule does not exist",
			"code":  "NOT_FOUND",
			"uuid":  uuid,
		})
		return
	} else if err != nil {
		log.Printf("[DEBUG-CPURULE] ERROR: DB query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve rule information",
			"code":  "INTERNAL_ERROR",
			"uuid":  uuid,
		})
		return
	}
	log.Printf("[DEBUG-CPURULE] Found CPU rule: name=%s, n9e_alert_rule_id=%d, bg_uuid=%s", cpuRule.Name, cpuRule.N9EAlertRuleID, cpuRule.BusinessGroupUUID)

	// Step 2: Get business group
	log.Printf("[DEBUG-CPURULE] Step 2: Getting business group %s", cpuRule.BusinessGroupUUID)
	businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), cpuRule.BusinessGroupUUID)
	if err != nil {
		log.Printf("[DEBUG-CPURULE] WARNING: Failed to get business group: %v", err)
		log.Printf("Warning: Failed to get business group %s: %v", cpuRule.BusinessGroupUUID, err)
	} else {
		log.Printf("[DEBUG-CPURULE] Found business group: name=%s, n9e_bg_id=%d", businessGroup.Name, businessGroup.N9EBusinessGroupID)
	}

	// Step 3: Initialize components
	a.InitN9EComponents()

	// Step 4: Get linked VMs and clear anchor thresholds
	vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), uuid)
	linkedVMs := []string{}
	instanceUUIDs := []string{}

	if err == nil && len(vmLinks) > 0 {
		for _, link := range vmLinks {
			if link.RuleType != "cpu" {
				continue
			}
			linkedVMs = append(linkedVMs, link.VMUUID)
			instanceUUIDs = append(instanceUUIDs, link.VMUUID)
		}
	}

	// Step 5: Clear anchor thresholds in VictoriaMetrics
	// Note: Region/Domain not stored in DB link; partial clear uses just ruleUUID+instanceID.
	// Remaining anchor data expires via last_over_time retention.
	if len(instanceUUIDs) > 0 && a.anchorManager != nil {
		anchorInstances := make([]routes.AnchorInstance, len(instanceUUIDs))
		for i, vmUUID := range instanceUUIDs {
			anchorInstances[i] = routes.AnchorInstance{
				RuleUUID:   uuid,
				InstanceID: vmUUID,
				Owner:      cpuRule.Owner,
			}
		}
		if err := a.anchorManager.ClearAnchorThresholds(c.Request.Context(), "cpu", anchorInstances); err != nil {
			log.Printf("Warning: Failed to clear CPU anchor thresholds: %v", err)
		}
	}

	// Step 6: Delete VM links from database
	for _, link := range vmLinks {
		if _, err := a.n9eOperator.DeleteVMRuleLink(c.Request.Context(), uuid, link.VMUUID, link.Interface); err != nil {
			log.Printf("Warning: Failed to delete VM rule link: %v", err)
		}
	}

	// Step 6.5: Delete N9E alert rule if exists
	log.Printf("[DEBUG-CPURULE] Step 6.5: Checking N9E alert rule deletion for rule %s", uuid)
	log.Printf("[DEBUG-CPURULE] cpuRule.N9EAlertRuleID=%d, n9eClient=%v, businessGroup=%v", cpuRule.N9EAlertRuleID, a.n9eClient != nil, businessGroup != nil)
	if cpuRule.N9EAlertRuleID > 0 && a.n9eClient != nil && businessGroup != nil {
		log.Printf("[DEBUG-CPURULE] Attempting to delete N9E alert rule ID %d from BG '%s'", cpuRule.N9EAlertRuleID, businessGroup.Name)
		n9eBGID, bgErr := a.n9eClient.GetBusinessGroupByName(c.Request.Context(), businessGroup.Name)
		if bgErr != nil {
			log.Printf("[DEBUG-CPURULE] ERROR: Failed to find N9E business group '%s': %v", businessGroup.Name, bgErr)
			log.Printf("Warning: Failed to find N9E business group '%s' for alert rule deletion: %v", businessGroup.Name, bgErr)
		} else {
			log.Printf("[DEBUG-CPURULE] Found N9E BG ID: %d, deleting alert rule %d", n9eBGID, cpuRule.N9EAlertRuleID)
			if delErr := a.n9eClient.DeleteAlertRule(c.Request.Context(), n9eBGID, cpuRule.N9EAlertRuleID); delErr != nil {
				log.Printf("[DEBUG-CPURULE] ERROR: DeleteAlertRule failed: %v", delErr)
				log.Printf("Warning: Failed to delete N9E alert rule ID %d: %v", cpuRule.N9EAlertRuleID, delErr)
			} else {
				log.Printf("[DEBUG-CPURULE] SUCCESS: Deleted N9E alert rule ID %d from BG %d", cpuRule.N9EAlertRuleID, n9eBGID)
				log.Printf("[N9E] Deleted alert rule ID %d from BG %d", cpuRule.N9EAlertRuleID, n9eBGID)
			}
		}
	} else {
		log.Printf("[DEBUG-CPURULE] SKIP: N9E alert rule deletion skipped (conditions not met)")
	}

	// Step 7: Delete CPU rule from local database
	if err := a.n9eOperator.DeleteCPURule(c.Request.Context(), uuid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}

	// Step 8: Check if business group should be auto-deleted
	log.Printf("[DEBUG-CPURULE] Step 8: Checking if BG should be auto-deleted")
	if businessGroup != nil {
		count, err := a.n9eOperator.CountRulesByBusinessGroupUUID(c.Request.Context(), businessGroup.UUID)
		if err != nil {
			log.Printf("[DEBUG-CPURULE] ERROR: Failed to count rules: %v", err)
			log.Printf("Warning: Failed to count rules for business group %s: %v", businessGroup.UUID, err)
		} else {
			log.Printf("[DEBUG-CPURULE] Remaining rules in BG %s: %d", businessGroup.UUID, count)
			if count == 0 {
				// No more rules, delete business group from local DB
				log.Printf("[DEBUG-CPURULE] Auto-deleting BG (no remaining rules)")
				log.Printf("Auto-deleting business group %s (no remaining rules)", businessGroup.UUID)
				if err := a.n9eOperator.DeleteBusinessGroup(c.Request.Context(), businessGroup.UUID); err != nil {
					log.Printf("[DEBUG-CPURULE] ERROR: Failed to delete BG from local DB: %v", err)
					log.Printf("Warning: Failed to delete business group from DB %s: %v", businessGroup.UUID, err)
				} else {
					log.Printf("[DEBUG-CPURULE] SUCCESS: Deleted BG from local DB")
				}
				// Also delete from N9E API side
				if a.n9eClient != nil {
					log.Printf("[DEBUG-CPURULE] Deleting BG '%s' from N9E", businessGroup.Name)
					n9eBGID, bgErr := a.n9eClient.GetBusinessGroupByName(c.Request.Context(), businessGroup.Name)
					if bgErr != nil {
						log.Printf("[DEBUG-CPURULE] ERROR: Failed to find N9E BG: %v", bgErr)
						log.Printf("Warning: Failed to find N9E business group '%s': %v", businessGroup.Name, bgErr)
					} else {
						log.Printf("[DEBUG-CPURULE] Found N9E BG ID: %d, deleting...", n9eBGID)
						if delErr := a.n9eClient.DeleteBusinessGroupByID(c.Request.Context(), n9eBGID); delErr != nil {
							log.Printf("[DEBUG-CPURULE] ERROR: Failed to delete N9E BG: %v", delErr)
							log.Printf("Warning: Failed to delete N9E business group ID %d: %v", n9eBGID, delErr)
						} else {
							log.Printf("[DEBUG-CPURULE] SUCCESS: Deleted N9E BG ID %d", n9eBGID)
						}
					}
				}
			}
		}
	}

	log.Printf("[DEBUG-CPURULE] ===== DeleteCPURule SUCCESS for UUID: %s =====", uuid)
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"rule_id":     uuid,
			"linked_vms":  linkedVMs,
			"unbound_vms": len(instanceUUIDs),
		},
	})
}

func (a *AlarmAPI) DeleteMemoryRule(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "empty uuid error.",
			"code":  "INVALID_UUID",
		})
		return
	}

	// Step 1: Get memory rule
	memoryRule, err := a.n9eOperator.GetMemoryRuleByUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Rule not found: The specified rule does not exist",
			"code":  "NOT_FOUND",
			"uuid":  uuid,
		})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve rule information",
			"code":  "INTERNAL_ERROR",
			"uuid":  uuid,
		})
		return
	}

	// Step 2: Get business group
	businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), memoryRule.BusinessGroupUUID)
	if err != nil {
		log.Printf("Warning: Failed to get business group %s: %v", memoryRule.BusinessGroupUUID, err)
	}

	// Step 3: Initialize components
	a.InitN9EComponents()

	// Step 4: Get linked VMs and clear anchor thresholds
	vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), uuid)
	linkedVMs := []string{}
	instanceUUIDs := []string{}

	if err == nil && len(vmLinks) > 0 {
		for _, link := range vmLinks {
			if link.RuleType != "memory" {
				continue
			}
			linkedVMs = append(linkedVMs, link.VMUUID)
			instanceUUIDs = append(instanceUUIDs, link.VMUUID)
		}
	}

	// Step 5: Clear anchor thresholds in VictoriaMetrics
	// Note: Region/Domain not stored in DB link; partial clear uses just ruleUUID+instanceID.
	// Remaining anchor data expires via last_over_time retention.
	if len(instanceUUIDs) > 0 && a.anchorManager != nil {
		anchorInstances := make([]routes.AnchorInstance, len(instanceUUIDs))
		for i, vmUUID := range instanceUUIDs {
			anchorInstances[i] = routes.AnchorInstance{
				RuleUUID:   uuid,
				InstanceID: vmUUID,
				Owner:      memoryRule.Owner,
			}
		}
		if err := a.anchorManager.ClearAnchorThresholds(c.Request.Context(), "mem", anchorInstances); err != nil {
			log.Printf("Warning: Failed to clear memory anchor thresholds: %v", err)
		}
	}

	// Step 6: Delete VM links from database
	for _, link := range vmLinks {
		if _, err := a.n9eOperator.DeleteVMRuleLink(c.Request.Context(), uuid, link.VMUUID, link.Interface); err != nil {
			log.Printf("Warning: Failed to delete VM rule link: %v", err)
		}
	}

	// Step 6.5: Delete N9E alert rule if exists
	if memoryRule.N9EAlertRuleID > 0 && a.n9eClient != nil && businessGroup != nil {
		n9eBGID, bgErr := a.n9eClient.GetBusinessGroupByName(c.Request.Context(), businessGroup.Name)
		if bgErr != nil {
			log.Printf("Warning: Failed to find N9E business group '%s' for alert rule deletion: %v", businessGroup.Name, bgErr)
		} else {
			if delErr := a.n9eClient.DeleteAlertRule(c.Request.Context(), n9eBGID, memoryRule.N9EAlertRuleID); delErr != nil {
				log.Printf("Warning: Failed to delete N9E alert rule ID %d: %v", memoryRule.N9EAlertRuleID, delErr)
			} else {
				log.Printf("[N9E] Deleted alert rule ID %d from BG %d", memoryRule.N9EAlertRuleID, n9eBGID)
			}
		}
	}

	// Step 7: Delete memory rule from local database
	if err := a.n9eOperator.DeleteMemoryRule(c.Request.Context(), uuid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}

	// Step 8: Check if business group should be auto-deleted
	if businessGroup != nil {
		count, err := a.n9eOperator.CountRulesByBusinessGroupUUID(c.Request.Context(), businessGroup.UUID)
		if err != nil {
			log.Printf("Warning: Failed to count rules for business group %s: %v", businessGroup.UUID, err)
		} else if count == 0 {
			// No more rules, delete business group from local DB
			log.Printf("Auto-deleting business group %s (no remaining rules)", businessGroup.UUID)
			if err := a.n9eOperator.DeleteBusinessGroup(c.Request.Context(), businessGroup.UUID); err != nil {
				log.Printf("Warning: Failed to delete business group from DB %s: %v", businessGroup.UUID, err)
			}
			// Also delete from N9E API side
			if a.n9eClient != nil {
				n9eBGID, bgErr := a.n9eClient.GetBusinessGroupByName(c.Request.Context(), businessGroup.Name)
				if bgErr != nil {
					log.Printf("Warning: Failed to find N9E business group '%s': %v", businessGroup.Name, bgErr)
				} else {
					if delErr := a.n9eClient.DeleteBusinessGroupByID(c.Request.Context(), n9eBGID); delErr != nil {
						log.Printf("Warning: Failed to delete N9E business group ID %d: %v", n9eBGID, delErr)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"rule_id":     uuid,
			"linked_vms":  linkedVMs,
			"unbound_vms": len(instanceUUIDs),
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
								logger.Errorf("Domain conversion failed domain=%s error=%v", domain, err)
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
			logger.Errorf("Unexpected data format: %T", data)
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
		logger.Infof("[GetHistoryAlarm] error Prometheus resp status: %s (StatusCode: %d)\n", resp.Status, resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse response"})
		return
	}
	processed := make([]gin.H, 0)
	for _, result := range promResp.Data.Result {
		instanceUUID, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), result.Metric.Domain)
		if err != nil {
			logger.Errorf("Domain to UUID convert failed : domain=%s error=%v", result.Metric.Domain, err)
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
			Status      string            `json:"status"`
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
		logger.Errorf("Alert webhook parsing failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid warning msg format"})
		return
	}

	status := notification.Status
	logger.Infof("Processing %d alert(s) with status: %s", len(notification.Alerts), status)
	logger.Debugf("Notification Alerts: %+v", notification.Alerts)

	for _, alert := range notification.Alerts {
		if alert.Status != "firing" {
			logger.Debugf("Alert status is %s, skipping Switch API request for IP: %s", alert.Status, alert.Labels["ip"])
			continue
		}
		alert_type := alert.Labels["alert_type"]

		// Handle IP Block alerts - Send to Switch API
		if alert_type == "ip_blocked" && SwitchAPIEndpoint != "" {
			ip := alert.Labels["ip"]
			summary := alert.Annotations["summary"]

			// Construct request body for Switch API
			reqBody := map[string]interface{}{
				"mode":       "simple",
				"ip_address": ip,
				"house":      SwitchAPIHouse,
				"reason":     "Block IP for detect attack",
				"comments":   summary,
			}

			// Send request asynchronously
			logger.Debugf("Switch API Request Body: %+v", reqBody)
			if routes.IsIPWhitelisted(ip) {
				logger.Infof("IP %s is in whitelist, skipping Switch API request", ip)
				continue
			}
			go a.sendSwitchAPIRequest(reqBody)
			logger.Infof("Triggered Switch API request for IP Block: %s", ip)
		} else {
			logger.Infof("Ignored alert (not ip_blocked or Switch API not configured): type=%s", alert_type)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "processed",
		"alerts":  len(notification.Alerts),
		"message": "alarm process completed",
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
			Limit     int    `json:"limit" binding:"required,min=1,max=100"`
			Duration  int    `json:"duration" binding:"required,min=1"`
			Operator  string `json:"operator"` // gt/lt/gte/lte/eq (default: gt)
		} `json:"rules" binding:"required,min=1,max=2"`
		LinkedVMs []struct {
			InstanceID   string `json:"instance_id" binding:"required"`
			TargetDevice string `json:"target_device" binding:"required"`
		} `json:"linkedvms"`
		DurationMinutes int `json:"duration_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate RuleID is in UUID format (ensures uniqueness)
	if req.RuleID == "" || !isValidUUID(req.RuleID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id must be a valid UUID format"})
		return
	}

	// Validate: 1 or 2 rules allowed (one per direction), no duplicate directions
	if len(req.Rules) < 1 || len(req.Rules) > 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rules must contain 1 or 2 entries (one per direction: in/out)"})
		return
	}
	if len(req.Rules) == 2 && req.Rules[0].Direction == req.Rules[1].Direction {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duplicate direction: each direction (in/out) can only appear once"})
		return
	}

	if _, err := a.n9eOperator.GetBandwidthRuleByUUID(c.Request.Context(), req.RuleID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bandwidth rule already exists with rule_id: " + req.RuleID})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query existing bandwidth rule: " + err.Error()})
		return
	}

	// Step 1: Create N9E Business Group (or get existing one)
	regionName, err := normalizeBusinessGroupName(viper.GetString("console.host"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to normalize business group name: " + err.Error()})
		return
	}
	businessGroup, err := a.n9eOperator.GetBusinessGroupByName(c.Request.Context(), regionName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query business group: " + err.Error()})
		return
	}

	// If business group doesn't exist, create it
	if businessGroup == nil {
		businessGroup = &model.N9EBusinessGroup{
			Name:     regionName,
			Owner:    req.Owner,
			RegionID: req.RegionID,
			Level:    req.Level,
			Enabled:  req.Enable,
		}
		if err := a.n9eOperator.CreateBusinessGroup(c.Request.Context(), businessGroup); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create business group: " + err.Error()})
			return
		}
	}

	// Step 2-4: Initialize N9E components, then loop over each direction rule
	a.InitN9EComponents()

	var createdRuleUUIDs []string

	for _, rule := range req.Rules {
		// Step 2: Create N9E Bandwidth Rule record per direction
		bwOperator := resolveOperator(rule.Operator)
		bwRule := &model.N9EBandwidthRule{
			RuleID:            req.RuleID,
			BusinessGroupUUID: businessGroup.UUID,
			Name:              rule.Name,
			Owner:             req.Owner,
			Direction:         rule.Direction,
			Duration:          rule.Duration,
			DurationMinutes:   req.DurationMinutes,
			Operator:          bwOperator,
			Enabled:           req.Enable,
		}

		if err := a.n9eOperator.CreateBandwidthRule(c.Request.Context(), bwRule); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create bandwidth rule (" + rule.Direction + "): " + err.Error()})
			return
		}
		createdRuleUUIDs = append(createdRuleUUIDs, bwRule.RuleID)

		// Step 3: Render direction-specific PromQL template
		// "bandwidth-in" uses N9E-bandwidth-in-promql.j2 (hardcoded receive / bw_in)
		// "bandwidth-out" uses N9E-bandwidth-out-promql.j2 (hardcoded transmit / bw_out)
		var ruleType string
		if rule.Direction == "out" {
			ruleType = "bandwidth-out"
		} else {
			ruleType = "bandwidth-in"
		}

		templateParams := map[string]interface{}{
			"duration": bwRule.Duration,
			"operator": bwOperator,
		}
		promql, templateErr := a.templateRenderer.RenderN9EPromQL(ruleType, bwRule.RuleID, templateParams)
		if templateErr != nil {
			log.Printf("Warning: Failed to render bandwidth PromQL template (%s) for rule %s: %v", ruleType, bwRule.RuleID, templateErr)
			continue
		}

		// Step 4: Create N9E alert rule for this direction
		if a.n9eClient != nil && promql != "" {
			n9eGroupID, bgErr := a.n9eClient.GetOrCreateBusinessGroup(c.Request.Context(), regionName)
			if bgErr != nil {
				log.Printf("Warning: Failed to get/create N9E business group for region %s: %v", regionName, bgErr)
				continue
			}
			datasourceName := viper.GetString("n9e.n9e_data_source")
			datasourceID, dsErr := a.n9eClient.GetDataSourceByName(c.Request.Context(), datasourceName)
			if dsErr != nil {
				log.Printf("Warning: Failed to get N9E datasource %s: %v", datasourceName, dsErr)
			}
			if n9eGroupID > 0 && datasourceID > 0 {
				n9eRule := routes.N9EAlertRule{
					GroupID:          n9eGroupID,
					RuleName:         bwRule.Name,
					Severity:         getSeverityLevel(req.Level),
					Disabled:         0,
					DatasourceIDs:    []int64{datasourceID},
					PromForDuration:  req.DurationMinutes * 60,
					PromEvalInterval: 60,
					NotifyRepeatStep: 60,
					RuleConfig: routes.N9ERuleConfig{
						Queries: []routes.N9EQuery{
							{PromQL: promql, Severity: getSeverityLevel(req.Level)},
						},
					},
				}
				n9eAlertRuleID, ruleErr := a.n9eClient.CreateAlertRule(c.Request.Context(), n9eRule)
				if ruleErr != nil {
					log.Printf("Warning: Failed to create N9E alert rule for bandwidth rule %s: %v", bwRule.RuleID, ruleErr)
				} else if n9eAlertRuleID > 0 {
					if err := a.n9eOperator.UpdateBandwidthRuleN9EID(c.Request.Context(), bwRule.RuleID, n9eAlertRuleID); err != nil {
						log.Printf("Warning: Failed to store N9E alert rule ID for bandwidth rule %s: %v", bwRule.RuleID, err)
					}
				}
			}
		}

		// Step 5: Create VM rule links for this direction rule
		// Note: anchor threshold metrics (vm_bw_in/out_anchor) must be written via LinkVMsToRule,
		// since region/domain info is required for correct label matching in PromQL join.
		if len(req.LinkedVMs) > 0 {
			for _, vmLink := range req.LinkedVMs {
				link := &model.N9EVMRuleLink{
					RuleType:          "bandwidth",
					RuleUUID:          bwRule.RuleID,
					BusinessGroupUUID: businessGroup.UUID,
					VMUUID:            vmLink.InstanceID,
					Interface:         vmLink.TargetDevice,
					Owner:             req.Owner,
				}
				if err := a.n9eOperator.CreateVMRuleLink(c.Request.Context(), link); err != nil {
					log.Printf("Warning: Failed to create VM rule link: %v", err)
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"business_group_uuid": businessGroup.UUID,
			"rule_id":             req.RuleID,
			"enabled":             req.Enable,
			"linkedvms":           req.LinkedVMs,
		},
	})
}

func (a *AlarmAPI) GetBWRules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	uuid := c.Param("uuid")
	owner := c.Query("owner")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}

	// If uuid is provided, query single rule
	if uuid != "" {
		bwRule, err := a.n9eOperator.GetBandwidthRuleByUUID(c.Request.Context(), uuid)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Bandwidth rule not found"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query bandwidth rule: " + err.Error()})
			return
		}

		// Get business group
		businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), bwRule.BusinessGroupUUID)
		if err != nil {
			log.Printf("Warning: Failed to get business group for bandwidth rule %s: %v", uuid, err)
		}

		// Get linked VMs
		linkedVMs := make([]gin.H, 0)
		vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), bwRule.RuleID)
		if err == nil {
			for _, link := range vmLinks {
				if link.RuleType == "bandwidth" {
					linkedVMs = append(linkedVMs, gin.H{
						"instance_id":   link.VMUUID,
						"target_device": link.Interface,
					})
				}
			}
		}

		responseData := gin.H{
			"rule_id":           bwRule.RuleID,
			"n9e_alert_rule_id": bwRule.N9EAlertRuleID,
			"name":              bwRule.Name,
			"owner":             bwRule.Owner,
			"direction":         bwRule.Direction,
			"duration":          bwRule.Duration,
			"linkedvms":         linkedVMs,
			"enabled":           bwRule.Enabled,
			"region_id":         businessGroup.RegionID,
			"level":             businessGroup.Level,
		}

		c.JSON(http.StatusOK, gin.H{
			"data": []gin.H{responseData},
			"meta": gin.H{
				"total":        1,
				"current_page": 1,
				"per_page":     1,
				"total_pages":  1,
			},
		})
		return
	}

	// List rules with pagination
	queryParams := routes.ListN9ERulesParams{
		Page:     page,
		PageSize: pageSize,
		Owner:    owner,
	}
	queryParams.SetDefaults()

	rules, total, err := a.n9eOperator.ListBandwidthRules(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query bandwidth rules: " + err.Error()})
		return
	}

	responseData := make([]gin.H, 0, len(rules))
	for _, rule := range rules {
		// Get business group
		businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), rule.BusinessGroupUUID)
		if err != nil {
			log.Printf("Warning: Failed to get business group for bandwidth rule %s: %v", rule.UUID, err)
		}

		var regionID, level, businessGroupName string
		if businessGroup != nil {
			regionID = businessGroup.RegionID
			level = businessGroup.Level
			businessGroupName = businessGroup.Name
		}

		// Get linked VMs
		linkedVMs := make([]gin.H, 0)
		vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), rule.RuleID)
		if err == nil {
			for _, link := range vmLinks {
				if link.RuleType == "bandwidth" {
					linkedVMs = append(linkedVMs, gin.H{
						"instance_id":   link.VMUUID,
						"target_device": link.Interface,
					})
				}
			}
		}

		responseData = append(responseData, gin.H{
			"rule_id":             rule.RuleID,
			"n9e_alert_rule_id":   rule.N9EAlertRuleID,
			"name":                rule.Name,
			"owner":               rule.Owner,
			"direction":           rule.Direction,
			"duration":            rule.Duration,
			"linkedvms":           linkedVMs,
			"enabled":             rule.Enabled,
			"business_group_uuid": rule.BusinessGroupUUID,
			"business_group_name": businessGroupName,
			"region_id":           regionID,
			"level":               level,
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
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "empty uuid error.",
			"code":  "INVALID_UUID",
		})
		return
	}

	// Step 1: Get bandwidth rule
	bwRule, err := a.n9eOperator.GetBandwidthRuleByUUID(c.Request.Context(), uuid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Rule not found: The specified rule does not exist",
			"code":  "NOT_FOUND",
			"uuid":  uuid,
		})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve rule information",
			"code":  "INTERNAL_ERROR",
			"uuid":  uuid,
		})
		return
	}

	// Step 2: Get business group
	businessGroup, err := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), bwRule.BusinessGroupUUID)
	if err != nil {
		log.Printf("Warning: Failed to get business group %s: %v", bwRule.BusinessGroupUUID, err)
	}

	// Step 3: Initialize components
	a.InitN9EComponents()

	// Determine anchor type based on bandwidth direction
	bwAnchorType := "bw_in"
	if bwRule.Direction == "out" {
		bwAnchorType = "bw_out"
	}

	// Step 4: Get linked VMs and clear anchor thresholds
	vmLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), uuid)
	linkedVMs := []string{}
	instanceUUIDs := []string{}

	if err == nil && len(vmLinks) > 0 {
		for _, link := range vmLinks {
			if link.RuleType != "bandwidth" {
				continue
			}
			linkedVMs = append(linkedVMs, link.VMUUID)
			instanceUUIDs = append(instanceUUIDs, link.VMUUID)
		}
	}

	// Step 5: Clear anchor thresholds in VictoriaMetrics
	// Note: Region/Domain not stored in DB link; partial clear uses just ruleUUID+instanceID.
	// Remaining anchor data expires via last_over_time retention.
	if len(instanceUUIDs) > 0 && a.anchorManager != nil {
		anchorInstances := make([]routes.AnchorInstance, len(instanceUUIDs))
		for i, vmUUID := range instanceUUIDs {
			anchorInstances[i] = routes.AnchorInstance{
				RuleUUID:   uuid,
				InstanceID: vmUUID,
				Owner:      bwRule.Owner,
			}
		}
		if err := a.anchorManager.ClearAnchorThresholds(c.Request.Context(), bwAnchorType, anchorInstances); err != nil {
			log.Printf("Warning: Failed to clear bandwidth anchor thresholds: %v", err)
		}
	}

	// Step 6: Delete VM links from database
	for _, link := range vmLinks {
		if _, err := a.n9eOperator.DeleteVMRuleLink(c.Request.Context(), uuid, link.VMUUID, link.Interface); err != nil {
			log.Printf("Warning: Failed to delete VM rule link: %v", err)
		}
	}

	// Step 6.5: Delete N9E alert rule if exists
	if bwRule.N9EAlertRuleID > 0 && a.n9eClient != nil && businessGroup != nil {
		n9eBGID, bgErr := a.n9eClient.GetBusinessGroupByName(c.Request.Context(), businessGroup.Name)
		if bgErr != nil {
			log.Printf("Warning: Failed to find N9E business group '%s' for alert rule deletion: %v", businessGroup.Name, bgErr)
		} else {
			if delErr := a.n9eClient.DeleteAlertRule(c.Request.Context(), n9eBGID, bwRule.N9EAlertRuleID); delErr != nil {
				log.Printf("Warning: Failed to delete N9E alert rule ID %d: %v", bwRule.N9EAlertRuleID, delErr)
			} else {
				log.Printf("[N9E] Deleted alert rule ID %d from BG %d", bwRule.N9EAlertRuleID, n9eBGID)
			}
		}
	}

	// Step 7: Delete bandwidth rule
	if err := a.n9eOperator.DeleteBandwidthRule(c.Request.Context(), uuid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "DB operation failed: " + err.Error(),
			"code":  "DB_DELETE_FAILED",
		})
		return
	}

	// Step 8: Check if business group should be auto-deleted
	if businessGroup != nil {
		count, err := a.n9eOperator.CountRulesByBusinessGroupUUID(c.Request.Context(), businessGroup.UUID)
		if err != nil {
			log.Printf("Warning: Failed to count rules for business group %s: %v", businessGroup.UUID, err)
		} else if count == 0 {
			// No more rules, delete business group from local DB
			log.Printf("Auto-deleting business group %s (no remaining rules)", businessGroup.UUID)
			if err := a.n9eOperator.DeleteBusinessGroup(c.Request.Context(), businessGroup.UUID); err != nil {
				log.Printf("Warning: Failed to delete business group from DB %s: %v", businessGroup.UUID, err)
			}
			// Also delete from N9E API side
			if a.n9eClient != nil {
				n9eBGID, bgErr := a.n9eClient.GetBusinessGroupByName(c.Request.Context(), businessGroup.Name)
				if bgErr != nil {
					log.Printf("Warning: Failed to find N9E business group '%s': %v", businessGroup.Name, bgErr)
				} else {
					if delErr := a.n9eClient.DeleteBusinessGroupByID(c.Request.Context(), n9eBGID); delErr != nil {
						log.Printf("Warning: Failed to delete N9E business group ID %d: %v", n9eBGID, delErr)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"rule_id":     uuid,
			"linked_vms":  linkedVMs,
			"unbound_vms": len(instanceUUIDs),
		},
	})
}

// GetN9EAlertRule retrieves alert rule directly from N9E by rule ID
// GET /api/v1/metrics/alarm/n9e/rule/:rule_id
func (a *AlarmAPI) GetN9EAlertRule(c *gin.Context) {
	ruleIDParam := c.Param("rule_id")
	if ruleIDParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id is required"})
		return
	}

	ruleID, err := strconv.ParseInt(ruleIDParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rule_id format"})
		return
	}

	// Initialize N9E components
	a.InitN9EComponents()

	if a.n9eClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "N9E client not initialized"})
		return
	}

	// Get alert rule from N9E (returns raw response with dat/err structure)
	result, err := a.n9eClient.GetAlertRule(c.Request.Context(), ruleID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "Rule not found in N9E",
				"rule_id": ruleID,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get alert rule from N9E",
			"details": err.Error(),
		})
		return
	}

	// Return the full N9E response (includes dat and err fields)
	c.JSON(http.StatusOK, result)
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
		logger.Errorf("Failed to get node alarm rules: error=%v", err)
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
			logger.Errorf("Failed to get linked VMs for %s group %s: %v", ruleType, group.UUID, err)
			continue
		}

		for _, link := range vmLinks {
			domain, err := routes.GetDomainByInstanceUUID(ctx, link.VMUUID)
			if err != nil {
				logger.Errorf("Failed to get domain for instance %s: %v", link.VMUUID, err)
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
				"targets": []string{"localhost:9109"},
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
	logger.Infof("Starting full synchronization of VM rule mappings")
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
				logger.Errorf("Failed to get %s rule groups: %v", cfg.name, err)
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
				logger.Errorf("Failed to get %s rule groups: %v", cfg.name, err)
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
		logger.Errorf("Failed to marshal matched_vms.json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "Failed to marshal mapping data"})
		return
	}

	if err := routes.WriteFile("/etc/prometheus/lists/matched_vms.json", mappingData, 0644); err != nil {
		logger.Errorf("Failed to write matched_vms.json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "Failed to write mapping file"})
		return
	}

	// Reload Prometheus
	if err := routes.ReloadPrometheusViaHTTP(); err != nil {
		logger.Errorf("Warning: Failed to reload Prometheus: %v", err)
		c.JSON(http.StatusOK, gin.H{"status": "partial_success", "message": "Mappings synchronized but failed to reload Prometheus", "count": len(allMappings), "stats": stats})
		return
	}

	logger.Infof("Successfully synchronized VM mappings: total=%d, stats=%+v", len(allMappings), stats)
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "VM rule mappings synchronized successfully", "count": len(allMappings), "stats": stats})
}

// VMAlarmMapping is used for serialization to vm_alarm_mapping.yml
type VMAlarmMapping struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}

// ToggleRuleStatus - Toggle rule status (enable/disable) by managing Prometheus symlinks
// ruleType: "alarm" or "adjust"
// action: "enable" or "disable"
func (a *AlarmAPI) ToggleRuleStatus(ruleType, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Step 1: Validate parameters
		uuid := c.Param("id")
		if uuid == "" {
			uuid = c.Param("uuid")
		}
		if uuid == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error",
				"error":  "ID or UUID is required",
			})
			return
		}

		if action != "enable" && action != "disable" {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error",
				"error":  "Invalid action, must be 'enable' or 'disable'",
			})
			return
		}

		// Step 2: Get rule group information based on ruleType
		var groupUUID, groupType, groupOwner string
		var groupEnabled bool

		if ruleType == "adjust" {
			// Query adjust_rule_group table using AdjustOperator
			adjustOperator := &routes.AdjustOperator{}
			adjustGroup, err := adjustOperator.GetAdjustRulesByIdentifier(c.Request.Context(), uuid)
			if err != nil {
				logger.Errorf("[%s-%s-ERROR] Adjust rule not found: %s, error=%v", strings.ToUpper(ruleType), strings.ToUpper(action), uuid, err)
				c.JSON(http.StatusNotFound, gin.H{
					"status": "error",
					"error":  "Adjust rule group not found",
				})
				return
			}
			groupUUID = adjustGroup.UUID
			groupType = adjustGroup.Type
			groupOwner = adjustGroup.Owner
			groupEnabled = adjustGroup.Enabled
		} else {
			// Query rule_group_v2 table using AlarmOperator (original logic)
			// Try as rule_id first, fallback to group_uuid if not found
			group, err := a.operator.GetRulesByRuleID(c.Request.Context(), uuid)
			if err != nil {
				// If not found by rule_id, try as group_uuid
				group, err = a.operator.GetRulesByGroupUUID(c.Request.Context(), uuid)
				if err != nil {
					logger.Errorf("[%s-%s-ERROR] Alarm rule not found: %s, error=%v", strings.ToUpper(ruleType), strings.ToUpper(action), uuid, err)
					c.JSON(http.StatusNotFound, gin.H{
						"status": "error",
						"error":  "Alarm rule group not found",
					})
					return
				}
			}
			groupUUID = group.UUID
			groupType = group.Type
			groupOwner = group.Owner
			groupEnabled = group.Enabled
		}

		// Step 3: Check current status to prevent duplicate operations
		isEnable := (action == "enable")
		if isEnable && groupEnabled {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error",
				"error":  "Rule is already enabled",
			})
			return
		}
		if !isEnable && !groupEnabled {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error",
				"error":  "Rule is already disabled",
			})
			return
		}

		// Step 4: Determine rule source (alarm or adjust) and build file paths
		var ruleSource string
		type FilePair struct {
			source string
			link   string
		}
		var filePaths []FilePair

		// Determine if this is an adjust rule by checking the rule type
		if strings.Contains(groupType, "adjust") {
			ruleSource = "adjust"
		} else {
			ruleSource = "alarm"
		}

		// Step 5: Build file paths based on rule source
		if ruleSource == "alarm" {
			// Alarm rules: handle different rule types
			switch groupType {
			case routes.RuleTypeCPU: // "cpu"
				// Format: cpu-{owner}-{group_uuid}.yml (matches CreateCPURule)
				rulePath := fmt.Sprintf("%s/cpu-%s-%s.yml", routes.RulesGeneral, groupOwner, groupUUID)
				ruleLinkPath := fmt.Sprintf("%s/cpu-%s-%s.yml", routes.RulesEnabled, groupOwner, groupUUID)
				filePaths = append(filePaths, FilePair{source: rulePath, link: ruleLinkPath})
			case routes.RuleTypeMemory: // "memory"
				// Format: memory-{owner}-{group_uuid}.yml (matches CreateMemoryRule)
				rulePath := fmt.Sprintf("%s/memory-%s-%s.yml", routes.RulesGeneral, groupOwner, groupUUID)
				ruleLinkPath := fmt.Sprintf("%s/memory-%s-%s.yml", routes.RulesEnabled, groupOwner, groupUUID)
				filePaths = append(filePaths, FilePair{source: rulePath, link: ruleLinkPath})
			case routes.RuleTypeBW: // "bw"
				// Format: bw-in-{owner}-{group_uuid}.yml and bw-out-{owner}-{group_uuid}.yml (matches CreateBWRule)
				// Need to query BWRuleDetail to get all directions
				details, err := a.operator.GetBWRuleDetails(c.Request.Context(), groupUUID)
				if err != nil {
					logger.Errorf("[BW-ERROR] Failed to get BW rule details: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{
						"status": "error",
						"error":  "Failed to get BW rule details: " + err.Error(),
					})
					return
				}
				if len(details) == 0 {
					c.JSON(http.StatusBadRequest, gin.H{
						"status": "error",
						"error":  "No BW rule details found",
					})
					return
				}
				// Generate file paths for each direction
				for _, detail := range details {
					var filename string
					switch detail.Direction {
					case "in":
						filename = fmt.Sprintf("bw-in-%s-%s.yml", groupOwner, groupUUID)
					case "out":
						filename = fmt.Sprintf("bw-out-%s-%s.yml", groupOwner, groupUUID)
					default:
						logger.Errorf("[BW-WARNING] Unknown direction: %s, skipping", detail.Direction)
						continue
					}
					rulePath := fmt.Sprintf("%s/%s", routes.RulesGeneral, filename)
					ruleLinkPath := fmt.Sprintf("%s/%s", routes.RulesEnabled, filename)
					filePaths = append(filePaths, FilePair{source: rulePath, link: ruleLinkPath})
				}
			default:
				c.JSON(http.StatusBadRequest, gin.H{
					"status": "error",
					"error":  fmt.Sprintf("Unsupported alarm rule type: %s", groupType),
				})
				return
			}
		} else {
			// Adjust rules: 2 files
			var ruleTypePrefix string
			switch groupType {
			case model.RuleTypeAdjustCPU: // "adjust_cpu"
				ruleTypePrefix = "cpu-adjust"
			case model.RuleTypeAdjustInBW: // "adjust_in_bw"
				ruleTypePrefix = "bw-in-adjust"
			case model.RuleTypeAdjustOutBW: // "adjust_out_bw"
				ruleTypePrefix = "bw-out-adjust"
			default:
				c.JSON(http.StatusBadRequest, gin.H{
					"status": "error",
					"error":  fmt.Sprintf("Unsupported adjust rule type: %s", groupType),
				})
				return
			}

			// File 1: Adjust rule file
			rulePath := fmt.Sprintf("%s/%s-%s-%s.yml", routes.RulesGeneral, ruleTypePrefix, groupOwner, groupUUID)
			ruleLinkPath := fmt.Sprintf("%s/%s-%s-%s.yml", routes.RulesEnabled, ruleTypePrefix, groupOwner, groupUUID)
			filePaths = append(filePaths, FilePair{source: rulePath, link: ruleLinkPath})

			// File 2: Alert file
			alertPath := fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesGeneral, groupOwner, groupUUID)
			alertLinkPath := fmt.Sprintf("%s/resource-adjust-alerts-%s-%s.yml", routes.RulesEnabled, groupOwner, groupUUID)
			filePaths = append(filePaths, FilePair{source: alertPath, link: alertLinkPath})
		}

		// Step 6: Check if source files exist
		for _, fp := range filePaths {
			exists, err := routes.CheckFileExists(fp.source)
			if err != nil {
				logger.Errorf("[%s-%s-ERROR] Failed to check file existence: %s, error: %v",
					strings.ToUpper(ruleType), strings.ToUpper(action), fp.source, err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"status": "error",
					"error":  fmt.Sprintf("Failed to check file existence: %s", filepath.Base(fp.source)),
				})
				return
			}
			if !exists {
				logger.Errorf("[%s-%s-ERROR] Rule file not found: %s",
					strings.ToUpper(ruleType), strings.ToUpper(action), fp.source)
				c.JSON(http.StatusNotFound, gin.H{
					"status": "error",
					"error":  fmt.Sprintf("Rule file not found: %s", filepath.Base(fp.source)),
				})
				return
			}
		}

		// Step 7: Execute enable or disable action
		if isEnable {
			// Enable: Create symlinks
			var createdLinks []string
			for _, fp := range filePaths {
				// Create symlink (CreateSymlink will overwrite if already exists)
				if err := routes.CreateSymlink(fp.source, fp.link); err != nil {
					logger.Errorf("[%s-%s-ERROR] Failed to create symlink: %s -> %s, error: %v",
						strings.ToUpper(ruleType), strings.ToUpper(action), fp.link, fp.source, err)
					// Rollback: Remove already created symlinks
					for _, link := range createdLinks {
						routes.RemoveSymlink(link)
					}
					c.JSON(http.StatusInternalServerError, gin.H{
						"status": "error",
						"error":  fmt.Sprintf("Failed to create symlink: %s", filepath.Base(fp.link)),
					})
					return
				}
				createdLinks = append(createdLinks, fp.link)
				logger.Infof("[%s-%s-INFO] Created symlink: %s -> %s",
					strings.ToUpper(ruleType), strings.ToUpper(action), fp.link, fp.source)
			}
		} else {
			// Disable: Remove symlinks
			var failedLinks []string
			var removedLinks []string

			for _, fp := range filePaths {
				// Check if link exists
				linkExists, err := routes.CheckFileExists(fp.link)
				if err != nil {
					logger.Errorf("[%s-%s-ERROR] Failed to check symlink existence: %s, error: %v",
						strings.ToUpper(ruleType), strings.ToUpper(action), fp.link, err)
					failedLinks = append(failedLinks, fp.link)
					continue
				}

				if !linkExists {
					logger.Debugf("[%s-%s-WARNING] Symlink does not exist (already removed?): %s",
						strings.ToUpper(ruleType), strings.ToUpper(action), fp.link)
					continue
				}

				// Remove symlink
				if err := routes.RemoveSymlink(fp.link); err != nil {
					failedLinks = append(failedLinks, fp.link)
					logger.Errorf("[%s-%s-ERROR] Failed to remove symlink: %s, error: %v",
						strings.ToUpper(ruleType), strings.ToUpper(action), fp.link, err)
				} else {
					removedLinks = append(removedLinks, fp.link)
					logger.Debugf("[%s-%s-INFO] Removed symlink: %s",
						strings.ToUpper(ruleType), strings.ToUpper(action), fp.link)
				}
			}

			// If there are failed removals, return error
			if len(failedLinks) > 0 {
				c.JSON(http.StatusInternalServerError, gin.H{
					"status": "error",
					"error":  fmt.Sprintf("Failed to remove some symlinks: %d failed", len(failedLinks)),
				})
				return
			}

			// If no symlinks were removed, give a warning (but still succeed)
			if len(removedLinks) == 0 {
				logger.Debugf("[%s-%s-WARNING] No symlinks were removed (rule may already be disabled)",
					strings.ToUpper(ruleType), strings.ToUpper(action))
			}
		}

		// Step 8: Reload Prometheus
		logger.Infof("[%s-%s-INFO] Reloading Prometheus configuration", strings.ToUpper(ruleType), strings.ToUpper(action))
		if err := routes.ReloadPrometheusViaHTTP(); err != nil {
			logger.Errorf("[%s-%s-ERROR] Failed to reload Prometheus: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "Failed to reload Prometheus configuration",
			})
			return
		}

		// Step 9: Update database status based on ruleType
		if ruleType == "adjust" {
			// Update adjust_rule_group table
			adjustOperator := &routes.AdjustOperator{}
			if err := adjustOperator.UpdateAdjustRuleGroupStatus(c.Request.Context(), groupUUID, isEnable); err != nil {
				logger.Errorf("[%s-%s-ERROR] Failed to update adjust group status: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"status": "error",
					"error":  "Failed to update adjust rule status in database",
				})
				return
			}
		} else {
			// Update rule_group_v2 table
			if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupUUID, isEnable); err != nil {
				logger.Errorf("[%s-%s-ERROR] Failed to update alarm group status: %v", strings.ToUpper(ruleType), strings.ToUpper(action), err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"status": "error",
					"error":  "Failed to update alarm rule status in database",
				})
				return
			}
		}

		// Step 10: Return success response
		// Collect target file paths
		var targetFiles []string
		for _, fp := range filePaths {
			targetFiles = append(targetFiles, fp.link)
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data": gin.H{
				"group_uuid":   groupUUID,
				"rule_type":    groupType,
				"rule_source":  ruleSource,
				"action":       action,
				"enabled":      isEnable,
				"owner":        groupOwner,
				"target_files": targetFiles,
			},
		})
	}
}

// BatchGetRulesRequest 批量获取规则请求
type BatchGetRulesRequest struct {
	Identifiers      []string `json:"identifiers" binding:"required"`
	IncludeDetails   *bool    `json:"include_details"`
	IncludeLinkedVMs *bool    `json:"include_linked_vms"`
}

// BatchGetRulesResponse 批量获取规则响应
type BatchGetRulesResponse struct {
	Success     bool     `json:"success"`
	Total       int      `json:"total"`
	Found       int      `json:"found"`
	NotFound    int      `json:"not_found"`
	Rules       []gin.H  `json:"rules"`
	NotFoundIDs []string `json:"not_found_ids"`
}

// BatchGetRules 批量获取规则信息 (支持告警和调整规则)
func (a *AlarmAPI) BatchGetRules(c *gin.Context) {
	var req BatchGetRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// 默认值处理
	includeDetails := true
	includeLinkedVMs := true
	if req.IncludeDetails != nil {
		includeDetails = *req.IncludeDetails
	}
	if req.IncludeLinkedVMs != nil {
		includeLinkedVMs = *req.IncludeLinkedVMs
	}

	ctx := c.Request.Context()
	rules := make([]gin.H, 0)
	notFoundIDs := make([]string, 0)

	// 遍历每个标识符
	for _, identifier := range req.Identifiers {
		ruleData, err := a.getSingleRuleByIdentifier(ctx, identifier, includeDetails, includeLinkedVMs)
		if err != nil {
			logger.Errorf("[BatchGetRules] Failed to get rule: identifier=%s, error=%v", identifier, err)
			notFoundIDs = append(notFoundIDs, identifier)
			continue
		}
		rules = append(rules, ruleData)
	}

	// 构建响应
	// 将长结果精简为仅返回 rule_id、rule_source（可选）和按 VM 去重后的 linked_vms 数量
	compactRules := make([]gin.H, 0, len(rules))
	for _, r := range rules {
		ruleID := ""
		if v, ok := r["rule_id"]; ok && v != nil {
			ruleID = fmt.Sprint(v)
		}
		ruleSource := ""
		if v, ok := r["rule_source"]; ok && v != nil {
			ruleSource = fmt.Sprint(v)
		}

		// 统计按 VM 去重的数量：同一 VM 多个网口仅计 1
		linkedVMsCount := 0
		if includeLinkedVMs {
			if arr, ok := r["linked_vms"]; ok && arr != nil {
				if vmList, ok := arr.([]gin.H); ok {
					seen := make(map[string]struct{}, len(vmList))
					for _, vm := range vmList {
						instanceID := ""
						if v, ok := vm["instance_id"]; ok && v != nil {
							instanceID = fmt.Sprint(v)
						}
						if instanceID == "" {
							continue
						}
						if _, exists := seen[instanceID]; !exists {
							seen[instanceID] = struct{}{}
						}
					}
					linkedVMsCount = len(seen)
				}
			}
		}

		item := gin.H{
			"rule_id":    ruleID,
			"linked_vms": linkedVMsCount,
		}
		if ruleSource != "" {
			item["rule_source"] = ruleSource
		}
		compactRules = append(compactRules, item)
	}

	response := BatchGetRulesResponse{
		Success:     true,
		Total:       len(req.Identifiers),
		Found:       len(compactRules),
		NotFound:    len(notFoundIDs),
		Rules:       compactRules,
		NotFoundIDs: notFoundIDs,
	}

	c.JSON(http.StatusOK, response)
}

func (a *AlarmAPI) updateComputeMonitorStatus(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Target string `json:"target"`
			IP     string `json:"ip"`
			Host   string `json:"host"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		input := strings.TrimSpace(req.Target)
		if input == "" {
			input = strings.TrimSpace(req.IP)
		}
		if input == "" {
			input = strings.TrimSpace(req.Host)
		}
		if input == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "target is required"})
			return
		}

		var (
			hyper *model.Hyper
			err   error
		)
		if net.ParseIP(input) != nil {
			hyper, err = hyperAdmin.GetHyperByHostIP(c.Request.Context(), input)
			if err != nil {
				logger.Errorf("Failed to query hypervisor by ip %s: %v", input, err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "hypervisor not found"})
				return
			}
		} else {
			hyper, err = hyperAdmin.GetHyperByHostname(c.Request.Context(), input)
			if err != nil {
				logger.Errorf("Failed to query hypervisor by hostname %s: %v", input, err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "hypervisor not found"})
				return
			}
		}

		targetIP := strings.TrimSpace(hyper.HostIP)
		if targetIP == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "hypervisor host ip is empty"})
			return
		}

		if err := routes.UpdateComputeTargetsJSON(c.Request.Context(), hyper.Hostname, targetIP, action); err != nil {
			logger.Errorf("Failed to %s compute monitor target for host %s: %v", action, hyper.Hostname, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to %s compute monitor target", action)})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data": gin.H{
				"hostname": hyper.Hostname,
				"host_ip":  hyper.HostIP,
				"action":   action,
			},
		})
	}
}

func (a *AlarmAPI) EnableComputeMonitor(c *gin.Context) {
	a.updateComputeMonitorStatus("enable")(c)
}

func (a *AlarmAPI) DisableComputeMonitor(c *gin.Context) {
	a.updateComputeMonitorStatus("disable")(c)
}

// getSingleRuleByIdentifier 通过标识符获取单个规则 (支持uuid和rule_id)
func (a *AlarmAPI) getSingleRuleByIdentifier(ctx context.Context, identifier string, includeDetails, includeLinkedVMs bool) (gin.H, error) {
	// 策略1: 尝试从调整规则表查询
	adjustOperator := &routes.AdjustOperator{}
	adjustGroup, err := adjustOperator.GetAdjustRulesByIdentifier(ctx, identifier)
	if err == nil {
		return a.buildAdjustRuleResponse(ctx, adjustGroup, includeDetails, includeLinkedVMs)
	}

	// 策略2: 尝试从告警规则表查询 (支持uuid)
	groups, _, err := a.operator.ListRuleGroups(ctx, routes.ListRuleGroupsParams{
		GroupUUID: identifier,
		PageSize:  1,
	})
	if err == nil && len(groups) > 0 {
		return a.buildAlarmRuleResponse(ctx, &groups[0], includeDetails, includeLinkedVMs)
	}

	// 策略3: 尝试从告警规则表查询 (支持rule_id)
	groups, _, err = a.operator.ListRuleGroups(ctx, routes.ListRuleGroupsParams{
		RuleID:   identifier,
		PageSize: 1,
	})
	if err == nil && len(groups) > 0 {
		return a.buildAlarmRuleResponse(ctx, &groups[0], includeDetails, includeLinkedVMs)
	}

	return nil, fmt.Errorf("rule not found: %s", identifier)
}

// buildAlarmRuleResponse 构建告警规则响应
func (a *AlarmAPI) buildAlarmRuleResponse(ctx context.Context, group *model.RuleGroupV2, includeDetails, includeLinkedVMs bool) (gin.H, error) {
	response := gin.H{
		"identifier":    group.UUID,
		"rule_source":   "alarm",
		"uuid":          group.UUID,
		"rule_id":       group.RuleID,
		"name":          group.Name,
		"type":          group.Type,
		"owner":         group.Owner,
		"enabled":       group.Enabled,
		"trigger_count": group.TriggerCnt,
		"created_at":    group.CreatedAt,
	}

	// 包含详情
	if includeDetails {
		details, err := a.getRuleDetails(ctx, group.UUID, group.Type)
		if err != nil {
			logger.Errorf("[buildAlarmRuleResponse] Failed to get details: uuid=%s, error=%v", group.UUID, err)
			response["rules"] = []gin.H{}
		} else {
			response["rules"] = details
		}
	}

	// 包含关联VM
	if includeLinkedVMs {
		vmLinks, err := a.operator.GetLinkedVMs(ctx, group.UUID)
		if err != nil {
			logger.Errorf("[buildAlarmRuleResponse] Failed to get linked VMs: uuid=%s, error=%v", group.UUID, err)
			response["linked_vms"] = []gin.H{}
		} else {
			linkedVMs := make([]gin.H, 0, len(vmLinks))
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, gin.H{
					"instance_id":   link.VMUUID,
					"target_device": link.Interface,
				})
			}
			response["linked_vms"] = linkedVMs
		}
	}

	return response, nil
}

// getRuleDetails 根据规则类型获取详情
func (a *AlarmAPI) getRuleDetails(ctx context.Context, groupUUID, ruleType string) ([]gin.H, error) {
	switch ruleType {
	case routes.RuleTypeCPU:
		details, err := a.operator.GetCPURuleDetails(ctx, groupUUID)
		if err != nil {
			return nil, err
		}
		result := make([]gin.H, 0, len(details))
		for _, d := range details {
			result = append(result, gin.H{
				"name":          d.Name,
				"rule":          d.Rule,
				"limit":         d.Limit,
				"duration":      d.Duration,
				"over":          d.Over,
				"down_to":       d.DownTo,
				"down_duration": d.DownDuration,
			})
		}
		return result, nil

	case routes.RuleTypeMemory:
		details, err := a.operator.GetMemoryRuleDetails(ctx, groupUUID)
		if err != nil {
			return nil, err
		}
		result := make([]gin.H, 0, len(details))
		for _, d := range details {
			result = append(result, gin.H{
				"name":          d.Name,
				"rule":          d.Rule,
				"limit":         d.Limit,
				"duration":      d.Duration,
				"over":          d.Over,
				"down_to":       d.DownTo,
				"down_duration": d.DownDuration,
			})
		}
		return result, nil

	case routes.RuleTypeBW:
		details, err := a.operator.GetBWRuleDetails(ctx, groupUUID)
		if err != nil {
			return nil, err
		}
		result := make([]gin.H, 0, len(details))
		for _, d := range details {
			result = append(result, gin.H{
				"direction": d.Direction,
				"name":      d.Name,
				"limit":     d.Limit,
				"duration":  d.Duration,
			})
		}
		return result, nil

	default:
		return []gin.H{}, fmt.Errorf("unsupported rule type: %s", ruleType)
	}
}

// buildAdjustRuleResponse 构建调整规则响应
func (a *AlarmAPI) buildAdjustRuleResponse(ctx context.Context, group *model.AdjustRuleGroup, includeDetails, includeLinkedVMs bool) (gin.H, error) {
	adjustOperator := &routes.AdjustOperator{}

	response := gin.H{
		"identifier":  group.UUID,
		"rule_source": "adjust",
		"uuid":        group.UUID,
		"rule_id":     group.RuleID,
		"name":        group.Name,
		"type":        group.Type,
		"owner":       group.Owner,
		"enabled":     group.Enabled,
		"created_at":  group.CreatedAt,
	}

	// 包含详情
	if includeDetails {
		details, err := a.getAdjustRuleDetails(ctx, group.UUID, group.Type, adjustOperator)
		if err != nil {
			logger.Errorf("[buildAdjustRuleResponse] Failed to get details: uuid=%s, error=%v", group.UUID, err)
			response["rules"] = []gin.H{}
		} else {
			response["rules"] = details
		}
	}

	// 包含关联VM
	if includeLinkedVMs {
		vmLinks, err := a.operator.GetLinkedVMs(ctx, group.UUID)
		if err != nil {
			logger.Errorf("[buildAdjustRuleResponse] Failed to get linked VMs: uuid=%s, error=%v", group.UUID, err)
			response["linked_vms"] = []gin.H{}
		} else {
			linkedVMs := make([]gin.H, 0, len(vmLinks))
			for _, link := range vmLinks {
				linkedVMs = append(linkedVMs, gin.H{
					"instance_id":   link.VMUUID,
					"target_device": link.Interface,
				})
			}
			response["linked_vms"] = linkedVMs
		}
	}

	return response, nil
}

// getAdjustRuleDetails 根据调整规则类型获取详情
func (a *AlarmAPI) getAdjustRuleDetails(ctx context.Context, groupUUID, ruleType string, operator *routes.AdjustOperator) ([]gin.H, error) {
	switch ruleType {
	case model.RuleTypeAdjustCPU:
		details, err := operator.GetCPUAdjustRuleDetails(ctx, groupUUID)
		if err != nil {
			return nil, err
		}
		result := make([]gin.H, 0, len(details))
		for _, d := range details {
			result = append(result, gin.H{
				"name":             d.Name,
				"high_threshold":   d.HighThreshold,
				"smooth_window":    d.SmoothWindow,
				"trigger_duration": d.TriggerDuration,
				"limit_duration":   d.LimitDuration,
				"limit_percent":    d.LimitPercent,
			})
		}
		return result, nil

	case model.RuleTypeAdjustInBW, model.RuleTypeAdjustOutBW:
		details, err := operator.GetBWAdjustRuleDetails(ctx, groupUUID)
		if err != nil {
			return nil, err
		}
		result := make([]gin.H, 0, len(details))
		for _, d := range details {
			result = append(result, gin.H{
				"name":             d.Name,
				"high_threshold":   d.HighThresholdPct,
				"smooth_window":    d.SmoothWindow,
				"trigger_duration": d.TriggerDuration,
				"limit_duration":   d.LimitDuration,
				"limit_bandwidth":  d.LimitValuePct,
			})
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported rule type: %s", ruleType)
	}
}

// ============================================
// N9E Anchor Management APIs
// ============================================

// LinkVMsToRule binds VMs to an alert rule by writing vm_rule_anchor metrics
// POST /api/v1/alarm/anchor/link
func (a *AlarmAPI) LinkVMsToRule(c *gin.Context) {
	a.InitN9EComponents()

	if a.anchorManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Anchor manager not configured"})
		return
	}

	var req struct {
		RuleUUID  string  `json:"rule_uuid" binding:"required"`
		RuleType  string  `json:"rule_type" binding:"required,oneof=cpu memory bandwidth"`
		TenantID  string  `json:"tenant_id" binding:"required"` // 调用方传入的租户UUID，写入 anchor label
		RegionID  string  `json:"region_id" binding:"required"` // 业务层面的区域UUID，用于远端推送等业务逻辑
		Threshold float64 `json:"threshold" binding:"required"` // 告警阈值
		VMs       []struct {
			InstanceID string `json:"instance_id" binding:"required"`
			Interface  string `json:"interface"` // Required for bandwidth rules
		} `json:"vms" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get rule to determine owner, business group, threshold, and anchor type
	var owner string
	var businessGroupUUID string
	var anchorType string

	switch req.RuleType {
	case "cpu":
		cpuRule, err := a.n9eOperator.GetCPURuleByUUID(c.Request.Context(), req.RuleUUID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "CPU rule not found"})
			return
		}
		owner = cpuRule.Owner
		businessGroupUUID = cpuRule.BusinessGroupUUID
		anchorType = "cpu"
	case "memory":
		memoryRule, err := a.n9eOperator.GetMemoryRuleByUUID(c.Request.Context(), req.RuleUUID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Memory rule not found"})
			return
		}
		owner = memoryRule.Owner
		businessGroupUUID = memoryRule.BusinessGroupUUID
		anchorType = "mem"
	case "bandwidth":
		bwRule, err := a.n9eOperator.GetBandwidthRuleByUUID(c.Request.Context(), req.RuleUUID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Bandwidth rule not found"})
			return
		}
		owner = bwRule.Owner
		businessGroupUUID = bwRule.BusinessGroupUUID
		if bwRule.Direction == "out" {
			anchorType = "bw_out"
		} else {
			anchorType = "bw_in"
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type"})
		return
	}

	// 带宽规则必须指定网卡
	if req.RuleType == "bandwidth" {
		for idx, vm := range req.VMs {
			if vm.Interface == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("vms[%d]: interface is required for bandwidth rules", idx)})
				return
			}
		}
	}

	// Build typed anchor instances and write thresholds to VictoriaMetrics
	// Labels: rule_uuid, region, domain, instance_id, interface(bandwidth only), owner, tenant_id
	anchorInstances := make([]routes.AnchorInstance, 0, len(req.VMs))
	for i, vm := range req.VMs {
		// Get domain, hypervisor, region from VictoriaMetrics vm_instance_map
		domain, _, region, err := a.anchorManager.GetInstanceMetadata(c.Request.Context(), vm.InstanceID)
		if err != nil {
			// Fallback to database if vm_instance_map not available
			log.Printf("Failed to get metadata from VM for instance %s, falling back to DB: %v", vm.InstanceID, err)
			domain, err = routes.GetDomainByInstanceUUID(c.Request.Context(), vm.InstanceID)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("vms[%d]: failed to get metadata for instance %s: %v", i, vm.InstanceID, err)})
				return
			}
			// When using DB fallback, region must be extracted from instance record
			region = ""
			log.Printf("Warning: using DB fallback without region for instance %s", vm.InstanceID)
		} else {
			log.Printf("Got metadata from VM: instance=%s, domain=%s, region=%s", vm.InstanceID, domain, region)
		}

		anchorInstances = append(anchorInstances, routes.AnchorInstance{
			RuleUUID:   req.RuleUUID,
			Region:     region,
			Domain:     domain,
			InstanceID: vm.InstanceID,
			Interface:  vm.Interface,
			Owner:      owner,
			TenantID:   req.TenantID,
			Threshold:  req.Threshold,
		})
	}

	if err := a.anchorManager.WriteAnchorThresholdBatch(c.Request.Context(), anchorType, anchorInstances); err != nil {
		log.Printf("Failed to write anchor thresholds for rule %s: %v", req.RuleUUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to write anchor thresholds: %v", err)})
		return
	}

	// Create VM rule links in database
	for _, vm := range req.VMs {
		link := &model.N9EVMRuleLink{
			RuleType:          req.RuleType,
			RuleUUID:          req.RuleUUID,
			BusinessGroupUUID: businessGroupUUID,
			VMUUID:            vm.InstanceID,
			Interface:         vm.Interface,
			Owner:             owner,
			Threshold:         req.Threshold,
			TenantID:          req.TenantID,
		}
		if err := a.n9eOperator.CreateVMRuleLink(c.Request.Context(), link); err != nil {
			log.Printf("Warning: Failed to create VM rule link: %v", err)
		}
	}

	log.Printf("Successfully linked %d VMs to rule %s (anchorType=%s, threshold=%g)", len(req.VMs), req.RuleUUID, anchorType, req.Threshold)
	c.JSON(http.StatusOK, gin.H{
		"status":    "success",
		"message":   fmt.Sprintf("Linked %d VMs to rule", len(req.VMs)),
		"rule_uuid": req.RuleUUID,
		"vm_count":  len(req.VMs),
		"region_id": req.RegionID, // 返回业务层面的 region_id
	})
}

// GetRuleLinks queries all VMs bound to a rule from VictoriaMetrics (typed anchors)
// GET /api/v1/alarm/anchor/links
// Query parameters (all optional):
// - rule_uuid: query links for a specific rule
// - rule_type: one of cpu/memory/bandwidth — narrows anchor metric type for rule_uuid queries
// - owner: query all links for a specific owner (when rule_uuid not provided)
func (a *AlarmAPI) GetRuleLinks(c *gin.Context) {
	a.InitN9EComponents()

	if a.anchorManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Anchor manager not configured"})
		return
	}

	ruleUUID := c.Query("rule_uuid")
	owner := c.Query("owner")
	ruleType := c.Query("rule_type") // optional: cpu / memory / bandwidth

	// anchorTypes 派生辅助函数：根据 rule_type query param 返回要查询的 anchorType 列表
	resolveAnchorTypes := func(rt string) []string {
		switch rt {
		case "cpu":
			return []string{"cpu"}
		case "memory":
			return []string{"mem"}
		case "bandwidth":
			return []string{"bw_in", "bw_out"}
		default:
			// 未指定则查全部 4 种
			return []string{"cpu", "mem", "bw_in", "bw_out"}
		}
	}

	// anchorResult → gin.H 转换
	anchorToGinH := func(vm routes.AnchorResult) gin.H {
		return gin.H{
			"region":      vm.Region,
			"domain":      vm.Domain,
			"instance_id": vm.InstanceID,
			"interface":   vm.Interface, // 带宽 anchor 有此字段，CPU/内存为空字符串
			"owner":       vm.Owner,
			"tenant_id":   vm.TenantID,
			"threshold":   vm.Threshold,
		}
	}

	var result gin.H

	if ruleUUID != "" {
		// 按 rule_uuid 查询（遍历所有相关 anchorType 并合并）
		anchorTypes := resolveAnchorTypes(ruleType)
		allVMs := make([]routes.AnchorResult, 0)
		for _, at := range anchorTypes {
			vms, queryErr := a.anchorManager.QueryTypedAnchorLinksByRule(c.Request.Context(), at, ruleUUID)
			if queryErr != nil {
				log.Printf("Failed to query typed anchor links for rule %s anchorType=%s: %v", ruleUUID, at, queryErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to query links: %v", queryErr)})
				return
			}
			allVMs = append(allVMs, vms...)
		}

		vmList := make([]gin.H, len(allVMs))
		for i, vm := range allVMs {
			vmList[i] = anchorToGinH(vm)
		}

		result = gin.H{
			"status":    "success",
			"rule_uuid": ruleUUID,
			"vm_count":  len(allVMs),
			"vms":       vmList,
		}
	} else if owner != "" {
		// 按 owner 查询（遍历所有相关 anchorType 并合并到 ruleMap）
		anchorTypes := resolveAnchorTypes(ruleType)
		ruleMap := make(map[string][]routes.AnchorResult)
		for _, at := range anchorTypes {
			partialMap, queryErr := a.anchorManager.QueryTypedAnchorLinksByOwner(c.Request.Context(), at, owner)
			if queryErr != nil {
				log.Printf("Failed to query typed anchor links for owner %s anchorType=%s: %v", owner, at, queryErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to query links: %v", queryErr)})
				return
			}
			for rUUID, vms := range partialMap {
				ruleMap[rUUID] = append(ruleMap[rUUID], vms...)
			}
		}

		rules := make([]gin.H, 0, len(ruleMap))
		totalVMs := 0
		for rUUID, vms := range ruleMap {
			vmList := make([]gin.H, len(vms))
			for i, vm := range vms {
				vmList[i] = anchorToGinH(vm)
			}
			rules = append(rules, gin.H{
				"rule_uuid": rUUID,
				"vm_count":  len(vms),
				"vms":       vmList,
			})
			totalVMs += len(vms)
		}

		result = gin.H{
			"status":     "success",
			"owner":      owner,
			"rule_count": len(ruleMap),
			"total_vms":  totalVMs,
			"rules":      rules,
		}
	} else {
		// 查询全部（遍历 4 种 anchorType）
		ruleMap, queryErr := a.anchorManager.QueryAllTypedAnchorLinks(c.Request.Context())
		if queryErr != nil {
			log.Printf("Failed to query all typed anchor links: %v", queryErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to query links: %v", queryErr)})
			return
		}

		rules := make([]gin.H, 0, len(ruleMap))
		totalVMs := 0
		for rUUID, vms := range ruleMap {
			vmList := make([]gin.H, len(vms))
			for i, vm := range vms {
				vmList[i] = anchorToGinH(vm)
			}
			rules = append(rules, gin.H{
				"rule_uuid": rUUID,
				"vm_count":  len(vms),
				"vms":       vmList,
			})
			totalVMs += len(vms)
		}

		result = gin.H{
			"status":     "success",
			"rule_count": len(ruleMap),
			"total_vms":  totalVMs,
			"rules":      rules,
		}
	}

	c.JSON(http.StatusOK, result)
}

// GetDBLinks queries VM links from CloudLand database
// GET /api/v1/alarm/anchor/dblinks?rule_id=<rule_id>&owner=<owner>
// Parameters are optional:
// - rule_id: query links for a specific rule (by rule_id, not UUID)
// - owner: query all links for a specific owner
// - neither: query all links
func (a *AlarmAPI) GetDBLinks(c *gin.Context) {
	ruleUUID := c.Query("rule_uuid")
	owner := c.Query("owner")
	ruleType := c.Query("rule_type") // cpu, memory, bandwidth - optional filter

	// Query database using N9E operator
	var allLinks []model.N9EVMRuleLink
	var err error

	if ruleUUID != "" {
		// Query by specific rule UUID
		allLinks, err = a.n9eOperator.GetVMRuleLinks(c.Request.Context(), ruleUUID)
		if err != nil {
			log.Printf("Failed to query DB links: ruleUUID=%s, error=%v", ruleUUID, err)
		}

		// Filter by rule type if specified
		if ruleType != "" && err == nil {
			filtered := []model.N9EVMRuleLink{}
			for _, link := range allLinks {
				if link.RuleType == ruleType {
					filtered = append(filtered, link)
				}
			}
			allLinks = filtered
		}
	} else if owner != "" {
		// Query by owner
		allLinks, err = a.n9eOperator.GetVMRuleLinksByOwner(c.Request.Context(), owner)
	} else {
		// Query all (need to be careful with large datasets)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Must provide rule_uuid or owner parameter"})
		return
	}

	if err != nil {
		log.Printf("Failed to query DB links: ruleUUID=%s, owner=%s, error=%v", ruleUUID, owner, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to query database: %v", err)})
		return
	}

	// Group links by rule UUID
	linkMap := make(map[string][]model.N9EVMRuleLink)
	for _, link := range allLinks {
		linkMap[link.RuleUUID] = append(linkMap[link.RuleUUID], link)
	}

	// Convert to response format
	rules := make([]gin.H, 0, len(linkMap))
	totalVMs := 0

	for rUUID, links := range linkMap {
		if len(links) == 0 {
			continue
		}

		// Get rule info based on rule type
		firstLink := links[0]
		var ruleName string
		var ruleOwner string
		var businessGroupName string

		switch firstLink.RuleType {
		case "cpu":
			cpuRule, err := a.n9eOperator.GetCPURuleByUUID(c.Request.Context(), rUUID)
			if err == nil {
				ruleName = cpuRule.Name
				ruleOwner = cpuRule.Owner
				bg, _ := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), cpuRule.BusinessGroupUUID)
				if bg != nil {
					businessGroupName = bg.Name
				}
			}
		case "memory":
			memoryRule, err := a.n9eOperator.GetMemoryRuleByUUID(c.Request.Context(), rUUID)
			if err == nil {
				ruleName = memoryRule.Name
				ruleOwner = memoryRule.Owner
				bg, _ := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), memoryRule.BusinessGroupUUID)
				if bg != nil {
					businessGroupName = bg.Name
				}
			}
		case "bandwidth":
			bwRule, err := a.n9eOperator.GetBandwidthRuleByUUID(c.Request.Context(), rUUID)
			if err == nil {
				ruleName = bwRule.Name
				ruleOwner = bwRule.Owner
				bg, _ := a.n9eOperator.GetBusinessGroupByUUID(c.Request.Context(), bwRule.BusinessGroupUUID)
				if bg != nil {
					businessGroupName = bg.Name
				}
			}
		}

		vmList := make([]gin.H, len(links))
		for i, link := range links {
			vmList[i] = gin.H{
				"vm_uuid":   link.VMUUID,
				"interface": link.Interface,
			}
		}

		rules = append(rules, gin.H{
			"rule_uuid":           rUUID,
			"rule_type":           firstLink.RuleType,
			"rule_name":           ruleName,
			"owner":               ruleOwner,
			"business_group_uuid": firstLink.BusinessGroupUUID,
			"business_group_name": businessGroupName,
			"vm_count":            len(links),
			"vms":                 vmList,
		})
		totalVMs += len(links)
	}

	result := gin.H{
		"status":     "success",
		"rule_count": len(rules),
		"total_vms":  totalVMs,
		"rules":      rules,
	}

	// Add filter info to response
	if ruleUUID != "" {
		result["filter_rule_uuid"] = ruleUUID
	}
	if owner != "" {
		result["filter_owner"] = owner
	}
	if ruleType != "" {
		result["filter_rule_type"] = ruleType
	}

	c.JSON(http.StatusOK, result)
}

// UnlinkVMsFromRule unbinds VMs from a rule by writing vm_rule_anchor metrics with value=0
// DELETE /api/v1/alarm/anchor/unlink
// 调用方只需提供 rule_uuid + rule_type + vms[]{instance_id}，
// region/domain/owner/tenant_id 等 label 从 VictoriaMetrics 已有 anchor 中自动读取，保证精确匹配。
func (a *AlarmAPI) UnlinkVMsFromRule(c *gin.Context) {
	a.InitN9EComponents()

	if a.anchorManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Anchor manager not configured"})
		return
	}

	var req struct {
		RuleUUID string `json:"rule_uuid" binding:"required"`
		RuleType string `json:"rule_type" binding:"required,oneof=cpu memory bandwidth"`
		VMs      []struct {
			InstanceID string `json:"instance_id" binding:"required"`
			Interface  string `json:"interface"` // 带宽规则必填，用于定位具体网卡的 anchor series
		} `json:"vms" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Determine anchor type from rule type; also validate bandwidth interface requirement
	var anchorType string

	switch req.RuleType {
	case "cpu":
		anchorType = "cpu"
	case "memory":
		anchorType = "mem"
	case "bandwidth":
		bwRule, err := a.n9eOperator.GetBandwidthRuleByUUID(c.Request.Context(), req.RuleUUID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Bandwidth rule not found"})
			return
		}
		if bwRule.Direction == "out" {
			anchorType = "bw_out"
		} else {
			anchorType = "bw_in"
		}
		// 带宽规则必须指定网卡
		for idx, vm := range req.VMs {
			if vm.Interface == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("vms[%d]: interface is required for bandwidth rules", idx)})
				return
			}
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rule type"})
		return
	}

	// Delete anchor series from VictoriaMetrics using delete_series API.
	// This physically removes the series so no stale data remains.
	// For bandwidth: vm.Interface is required (validated above), targets exactly one NIC.
	// For cpu/memory: vm.Interface is "", deletes the single series for that instance+rule.
	for i, vm := range req.VMs {
		if err := a.anchorManager.DeleteAnchorSeries(c.Request.Context(), anchorType, req.RuleUUID, vm.InstanceID, vm.Interface); err != nil {
			log.Printf("Warning: vms[%d] failed to delete anchor series for instance %s: %v", i, vm.InstanceID, err)
			// 删除失败不阻断流程，继续清理 DB 记录
		}
	}

	// Delete VM rule links from database
	existingLinks, err := a.n9eOperator.GetVMRuleLinks(c.Request.Context(), req.RuleUUID)
	if err == nil {
		for _, link := range existingLinks {
			for _, vm := range req.VMs {
				if link.VMUUID == vm.InstanceID {
					if _, err := a.n9eOperator.DeleteVMRuleLink(c.Request.Context(), req.RuleUUID, link.VMUUID, link.Interface); err != nil {
						log.Printf("Warning: Failed to delete VM rule link: %v", err)
					}
					break
				}
			}
		}
	}

	log.Printf("Successfully unlinked %d VMs from rule %s (anchorType=%s)", len(req.VMs), req.RuleUUID, anchorType)
	c.JSON(http.StatusOK, gin.H{
		"status":    "success",
		"message":   fmt.Sprintf("Unlinked %d VMs from rule", len(req.VMs)),
		"rule_uuid": req.RuleUUID,
		"vm_count":  len(req.VMs),
	})
}

// SyncAnchorThresholds refreshes stale anchor series in VictoriaMetrics.
// POST /api/v1/alarm/anchor/sync
// Finds anchor series active in [14d] but not refreshed in [7d], and re-imports them
// with the current timestamp. Safe to call repeatedly; idempotent.
func (a *AlarmAPI) SyncAnchorThresholds(c *gin.Context) {
	a.InitN9EComponents()

	if a.anchorManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Anchor manager not configured"})
		return
	}

	startTime := time.Now()

	// Step 1: query stale anchors across all 4 types
	staleAnchors, queryErr := a.anchorManager.QueryStaleAnchors(c.Request.Context())

	staleByType := make(map[string]int)
	totalStale := 0
	for anchorType, results := range staleAnchors {
		staleByType[anchorType] = len(results)
		totalStale += len(results)
	}

	if totalStale == 0 {
		warning := ""
		if queryErr != nil {
			warning = queryErr.Error()
		}
		c.JSON(http.StatusOK, gin.H{
			"status":        "ok",
			"message":       "No stale anchors found, nothing to sync.",
			"stale_by_type": staleByType,
			"total_stale":   0,
			"elapsed_ms":    time.Since(startTime).Milliseconds(),
			"query_warning": warning,
		})
		return
	}

	log.Printf("SyncAnchorThresholds: found total_stale=%d by_type=%v", totalStale, staleByType)

	// Step 2: re-import in batches of 500, 50ms sleep between batches
	refreshed, syncErr := a.anchorManager.SyncStaleAnchorsBatch(c.Request.Context(), staleAnchors, 500, 50)

	totalRefreshed := 0
	for _, count := range refreshed {
		totalRefreshed += count
	}

	respBody := gin.H{
		"status":          "ok",
		"stale_by_type":   staleByType,
		"refreshed":       refreshed,
		"total_stale":     totalStale,
		"total_refreshed": totalRefreshed,
		"elapsed_ms":      time.Since(startTime).Milliseconds(),
	}
	if queryErr != nil {
		respBody["query_warning"] = queryErr.Error()
	}
	if syncErr != nil {
		respBody["status"] = "partial"
		respBody["sync_error"] = syncErr.Error()
		log.Printf("SyncAnchorThresholds: partial error: %v", syncErr)
		c.JSON(http.StatusPartialContent, respBody)
		return
	}

	log.Printf("SyncAnchorThresholds: done total_stale=%d total_refreshed=%d elapsed=%s",
		totalStale, totalRefreshed, time.Since(startTime))
	c.JSON(http.StatusOK, respBody)
}

// RecoverAnchorThresholds rebuilds VictoriaMetrics anchor metrics from the database.
// This is a disaster-recovery endpoint for use when VM anchor data has been lost.
// It reads all N9EVMRuleLink rows (which store threshold + tenant_id since the last LinkVMsToRule call),
// fetches live domain/region from VictoriaMetrics vm_instance_map (with DB fallback), and
// re-imports all anchor metrics in batches.
//
// POST /api/v1/metrics/alarm/anchor/recover
func (a *AlarmAPI) RecoverAnchorThresholds(c *gin.Context) {
	a.InitN9EComponents()

	if a.anchorManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Anchor manager not configured"})
		return
	}

	ctx := c.Request.Context()
	startTime := time.Now()

	// Step 1: query all VM rule links from database
	allLinks, err := a.n9eOperator.GetAllVMRuleLinks(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to query VM rule links: %v", err)})
		return
	}
	if len(allLinks) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"message":    "No VM rule links found in database, nothing to recover.",
			"elapsed_ms": time.Since(startTime).Milliseconds(),
		})
		return
	}

	// Cache bandwidth rule directions to avoid repeated DB lookups
	bwDirectionCache := make(map[string]string) // rule_uuid → "in" or "out"

	// Collect AnchorInstance lists per anchor type
	anchorBatches := make(map[string][]routes.AnchorInstance)

	skipped := 0
	for _, link := range allLinks {
		if link.Threshold <= 0 {
			// Legacy links that were created before Threshold was stored; skip them
			log.Printf("[RecoverAnchor] Skipping link rule=%s vm=%s: threshold=0 (legacy record)", link.RuleUUID, link.VMUUID)
			skipped++
			continue
		}

		// Determine anchor type
		var anchorType string
		switch link.RuleType {
		case "cpu":
			anchorType = "cpu"
		case "memory":
			anchorType = "mem"
		case "bandwidth":
			direction, ok := bwDirectionCache[link.RuleUUID]
			if !ok {
				bwRule, bwErr := a.n9eOperator.GetBandwidthRuleByUUID(ctx, link.RuleUUID)
				if bwErr != nil {
					log.Printf("[RecoverAnchor] Cannot get bandwidth rule %s: %v, skipping link", link.RuleUUID, bwErr)
					skipped++
					continue
				}
				direction = bwRule.Direction
				bwDirectionCache[link.RuleUUID] = direction
			}
			if direction == "out" {
				anchorType = "bw_out"
			} else {
				anchorType = "bw_in"
			}
		default:
			log.Printf("[RecoverAnchor] Unknown rule type %q for link rule=%s, skipping", link.RuleType, link.RuleUUID)
			skipped++
			continue
		}

		// Resolve domain/region from VictoriaMetrics vm_instance_map; fallback to DB
		domain, _, region, metaErr := a.anchorManager.GetInstanceMetadata(ctx, link.VMUUID)
		if metaErr != nil {
			log.Printf("[RecoverAnchor] VM metadata unavailable for %s (%v); trying DB fallback", link.VMUUID, metaErr)
			domain, metaErr = routes.GetDomainByInstanceUUID(ctx, link.VMUUID)
			if metaErr != nil {
				log.Printf("[RecoverAnchor] DB fallback also failed for %s (%v); recovering with empty domain/region", link.VMUUID, metaErr)
				domain = ""
			}
			region = ""
		}

		anchorBatches[anchorType] = append(anchorBatches[anchorType], routes.AnchorInstance{
			RuleUUID:   link.RuleUUID,
			Region:     region,
			Domain:     domain,
			InstanceID: link.VMUUID,
			Interface:  link.Interface,
			Owner:      link.Owner,
			TenantID:   link.TenantID,
			Threshold:  link.Threshold,
		})
	}

	// Step 2: write each anchor type batch to VictoriaMetrics
	recovered := make(map[string]int)
	var writeErrors []string
	for anchorType, instances := range anchorBatches {
		if wErr := a.anchorManager.WriteAnchorThresholdBatch(ctx, anchorType, instances); wErr != nil {
			log.Printf("[RecoverAnchor] Failed to write %s anchors: %v", anchorType, wErr)
			writeErrors = append(writeErrors, fmt.Sprintf("%s: %v", anchorType, wErr))
		} else {
			recovered[anchorType] = len(instances)
			log.Printf("[RecoverAnchor] Recovered %d %s anchor(s)", len(instances), anchorType)
		}
	}

	totalRecovered := 0
	for _, cnt := range recovered {
		totalRecovered += cnt
	}

	resp := gin.H{
		"status":          "ok",
		"total_links":     len(allLinks),
		"skipped":         skipped,
		"total_recovered": totalRecovered,
		"recovered":       recovered,
		"elapsed_ms":      time.Since(startTime).Milliseconds(),
	}
	if len(writeErrors) > 0 {
		resp["status"] = "partial"
		resp["write_errors"] = writeErrors
		log.Printf("[RecoverAnchor] Partial recovery: %v", writeErrors)
		c.JSON(http.StatusPartialContent, resp)
		return
	}

	log.Printf("[RecoverAnchor] Done: total_links=%d skipped=%d total_recovered=%d elapsed=%s",
		len(allLinks), skipped, totalRecovered, time.Since(startTime))
	c.JSON(http.StatusOK, resp)
}

func (a *AlarmAPI) sendSwitchAPIRequest(data map[string]interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("Failed to marshal switch api request: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(SwitchAPIEndpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		logger.Errorf("Failed to send request to Switch API: %v", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	logger.Infof("Switch API response status: %s, body: %s", resp.Status, string(body))
}
