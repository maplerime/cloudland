package routes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"web/src/common"
	"web/src/dbs"
	"web/src/model"
)

// findInterfaceByTargetDevice finds the corresponding interface by target_device
// target_device format: tapXXXXXX (tap + last 6 digits of MAC without colons)
// Example: tapdb4c44 corresponds to MAC 52:54:21:db:4c:44
func findInterfaceByTargetDevice(instance *model.Instance, targetDevice string) *model.Interface {
	if !strings.HasPrefix(targetDevice, "tap") || len(targetDevice) != 9 {
		log.Printf("Invalid target_device format: %s, expected tapXXXXXX", targetDevice)
		return nil
	}

	// Extract last 6 digits of MAC address
	macSuffix := targetDevice[3:]

	for _, iface := range instance.Interfaces {
		if iface.MacAddr == "" {
			continue
		}

		// Extract last 6 digits of MAC (remove colons)
		macParts := strings.Split(iface.MacAddr, ":")
		if len(macParts) >= 3 {
			// Take last 3 parts, remove colons
			lastThreeParts := strings.Join(macParts[len(macParts)-3:], "")
			if strings.EqualFold(lastThreeParts, macSuffix) {
				log.Printf("Found matching interface: MAC=%s, targetDevice=%s", iface.MacAddr, targetDevice)
				return iface
			}
		}
	}

	log.Printf("No matching interface found for target_device: %s", targetDevice)
	return nil
}

// getAdminPassword reads admin password from config file
func getAdminPassword() string {
	viper.SetConfigFile("conf/config.toml")
	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Failed to read config file, using default password: %v", err)
		return "passw0rd"
	}

	password := viper.GetString("admin.password")
	if password == "" {
		password = "passw0rd"
	}
	return password
}

// CreateAdminContext creates admin context for webhook requests
// This function solves the problem of webhook requests not passing through auth middleware
func CreateAdminContext(ctx context.Context) (context.Context, error) {
	// Get admin password from config file
	adminPassword := getAdminPassword()

	// Validate admin user and password
	user, err := userAdmin.Validate(ctx, "admin", adminPassword)
	if err != nil {
		log.Printf("Failed to validate admin user: %v", err)
		return ctx, fmt.Errorf("failed to validate admin user: %v", err)
	}

	// Get admin organization
	org, err := orgAdmin.GetOrgByName(ctx, "admin")
	if err != nil {
		log.Printf("Failed to get admin org: %v", err)
		return ctx, fmt.Errorf("failed to get admin org: %v", err)
	}

	// Get membership
	memberShip, err := common.GetDBMemberShip(user.ID, org.ID)
	if err != nil {
		log.Printf("Failed to get admin membership: %v", err)
		return ctx, fmt.Errorf("failed to get admin membership: %v", err)
	}

	// Ensure admin role
	memberShip.Role = model.Admin

	// Set context
	adminCtx := memberShip.SetContext(ctx)

	return adminCtx, nil
}

// GetInstanceByUUIDWithAuth helper function to get instance with admin auth
// This function is specifically for webhook calls
func GetInstanceByUUIDWithAuth(ctx context.Context, instanceID string) (*model.Instance, error) {
	// Create admin context
	adminCtx, err := CreateAdminContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin context: %v", err)
	}

	// Get instance with admin context
	instance, err := instanceAdmin.GetInstanceByUUID(adminCtx, instanceID)
	if err != nil {
		log.Printf("Failed to get instance: %v", err)
		return nil, fmt.Errorf("failed to get instance: %v", err)
	}

	return instance, nil
}

// AlertWebhookRequest Prometheus alert webhook request structure
type AlertWebhookRequest struct {
	Status string        `json:"status"`
	Alerts []AdjustAlert `json:"alerts"`
}

// AdjustAlert Alert information structure
type AdjustAlert struct {
	Status      string            `json:"status"`
	State       string            `json:"state"`
	ActiveAt    time.Time         `json:"activeAt"`
	Value       string            `json:"value"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
}

// AdjustmentRecord Adjustment record
type AdjustmentRecord struct {
	Name          string
	RuleGroupUUID string
	Summary       string
	Description   string
	StartsAt      time.Time
	AdjustType    string
	TargetDevice  string
}

// AdjustOperator Resource auto-adjustment operator
type AdjustOperator struct{}

// NewAdjustOperator creates resource auto-adjustment operator
func NewAdjustOperator() *AdjustOperator {
	return &AdjustOperator{}
}

// ListAdjustRuleGroupsParams parameters for listing resource adjustment rule groups
type ListAdjustRuleGroupsParams struct {
	RuleType   string
	GroupUUID  string
	RuleID     string
	Owner      string
	Page       int
	PageSize   int
	EnabledSQL string
}

// CreateAdjustRuleGroup creates resource adjustment rule group
func (o *AdjustOperator) CreateAdjustRuleGroup(ctx context.Context, group *model.AdjustRuleGroup) error {
	if group.UUID == "" {
		group.UUID = uuid.New().String()
	}

	return dbs.DB().Create(group).Error
}

// GetAdjustRulesByGroupUUID gets resource adjustment rule group by UUID
func (o *AdjustOperator) GetAdjustRulesByGroupUUID(ctx context.Context, uuid string) (*model.AdjustRuleGroup, error) {
	var group model.AdjustRuleGroup
	if err := dbs.DB().Where("uuid = ?", uuid).First(&group).Error; err != nil {
		return nil, err
	}
	return &group, nil
}

// GetAdjustRulesByIdentifier gets resource adjustment rule group by identifier (supports rule_id and group_uuid)
func (o *AdjustOperator) GetAdjustRulesByIdentifier(ctx context.Context, identifier string) (*model.AdjustRuleGroup, error) {
	var group model.AdjustRuleGroup

	// Try querying by rule_id first
	err := dbs.DB().Where("rule_id = ?", identifier).First(&group).Error
	if err == nil {
		return &group, nil
	}

	// If rule_id query fails, query by uuid (backward compatible)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = dbs.DB().Where("uuid = ?", identifier).First(&group).Error
		if err != nil {
			return nil, err
		}
		return &group, nil
	}

	return nil, err
}

// ListAdjustRuleGroups lists resource adjustment rule groups
func (o *AdjustOperator) ListAdjustRuleGroups(ctx context.Context, params ListAdjustRuleGroupsParams) ([]model.AdjustRuleGroup, int64, error) {
	var groups []model.AdjustRuleGroup
	var total int64

	query := dbs.DB().Model(&model.AdjustRuleGroup{})

	// Apply filter conditions
	if params.RuleType != "" {
		query = query.Where("type = ?", params.RuleType)
	}

	// Dual identifier query logic
	if params.RuleID != "" && params.GroupUUID != "" {
		// Both identifiers provided, use OR query
		query = query.Where("rule_id = ? OR uuid = ?", params.RuleID, params.GroupUUID)
	} else if params.RuleID != "" {
		query = query.Where("rule_id = ?", params.RuleID)
	} else if params.GroupUUID != "" {
		query = query.Where("uuid = ?", params.GroupUUID)
	}

	if params.Owner != "" {
		query = query.Where("owner = ?", params.Owner)
	}
	if params.EnabledSQL != "" {
		query = query.Where(params.EnabledSQL)
	}

	// Get total count
	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Apply pagination
	offset := (params.Page - 1) * params.PageSize
	query = query.Offset(offset).Limit(params.PageSize)

	// Sort
	query = query.Order("created_at desc")

	// Execute query
	if err := query.Find(&groups).Error; err != nil {
		return nil, 0, err
	}

	return groups, total, nil
}

// CreateCPUAdjustRuleDetail creates CPU adjustment rule detail
func (o *AdjustOperator) CreateCPUAdjustRuleDetail(ctx context.Context, detail *model.CPUAdjustRuleDetail) error {
	return dbs.DB().Create(detail).Error
}

// GetCPUAdjustRuleDetails gets CPU adjustment rule details
func (o *AdjustOperator) GetCPUAdjustRuleDetails(ctx context.Context, groupUUID string) ([]model.CPUAdjustRuleDetail, error) {
	var details []model.CPUAdjustRuleDetail
	if err := dbs.DB().Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		return nil, err
	}
	return details, nil
}

// CreateBWAdjustRuleDetail creates bandwidth adjustment rule detail
func (o *AdjustOperator) CreateBWAdjustRuleDetail(ctx context.Context, detail *model.BWAdjustRuleDetail) error {
	if detail.UUID == "" {
		detail.UUID = uuid.New().String()
	}
	return dbs.DB().Create(detail).Error
}

// GetBWAdjustRuleDetails gets bandwidth adjustment rule details
func (o *AdjustOperator) GetBWAdjustRuleDetails(ctx context.Context, groupUUID string) ([]model.BWAdjustRuleDetail, error) {
	var details []model.BWAdjustRuleDetail
	if err := dbs.DB().Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		return nil, err
	}
	return details, nil
}

// UpdateAdjustRuleGroupStatus updates adjust rule group enabled status
func (o *AdjustOperator) UpdateAdjustRuleGroupStatus(ctx context.Context, groupUUID string, enabled bool) error {
	result := dbs.DB().Model(&model.AdjustRuleGroup{}).
		Where("uuid = ?", groupUUID).
		Update("enabled", enabled)

	if result.Error != nil {
		log.Printf("update adjust rule group status failed groupUUID %s error %v", groupUUID, result.Error)
		return fmt.Errorf("update adjust rule group status failed: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("adjust rule group not found")
	}

	return nil
}

// DeleteAdjustRuleGroupWithDependencies deletes resource adjustment rule group and its dependencies
func (o *AdjustOperator) DeleteAdjustRuleGroupWithDependencies(ctx context.Context, groupUUID string) error {
	tx := dbs.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Delete CPU adjustment rule details
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.CPUAdjustRuleDetail{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Delete bandwidth adjustment rule details
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.BWAdjustRuleDetail{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Delete VM links
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.VMRuleLink{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Delete adjustment history
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.AdjustmentHistory{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Delete rule group
	if err := tx.Where("uuid = ?", groupUUID).Delete(&model.AdjustRuleGroup{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// RecordAdjustmentHistory records adjustment history
func (o *AdjustOperator) RecordAdjustmentHistory(ctx context.Context, history *model.AdjustmentHistory) error {
	history.AdjustTime = time.Now()
	return dbs.DB().Create(history).Error
}

// IsInCooldown checks if in cooldown period
func (o *AdjustOperator) IsInCooldown(ctx context.Context, domain, ruleID, actionType string, cooldownSeconds int) (bool, error) {
	var history model.AdjustmentHistory
	err := dbs.DB().Where("domain_name = ? AND rule_id = ? AND action_type = ?", domain, ruleID, actionType).
		Order("adjust_time desc").
		First(&history).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	// Check if in cooldown period
	cooldownDuration := time.Duration(cooldownSeconds) * time.Second
	return time.Since(history.AdjustTime) < cooldownDuration, nil
}

// GetAdjustmentHistory gets adjustment history
func (o *AdjustOperator) GetAdjustmentHistory(ctx context.Context, groupUUID string, limit int) ([]model.AdjustmentHistory, error) {
	var history []model.AdjustmentHistory
	query := dbs.DB().Where("group_uuid = ?", groupUUID).Order("adjust_time desc")

	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Find(&history).Error
	return history, err
}

// SaveAdjustmentHistory saves adjustment history
func (o *AdjustOperator) SaveAdjustmentHistory(ctx context.Context, history *model.AdjustmentHistory) error {
	return dbs.DB().Create(history).Error
}

// AdjustCPUResource adjusts CPU resources
func (o *AdjustOperator) AdjustCPUResource(ctx context.Context, record *AdjustmentRecord, domain string, limit bool, instanceID string) error {
	var instance *model.Instance
	var err error

	// Get instance by ID if provided, otherwise query by domain
	if instanceID != "" {
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	} else {
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	}

	// Build command
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	var command string

	if limit {
		// Get limit value from rule group
		details, err := o.GetCPUAdjustRuleDetails(ctx, record.RuleGroupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get CPU adjustment rule details: %v", err)
			return fmt.Errorf("failed to get CPU adjustment rule details: %v", err)
		}

		// Get limit percentage
		limitPercent := details[0].LimitPercent

		command = fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' '%d'",
			domain, limitPercent)
	} else {
		// Restore CPU resources
		command = fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' 'restore'",
			domain)
	}

	// Execute command
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		log.Printf("Failed to adjust CPU: %v", err)
		return fmt.Errorf("failed to adjust CPU resources: %v", err)
	}

	// Update custom metric status
	status := 0
	if limit {
		status = 1
	}
	updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_cpu_adjustment_status.sh --domain '%s' --rule-id '%s' --status %d",
		domain, fmt.Sprintf("%s-%s", domain, record.RuleGroupUUID), status)

	err = common.HyperExecute(ctx, control, updateCommand)
	if err != nil {
		log.Printf("Warning: Failed to update CPU adjustment metric for domain %s: %v", domain, err)
	}

	log.Printf("Successfully adjusted CPU resources: domain=%s, limit=%v", domain, limit)
	return nil
}

// RestoreCPUResource restores CPU resources
func (o *AdjustOperator) RestoreCPUResource(ctx context.Context, record *AdjustmentRecord, domain string, instanceID string) error {

	var instance *model.Instance
	var err error

	// Get instance by ID if provided
	if instanceID != "" {
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	} else {
		// Otherwise query by domain
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	}

	// Build command
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// Restore CPU resources
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' 'restore'",
		domain)

	// Execute command
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		log.Printf("Failed to restore CPU: %v", err)
		return fmt.Errorf("failed to restore CPU resources: %v", err)
	}

	// Update custom metric status
	status := 0
	updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_cpu_adjustment_status.sh --domain '%s' --rule-id '%s' --status %d",
		domain, fmt.Sprintf("%s-%s", domain, record.RuleGroupUUID), status)

	err = common.HyperExecute(ctx, control, updateCommand)
	if err != nil {
		log.Printf("Warning: Failed to update CPU adjustment metric for domain %s: %v", domain, err)
	}

	log.Printf("Successfully restored CPU resources: domain=%s", domain)
	return nil
}

// AdjustBandwidthResource adjusts bandwidth resources
// totalInBw and totalOutBw are the total bandwidth configuration in Mbps (from Prometheus alert value)
func (o *AdjustOperator) AdjustBandwidthResource(ctx context.Context, record *AdjustmentRecord, domain string, device string, limit bool, instanceID string, totalInBw int, totalOutBw int) error {
	var instance *model.Instance
	var err error

	// Get instance by ID if provided
	if instanceID != "" {
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	} else {
		// Otherwise query by domain
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	}

	// Build command
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// Determine target device name
	targetDevice := device
	if targetDevice == "" {
		// Use target_device from record if device not provided
		targetDevice = record.TargetDevice
	}

	if targetDevice == "" {
		log.Printf("Target device not specified for instance %s", instanceID)
		return fmt.Errorf("target device not specified")
	}

	// Use total bandwidth from Prometheus alert value (passed as parameters)
	// This avoids remote calls to read metrics file
	originalInBw := totalInBw
	originalOutBw := totalOutBw

	log.Printf("[BW-ADJUST] Total bandwidth from alert: domain=%s, device=%s, inBw=%d Mbps, outBw=%d Mbps",
		domain, targetDevice, originalInBw, originalOutBw)

	// Set bandwidth limit values
	inBw := originalInBw   // Default: keep original value
	outBw := originalOutBw // Default: keep original value

	// Check if actual bandwidth limiting is needed
	needActualLimit := false
	var bwType string

	if limit {
		// Get bandwidth adjustment rule details to calculate limit values
		details, err := o.GetBWAdjustRuleDetails(ctx, record.RuleGroupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get bandwidth adjustment rule details: %v", err)
			return fmt.Errorf("failed to get bandwidth adjustment rule details: %v", err)
		}

		// Set limit values based on adjustment type
		if record.AdjustType == "limit_in_bw" || record.AdjustType == model.RuleTypeAdjustInBW {
			bwType = "in"
			// Find inbound rule details
			for _, detail := range details {
				if detail.Direction == "in" {
					// Only limit if original inbound bandwidth > 0 (0 means unlimited)
					if originalInBw > 0 {
						needActualLimit = true
						// Calculate limit value from percentage: limitValue = totalBandwidth × limitPct / 100
						inBw = originalInBw * detail.LimitValuePct / 100
						// Use unidirectional setting, don't affect outbound bandwidth
						outBw = 0 // Placeholder, not actually used

						log.Printf("[BW-ADJUST] Calculated inbound limit: %d%% of %d Mbps = %d Mbps",
							detail.LimitValuePct, originalInBw, inBw)
					} else {
						log.Printf("[BW-ADJUST] Skip inbound limit: interface has unlimited bandwidth (0)")
					}
					break
				}
			}
		} else if record.AdjustType == "limit_out_bw" || record.AdjustType == model.RuleTypeAdjustOutBW {
			bwType = "out"
			// Find outbound rule details
			for _, detail := range details {
				if detail.Direction == "out" {
					// Only limit if original outbound bandwidth > 0 (0 means unlimited)
					if originalOutBw > 0 {
						needActualLimit = true
						// Calculate limit value from percentage: limitValue = totalBandwidth × limitPct / 100
						outBw = originalOutBw * detail.LimitValuePct / 100
						// Use unidirectional setting, don't affect inbound bandwidth
						inBw = 0 // Placeholder, not actually used

						log.Printf("[BW-ADJUST] Calculated outbound limit: %d%% of %d Mbps = %d Mbps",
							detail.LimitValuePct, originalOutBw, outBw)
					} else {
						log.Printf("[BW-ADJUST] Skip outbound limit: interface has unlimited bandwidth (0)")
					}
					break
				}
			}
		}
	}

	// Use target_device as nic_name
	nicName := record.TargetDevice
	if nicName == "" {
		// Use device parameter if target_device is empty
		nicName = device
	}

	// Execute bandwidth limit command only if needed
	if needActualLimit {
		// Extract ID from domain (remove inst- prefix)
		// Domain format: "inst-6", need to extract "6" for script
		vmID := domain
		if strings.HasPrefix(domain, "inst-") {
			vmID = strings.TrimPrefix(domain, "inst-")
		}

		var command string
		if bwType == "in" {
			// Limit inbound bandwidth only, use unidirectional mode
			command = fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '%d' '0' --inbound-only",
				vmID, nicName, inBw)
		} else if bwType == "out" {
			// Limit outbound bandwidth only, use unidirectional mode
			command = fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '0' '%d' --outbound-only",
				vmID, nicName, outBw)
		}

		// Execute command
		err = common.HyperExecute(ctx, control, command)
		if err != nil {
			log.Printf("Failed to adjust bandwidth: %v", err)
			return fmt.Errorf("failed to adjust bandwidth resources: %v", err)
		}
	}

	// Update custom metric status after successful bandwidth adjustment
	status := 0
	if limit {
		status = 1 // Limited state
	}

	if bwType != "" {
		// Generate proper rule_id format: adjust-bw-$DOMAIN-$UUID
		ruleID := fmt.Sprintf("adjust-bw-%s-%s", domain, record.RuleGroupUUID)
		updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_bandwidth_adjustment_status.sh --domain '%s' --rule-id '%s' --type '%s' --status %d --target-device '%s'",
			domain, ruleID, bwType, status, nicName)

		err = common.HyperExecute(ctx, control, updateCommand)
		if err != nil {
			log.Printf("Warning: Failed to update bandwidth adjustment metric for domain %s: %v", domain, err)
		}
	}

	log.Printf("Successfully adjusted bandwidth resources: domain=%s, device=%s, nicName=%s, limit=%v, inBw=%d, outBw=%d",
		domain, device, nicName, limit, inBw, outBw)
	return nil
}

// RestoreBandwidthResource restores bandwidth resources
func (o *AdjustOperator) RestoreBandwidthResource(ctx context.Context, record *AdjustmentRecord, domain string, device string, instanceID string) error {
	var instance *model.Instance
	var err error

	// Get instance by ID if provided
	if instanceID != "" {
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	} else {
		// Otherwise query by domain
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance: %v", err)
			return fmt.Errorf("failed to get instance: %v", err)
		}
	}

	// Build command
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// Get target interface's original bandwidth configuration
	var targetInterface *model.Interface
	if device != "" {
		targetInterface = findInterfaceByTargetDevice(instance, device)
	} else {
		// Use target_device from record if device not provided
		targetInterface = findInterfaceByTargetDevice(instance, record.TargetDevice)
	}

	if targetInterface == nil {
		log.Printf("Interface not found: device=%s, instanceID=%s", device, instanceID)
		return fmt.Errorf("interface not found: device=%s", device)
	}

	// Restore to original bandwidth configuration
	originalInBw := int(targetInterface.Inbound)   // Original inbound bandwidth (Mbps)
	originalOutBw := int(targetInterface.Outbound) // Original outbound bandwidth (Mbps)

	log.Printf("Restoring interface bandwidth: name=%s, inBw=%d, outBw=%d",
		targetInterface.Name, originalInBw, originalOutBw)

	// Check if actual bandwidth restoration is needed
	needActualRestore := false
	var bwType string

	// Determine previously limited bandwidth direction based on adjustment type for metric cleanup
	if record.AdjustType == model.RuleTypeAdjustInBW || record.AdjustType == "restore_in_bw" {
		bwType = "in"
	} else if record.AdjustType == model.RuleTypeAdjustOutBW || record.AdjustType == "restore_out_bw" {
		bwType = "out"
	}

	// For restore operation, execute if any original bandwidth limit exists (non-zero value)
	if originalInBw > 0 || originalOutBw > 0 {
		needActualRestore = true
	}

	// Use target_device as nic_name
	nicName := record.TargetDevice
	if nicName == "" {
		// Use device parameter if target_device is empty
		nicName = device
	}

	// Execute bandwidth restore command only if needed
	if needActualRestore {
		// Extract ID from domain (remove inst- prefix)
		// Domain format: "inst-6", need to extract "6" for script
		vmID := domain
		if strings.HasPrefix(domain, "inst-") {
			vmID = strings.TrimPrefix(domain, "inst-")
		}

		// Restore bandwidth to original configuration values
		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '%d' '%d'",
			vmID, nicName, originalInBw, originalOutBw)

		// Execute command
		err = common.HyperExecute(ctx, control, command)
		if err != nil {
			log.Printf("Failed to restore bandwidth: %v", err)
			return fmt.Errorf("failed to restore bandwidth resources: %v", err)
		}
	}

	// Update custom metric status after successful bandwidth restoration (cleanup metric)
	status := 0

	if bwType != "" {
		// Generate proper rule_id format: adjust-bw-$DOMAIN-$UUID
		// Must use exactly the same logic as AdjustBandwidthResource function
		ruleID := fmt.Sprintf("adjust-bw-%s-%s", domain, record.RuleGroupUUID)
		updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_bandwidth_adjustment_status.sh --domain '%s' --rule-id '%s' --type '%s' --status %d --target-device '%s'",
			domain, ruleID, bwType, status, nicName)

		err = common.HyperExecute(ctx, control, updateCommand)
		if err != nil {
			log.Printf("Warning: Failed to update bandwidth adjustment metric for domain %s: %v", domain, err)
		}
	}

	log.Printf("Successfully restored bandwidth resources: domain=%s, device=%s, nicName=%s, originalInBw=%d, originalOutBw=%d",
		domain, device, nicName, originalInBw, originalOutBw)
	return nil
}

// GetAdjustmentCooldownConfig retrieves adjustment cooldown configuration from database
func (o *AdjustOperator) GetAdjustmentCooldownConfig(ctx context.Context, adjustType string, groupUUID string) int {
	// Default cooldown time is 5 minutes (300 seconds)
	defaultCooldown := 300

	// Get corresponding rule configuration based on adjustment type
	switch adjustType {
	case model.RuleTypeAdjustCPU, "limit_cpu", "restore_cpu":
		// Query CPU adjustment rule details
		details, err := o.GetCPUAdjustRuleDetails(ctx, groupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get CPU adjustment rule details: %v", err)
			return defaultCooldown
		}
		// Use limit duration as cooldown period
		return details[0].LimitDuration
	case model.RuleTypeAdjustInBW, model.RuleTypeAdjustOutBW, "limit_in_bw", "restore_in_bw", "limit_out_bw", "restore_out_bw":
		// Query bandwidth adjustment rule details
		details, err := o.GetBWAdjustRuleDetails(ctx, groupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get bandwidth adjustment rule details: %v", err)
			return defaultCooldown
		}
		// Use first rule's limit duration as cooldown period
		return details[0].LimitDuration
	default:
		log.Printf("Unknown adjustment type: %s, using default cooldown", adjustType)
		return defaultCooldown
	}
}

// UpdateVMBandwidthMetric updates bandwidth configuration metric for a single VM interface
func (o *AdjustOperator) UpdateVMBandwidthMetric(ctx context.Context, hyperID int, domain string, targetDevice string, inBw int, outBw int) error {
	control := fmt.Sprintf("inter=%d", hyperID)
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_interface_bandwidth.sh 'add' '%s' '%s' %d %d",
		domain, targetDevice, inBw, outBw)

	err := common.HyperExecute(ctx, control, command)
	if err != nil {
		return fmt.Errorf("failed to update bandwidth metric: %v", err)
	}

	log.Printf("[BW-METRIC] Updated bandwidth metric: hyper=%d, domain=%s, device=%s, in=%d, out=%d",
		hyperID, domain, targetDevice, inBw, outBw)
	return nil
}
