package routes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"
	"web/src/common"
	"web/src/model"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"github.com/unknwon/i18n"

	"github.com/go-macaron/session"
	"github.com/jinzhu/gorm"
	"gopkg.in/macaron.v1"
)

const (
	RuleTypeCPU            = "cpu"
	RuleTypeMemory         = "memory"
	RuleTypeBW             = "bw"
	RuleTypeCompute        = "compute_node"
	RuleTypeControl        = "control_node"
	RuleTypeAvailable      = "node_available"
	RuleTypeHypervisorVCPU = "hypervisor_vcpu"
	RuleTypePacketDrop     = "packet_drop"
	RuleTypeIPBlock        = "ip_block"
	RulesEnabled           = "/etc/prometheus/rules_enabled"
	RulesGeneral           = "/etc/prometheus/general_rules"
	RulesSpecial           = "/etc/prometheus/special_rules"
	RulesNode              = "/etc/prometheus/node_rules"
	RuleTemplate           = "/etc/prometheus/node_templates"
)

var (
	alarmPrometheusIP      string
	alarmPrometheusPort    int
	alarmPrometheusSSHPort int
	isRemotePrometheus     bool
	sshKeyPath             string
	prometheusClient       *PrometheusClient
	alarmAdminInstance     = &AlarmAdmin{}
	alarmView              = &AlarmView{}
)

type PrometheusClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

type RuleFileRequest struct {
	Operation string `json:"operation"`
	FileUser  string `json:"file_user"`
	Content   string `json:"content"`
	FilePath  string `json:"file_path"`
	LinkPath  string `json:"link_path"`
}

type RuleFileResponse struct {
	Success bool   `json:"success"`
	Exists  bool   `json:"exists"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
}

type ListRuleGroupsParams struct {
	RuleType  string
	Page      int
	PageSize  int
	GroupUUID string
	RuleID    string // Added: Support query by rule_id
}

// AdaptiveQueryParams Adaptive query parameters
type AdaptiveQueryParams struct {
	ID       string // Can be GroupUUID or RuleID
	RuleType string
	Page     int
	PageSize int
}

type (
	VMRuleLink struct {
		ID        uint      `gorm:"primaryKey;autoIncrement"`
		GroupUUID string    `gorm:"column:group_uuid;type:varchar(36);index;not null"`
		VMName    string    `gorm:"type:varchar(255);index;not null"`
		CreatedAt time.Time `gorm:"autoCreateTime"`
	}

	RuleGroupV2 struct {
		ID         string    `gorm:"primaryKey;type:varchar(36)"`
		Name       string    `gorm:"index;size:255"`
		Type       string    `gorm:"type:varchar(10);index"` // cpu/bw/memory/disk/network-in/network-out
		Enabled    bool      `gorm:"default:true"`
		Owner      string    `gorm:"type:varchar(255);index"`
		CreatedAt  time.Time `gorm:"autoCreateTime"`
		TriggerCnt int       `gorm:"default:0"`
		UpdatedAt  time.Time
	}

	CPURule struct {
		ID           int       `gorm:"primaryKey;autoIncrement"`
		GroupUUID    string    `gorm:"column:group_uuid;type:varchar(36);index"`
		Name         string    `json:"name" gorm:"size:255"`
		Limit        int       `json:"limit" gorm:"column:limit;check:limit >= 1"` // Threshold value
		Rule         string    `json:"rule" gorm:"type:varchar(8);column:rule"`    // Comparison operator: gt/lt
		Duration     int       `json:"duration" gorm:"check:duration >= 1"`        // Duration in minutes
		Over         int       `json:"over" gorm:"check:over >= 1"`
		DownTo       int       `json:"down_to" gorm:"check:down_to >= 0"`
		DownDuration int       `json:"down_duration" gorm:"check:down_duration >= 1"`
		CreatedAt    time.Time `gorm:"autoCreateTime"`
	}

	MemoryRule struct {
		ID           int       `gorm:"primaryKey;autoIncrement"`
		GroupUUID    string    `gorm:"column:group_uuid;type:varchar(36);index"`
		Name         string    `json:"name" gorm:"size:255"`
		Limit        int       `json:"limit" gorm:"column:limit;check:limit >= 1"` // Threshold value
		Rule         string    `json:"rule" gorm:"type:varchar(8);column:rule"`    // Comparison operator: gt/lt
		Duration     int       `json:"duration" gorm:"check:duration >= 1"`        // Duration in minutes
		Over         int       `json:"over" gorm:"check:over >= 1"`
		DownTo       int       `json:"down_to" gorm:"check:down_to >= 0"`
		DownDuration int       `json:"down_duration" gorm:"check:down_duration >= 1"`
		CreatedAt    time.Time `gorm:"autoCreateTime"`
	}

	BWRule struct {
		ID        uint   `gorm:"primaryKey;autoIncrement"`
		GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index"`
		Name      string `gorm:"size:255"`

		InEnabled      bool   `gorm:"default:false"`
		InThreshold    int    `gorm:"check:in_threshold >= 0"`
		InDuration     int    `gorm:"check:in_duration >= 0"`
		InOverType     string `gorm:"type:varchar(20);default:'absolute'"`
		InDownTo       int    `gorm:"default:0"`
		InDownDuration int    `gorm:"default:0"`

		OutEnabled      bool   `gorm:"default:false"`
		OutThreshold    int    `gorm:"check:out_threshold >= 0"`
		OutDuration     int    `gorm:"check:out_duration >= 0"`
		OutOverType     string `gorm:"type:varchar(20);default:'absolute'"`
		OutDownTo       int    `gorm:"default:0"`
		OutDownDuration int    `gorm:"default:0"`

		CreatedAt time.Time `gorm:"autoCreateTime"`
	}
	Alert struct {
		ID            uint   `gorm:"primaryKey;autoIncrement"`
		Name          string `gorm:"size:255"`
		Status        string `gorm:"type:varchar(20)"`
		RuleGroupUUID string `json:"rule_group"`
		GlobalRuleID  string `gorm:"type:varchar(255)" json:"global_rule_id"` // Added: Global rule ID
		Severity      string `gorm:"type:varchar(20)"`
		Summary       string `gorm:"type:text"`
		Description   string `gorm:"type:text"`
		StartsAt      time.Time
		EndsAt        time.Time
		CreatedAt     time.Time `gorm:"autoCreateTime"`
		AlertType     string    `gorm:"type:varchar(20)" json:"alert_type"`
		TargetDevice  string    `gorm:"type:varchar(255)" json:"target_device"`
		RegionID      string    `gorm:"type:varchar(255)" json:"region_id"`   // Added: Region ID
		InstanceID    string    `gorm:"type:varchar(255)" json:"instance_id"` // Added: Instance ID
	}
)

// NodeAvailabilityConfig Complete configuration structure
type NodeAvailabilityConfig struct {
	NodeDownDuration     string `json:"node_down_duration"`
	AlertDurationMinutes int    `json:"alert_duration_minutes"`
}

type ManagementConfig struct {
	// CPU monitoring
	CPUUsageThreshold int    `json:"cpu_usage_threshold"`
	CPUAlertDuration  string `json:"cpu_alert_duration"`
	CPUAlertMinutes   int    `json:"cpu_alert_minutes"`

	// Memory monitoring
	MemoryUsageThreshold int    `json:"memory_usage_threshold"`
	MemoryAlertDuration  string `json:"memory_alert_duration"`
	MemoryAlertMinutes   int    `json:"memory_alert_minutes"`

	// Disk monitoring
	DiskSpaceThreshold int    `json:"disk_space_threshold"`
	DiskAlertDuration  string `json:"disk_alert_duration"`
	DiskAlertMinutes   int    `json:"disk_alert_minutes"`

	// Network monitoring - Added
	NetworkTrafficThresholdGB float64 `json:"network_traffic_threshold_gb"`
	NetworkAlertDuration      string  `json:"network_alert_duration"`
	NetworkAlertMinutes       int     `json:"network_alert_minutes"`
}

type ComputeConfig struct {
	// CPU monitoring
	CPUUsageThreshold int    `json:"cpu_usage_threshold"`
	CPUAlertDuration  string `json:"cpu_alert_duration"`
	CPUAlertMinutes   int    `json:"cpu_alert_minutes"`

	// Memory monitoring - Added
	MemoryUsageThreshold int    `json:"memory_usage_threshold"`
	MemoryAlertDuration  string `json:"memory_alert_duration"`
	MemoryAlertMinutes   int    `json:"memory_alert_minutes"`

	// Disk monitoring
	DiskSpaceThreshold int    `json:"disk_space_threshold"`
	DiskAlertDuration  string `json:"disk_alert_duration"`
	DiskAlertMinutes   int    `json:"disk_alert_minutes"`

	// Core network monitoring
	NetworkTrafficThresholdGB float64 `json:"network_traffic_threshold_gb"`
	NetworkAlertDuration      string  `json:"network_alert_duration"`
	NetworkAlertMinutes       int     `json:"network_alert_minutes"`

	// Multiple business type network monitoring
	NetworkTypes map[string]NetworkTypeConfig `json:"network_types"`
}

type NetworkTypeConfig struct {
	Threshold float64 `json:"threshold"`
	Pattern   string  `json:"pattern"`
	Duration  string  `json:"duration"`
}

type AlarmOperator struct {
	DB *gorm.DB
}

type AlarmAdmin struct{}
type AlarmView struct{}

func init() {
	viper.SetConfigFile("conf/config.toml")
	if err := viper.ReadInConfig(); err == nil {
		alarmPrometheusIP = viper.GetString("monitor.host")
		alarmPrometheusPort = viper.GetInt("monitor.port")
		alarmPrometheusSSHPort = viper.GetInt("monitor.sshport")
		sshKeyPath = viper.GetString("monitor.sshkey")
	}
	if alarmPrometheusPort == 0 {
		alarmPrometheusPort = 9090
	}
	if alarmPrometheusSSHPort == 0 {
		alarmPrometheusSSHPort = 22
	}
	if sshKeyPath == "" {
		sshKeyPath = "~/workspace/.ssh/cland.key"
	}
	isRemotePrometheus = !isLocalIP(alarmPrometheusIP)
	if !isRemotePrometheus || alarmPrometheusIP == "" {
		alarmPrometheusIP = "localhost"
	}
	if isRemotePrometheus {
		baseURL := fmt.Sprintf("https://%s:%d", alarmPrometheusIP, 8256)
		certFile := "/etc/ssl/certs/alarm_rules_manager.crt"
		client, err := AlertRUleClient(baseURL, certFile, "")
		if err != nil {
			log.Printf("Failed to initialize the Prometheus client.: %v", err)
		} else {
			prometheusClient = client
			log.Printf("Prometheus client initialized successfully with URL: %s", baseURL)
		}
	}
	log.Printf("Prometheus: IP=%s, port=%d, SSHport=%d, remote_mode=%v",
		alarmPrometheusIP, alarmPrometheusPort, alarmPrometheusSSHPort, isRemotePrometheus)
}

func GetPrometheusIP() string {
	return alarmPrometheusIP
}

func GetPrometheusPort() int {
	return alarmPrometheusPort
}

func GetPrometheusSSHPort() int {
	return alarmPrometheusSSHPort
}

func IsRemotePrometheus() bool {
	return isRemotePrometheus
}

func (a *AlarmOperator) GetCPURulesByGroupID(ctx context.Context, groupUUID string, rules *[]model.CPURuleDetail) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Where("group_uuid = ?", groupUUID).Find(rules).Error
}

func (a *AlarmOperator) GetRulesByGroupUUID(ctx context.Context, groupUUID string) (*model.RuleGroupV2, error) {
	ctx, _ = common.GetContextDB(ctx)
	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		GroupUUID: groupUUID,
		PageSize:  1,
	})
	if err != nil {
		log.Printf("rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("rules query failed: %v", err)
	}

	if len(groups) == 0 {
		log.Printf("rule not found: groupID=%s", groupUUID)
		return nil, gorm.ErrRecordNotFound
	}

	ruleType := groups[0].Type

	if ruleType == "cpu" {
		details, err := a.GetCPURuleDetails(ctx, groupUUID)
		if err != nil {
			log.Printf("detail rules query failed: groupID=%s, error=%v", groupUUID, err)
			return nil, fmt.Errorf("detail rules query failed: %w", err)
		}
		type ResultGroup struct {
			model.RuleGroupV2
			Details []model.CPURuleDetail `gorm:"-"`
		}
		result := &ResultGroup{
			RuleGroupV2: groups[0],
			Details:     details,
		}
		return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
	} else if ruleType == "bw" {
		details, err := a.GetBWRuleDetails(ctx, groupUUID)
		if err != nil {
			log.Printf("detail rules query failed: groupID=%s, error=%v", groupUUID, err)
			return nil, fmt.Errorf("detail rules query failed: %w", err)
		}
		type ResultGroup struct {
			model.RuleGroupV2
			Details []model.BWRuleDetail `gorm:"-"`
		}
		result := &ResultGroup{
			RuleGroupV2: groups[0],
			Details:     details,
		}
		return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
	} else {
		log.Printf("unsupported rule type groupID %s type %s", groupUUID, ruleType)
		return nil, fmt.Errorf("unsupported rule type: %s", ruleType)
	}
}

// GetRulesByRuleID 通过 rule_id 获取规则组（支持告警规则）
func (a *AlarmOperator) GetRulesByRuleID(ctx context.Context, ruleID string) (*model.RuleGroupV2, error) {
	ctx, _ = common.GetContextDB(ctx)
	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		RuleID:   ruleID,
		PageSize: 1,
	})
	if err != nil {
		log.Printf("rules query failed: ruleID=%s, error=%v", ruleID, err)
		return nil, fmt.Errorf("rules query failed: %v", err)
	}

	if len(groups) == 0 {
		log.Printf("rule not found: ruleID=%s", ruleID)
		return nil, gorm.ErrRecordNotFound
	}

	ruleType := groups[0].Type

	if ruleType == "cpu" {
		details, err := a.GetCPURuleDetails(ctx, groups[0].UUID)
		if err != nil {
			log.Printf("detail rules query failed: ruleID=%s, error=%v", ruleID, err)
			return nil, fmt.Errorf("detail rules query failed: %w", err)
		}
		type ResultGroup struct {
			model.RuleGroupV2
			Details []model.CPURuleDetail `gorm:"-"`
		}
		result := &ResultGroup{
			RuleGroupV2: groups[0],
			Details:     details,
		}
		return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
	} else if ruleType == "bw" {
		details, err := a.GetBWRuleDetails(ctx, groups[0].UUID)
		if err != nil {
			log.Printf("detail rules query failed: ruleID=%s, error=%v", ruleID, err)
			return nil, fmt.Errorf("detail rules query failed: %w", err)
		}
		type ResultGroup struct {
			model.RuleGroupV2
			Details []model.BWRuleDetail `gorm:"-"`
		}
		result := &ResultGroup{
			RuleGroupV2: groups[0],
			Details:     details,
		}
		return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
	} else if ruleType == "memory" {
		details, err := a.GetMemoryRuleDetails(ctx, groups[0].UUID)
		if err != nil {
			log.Printf("detail rules query failed: ruleID=%s, error=%v", ruleID, err)
			return nil, fmt.Errorf("detail rules query failed: %w", err)
		}
		type ResultGroup struct {
			model.RuleGroupV2
			Details []model.MemoryRuleDetail `gorm:"-"`
		}
		result := &ResultGroup{
			RuleGroupV2: groups[0],
			Details:     details,
		}
		return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
	} else {
		log.Printf("unsupported rule type ruleID %s type %s", ruleID, ruleType)
		return nil, fmt.Errorf("unsupported rule type: %s", ruleType)
	}
}

func (a *AlarmOperator) GetCPURulesByGroupUUID(ctx context.Context, groupUUID string, ruleType string) (*model.RuleGroupV2, error) {

	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		RuleType:  ruleType,
		GroupUUID: groupUUID,
		PageSize:  1,
	})
	if err != nil || len(groups) == 0 {
		log.Printf("rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("rules query failed: %w", err)
	}

	details, err := a.GetCPURuleDetails(ctx, groupUUID)
	if err != nil {
		log.Printf("detail rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("detail rules query failed: %w", err)
	}
	type ResultGroup struct {
		model.RuleGroupV2
		Details []model.CPURuleDetail `gorm:"-"`
	}
	result := &ResultGroup{
		RuleGroupV2: groups[0],
		Details:     details,
	}
	return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
}
func (a *AlarmOperator) GetMemoryRulesByGroupUUID(ctx context.Context, groupUUID string, ruleType string) (*model.RuleGroupV2, error) {
	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		RuleType:  ruleType,
		GroupUUID: groupUUID,
		PageSize:  1,
	})
	if err != nil || len(groups) == 0 {
		log.Printf("rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("rules query failed: %w", err)
	}

	details, err := a.GetMemoryRuleDetails(ctx, groupUUID)
	if err != nil {
		log.Printf("detail rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("detail rules query failed: %w", err)
	}
	type ResultGroup struct {
		model.RuleGroupV2
		Details []model.MemoryRuleDetail `gorm:"-"`
	}
	result := &ResultGroup{
		RuleGroupV2: groups[0],
		Details:     details,
	}
	return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
}

func (a *AlarmOperator) GetBWRulesByGroupUUID(ctx context.Context, groupUUID string, ruleType string) (*model.RuleGroupV2, error) {
	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		RuleType:  ruleType,
		GroupUUID: groupUUID,
		PageSize:  1,
	})
	if err != nil || len(groups) == 0 {
		log.Println("rules query failed:", "groupID", groupUUID, "error", err)
		return nil, fmt.Errorf("rules query failed: %w", err)
	}

	details, err := a.GetBWRuleDetails(ctx, groupUUID)
	if err != nil {
		log.Printf("detail rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("detail rules query failed: %w", err)
	}
	type ResultGroup struct {
		model.RuleGroupV2
		Details []model.BWRuleDetail `gorm:"-"`
	}
	result := &ResultGroup{
		RuleGroupV2: groups[0],
		Details:     details,
	}
	return (*model.RuleGroupV2)(unsafe.Pointer(result)), nil
}

func (a *AlarmOperator) UpdateRuleGroupStatus(ctx context.Context, groupID string, enabled bool) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.RuleGroupV2{}).
			Where("uuid = ?", groupID).
			Update("enabled", enabled)
		if result.Error != nil {
			log.Printf("update satus failed groupID %s error %v", groupID, result.Error)
			return fmt.Errorf("update satus failed: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("group rules no found")
		}
		return nil
	})
}

// CheckVMLinkExists checks if a VM link already exists
func (a *AlarmOperator) CheckVMLinkExists(ctx context.Context, groupUUID, vmUUID, iface string) bool {
	ctx, db := common.GetContextDB(ctx)
	var count int64
	query := db.Model(&model.VMRuleLink{}).
		Where("group_uuid = ? AND vm_uuid = ?", groupUUID, vmUUID)

	if iface != "" {
		query = query.Where("interface = ?", iface)
	}

	query.Count(&count)
	return count > 0
}

// CreateVMLink creates a single VM link
func (a *AlarmOperator) CreateVMLink(ctx context.Context, groupUUID, vmUUID, iface string) error {
	ctx, db := common.GetContextDB(ctx)
	link := &model.VMRuleLink{
		GroupUUID: groupUUID,
		VMUUID:    vmUUID,
		Interface: iface,
	}
	if err := db.Create(link).Error; err != nil {
		log.Printf("create link failed: GroupUUID=%s, vmUUID=%s, interface=%s, error=%v",
			groupUUID, vmUUID, iface, err)
		return fmt.Errorf("create link failed: %w", err)
	}
	return nil
}

func (a *AlarmOperator) BatchLinkVMs(ctx context.Context, GroupUUID string, vmUUIDs []string, iface string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for _, vmUUID := range vmUUIDs {
			var count int64
			tx.Model(&model.VMRuleLink{}).
				Where("group_uuid = ? AND vm_uuid = ? AND interface = ?", GroupUUID, vmUUID, iface).
				Count(&count)

			if count == 0 {
				link := &model.VMRuleLink{
					GroupUUID: GroupUUID,
					VMUUID:    vmUUID,
					Interface: iface,
				}
				if err := tx.Create(link).Error; err != nil {
					log.Printf("create link failed: GroupUUID=%s, vmUUID=%s, interface=%s, error=%v",
						GroupUUID, vmUUID, iface, err)
					return fmt.Errorf("create link failed: %w", err)
				}
			} else {
				log.Printf("link already exists, skipping: GroupUUID=%s, vmUUID=%s, interface=%s",
					GroupUUID, vmUUID, iface)
			}
		}
		return nil
	})
}

func (a *AlarmOperator) DeleteRuleGroup(ctx context.Context, groupUUID, ruleType string) error {
	ctx, db := common.GetContextDB(ctx)
	result := db.Where("uuid = ? AND type = ?", groupUUID, ruleType).
		Delete(&model.RuleGroupV2{})
	if result.Error != nil {
		log.Printf("delete rule failed: groupUUID=%s, type=%s, error=%v",
			groupUUID, ruleType, result.Error)
	}
	return result.Error
}

func (a *AlarmOperator) DeleteVMLink(ctx context.Context, groupUUID, vmUUID, iface string) (int64, error) {
	ctx, db := common.GetContextDB(ctx)
	query := db.Where("group_uuid = ? AND vm_uuid = ?", groupUUID, vmUUID)

	if iface != "" {
		query = query.Where("interface = ?", iface)
	}

	result := query.Delete(&model.VMRuleLink{})
	if result.Error != nil {
		log.Printf("delete link failed groupUUID %s vmUUID %s interface %s error %v", groupUUID, vmUUID, iface, result.Error)
	}
	return result.RowsAffected, result.Error
}

func (a *AlarmOperator) GetLinkedVMs(ctx context.Context, groupUUID string) ([]model.VMRuleLink, error) {
	ctx, db := common.GetContextDB(ctx)
	var links []model.VMRuleLink
	query := db.Model(&model.VMRuleLink{})

	if groupUUID != "" {
		query = query.Where("group_uuid = ?", groupUUID)
	} else {
		log.Printf("query all goup found, TBD")
	}

	if err := query.Find(&links).Error; err != nil {
		log.Printf("get link data failed: groupUUID=%s, error=%v", groupUUID, err)
		return nil, err
	}
	return links, nil
}

// GetRuleIDsByInstance retrieves all rule IDs associated with a single instance
// This includes both alarm rules (rule_group_v2) and adjust rules (adjust_rule_group)
// Input: instanceUUID - single instance UUID to query
// Output: []string - list of rule_id values
func (a *AlarmOperator) GetRuleIDsByInstance(ctx context.Context, instanceUUID string) ([]string, error) {
	if instanceUUID == "" {
		return []string{}, nil
	}

	ctx, db := common.GetContextDB(ctx)
	ruleIDs := make([]string, 0)

	// Query alarm rules from rule_group_v2
	type RuleIDResult struct {
		RuleID string
	}
	var alarmRuleIDs []RuleIDResult
	err := db.Table("vm_rule_links").
		Select("DISTINCT rule_group_v2.rule_id").
		Joins("JOIN rule_group_v2 ON vm_rule_links.group_uuid = rule_group_v2.uuid").
		Where("vm_rule_links.vm_uuid = ? AND vm_rule_links.deleted_at IS NULL", instanceUUID).
		Scan(&alarmRuleIDs).Error

	if err != nil {
		log.Printf("[GetRuleIDsByInstance] Failed to query alarm rules for instance %s: %v", instanceUUID, err)
		return nil, fmt.Errorf("failed to query alarm rules: %w", err)
	}

	for _, r := range alarmRuleIDs {
		ruleIDs = append(ruleIDs, r.RuleID)
	}

	// Query adjust rules from adjust_rule_group
	var adjustRuleIDs []RuleIDResult
	err = db.Table("vm_rule_links").
		Select("DISTINCT adjust_rule_group.rule_id").
		Joins("JOIN adjust_rule_group ON vm_rule_links.group_uuid = adjust_rule_group.uuid").
		Where("vm_rule_links.vm_uuid = ? AND vm_rule_links.deleted_at IS NULL", instanceUUID).
		Scan(&adjustRuleIDs).Error

	if err != nil {
		log.Printf("[GetRuleIDsByInstance] Failed to query adjust rules for instance %s: %v", instanceUUID, err)
		return nil, fmt.Errorf("failed to query adjust rules: %w", err)
	}

	for _, r := range adjustRuleIDs {
		ruleIDs = append(ruleIDs, r.RuleID)
	}

	return ruleIDs, nil
}

// GetCompleteRuleByRuleID retrieves complete rule information by rule_id
// This function automatically identifies if the rule is an alarm rule or adjust rule
// Input: ruleID - rule identifier
// Output: interface{} - either *RuleGroupV2 (alarm) or *AdjustRuleGroup (adjust)
func (a *AlarmOperator) GetCompleteRuleByRuleID(ctx context.Context, ruleID string) (interface{}, error) {
	if ruleID == "" {
		return nil, fmt.Errorf("ruleID cannot be empty")
	}

	// First, try to find in alarm rules (rule_group_v2)
	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		RuleID:   ruleID,
		PageSize: 1,
	})

	if err == nil && len(groups) > 0 {
		// Found in alarm rules, get complete info based on type
		group := groups[0]
		switch group.Type {
		case "cpu":
			return a.GetCPURulesByGroupUUID(ctx, group.UUID, group.Type)
		case "memory":
			return a.GetMemoryRulesByGroupUUID(ctx, group.UUID, group.Type)
		case "bandwidth":
			return a.GetBWRulesByGroupUUID(ctx, group.UUID, group.Type)
		default:
			log.Printf("[GetCompleteRuleByRuleID] Unknown alarm rule type: %s for rule_id: %s", group.Type, ruleID)
			return nil, fmt.Errorf("unknown alarm rule type: %s", group.Type)
		}
	}

	// Not found in alarm rules, try adjust rules
	adjustOperator := &AdjustOperator{}
	adjustGroups, _, err := adjustOperator.ListAdjustRuleGroups(ctx, ListAdjustRuleGroupsParams{
		RuleID:   ruleID,
		PageSize: 1,
	})

	if err == nil && len(adjustGroups) > 0 {
		// Found in adjust rules, get complete info based on type
		group := adjustGroups[0]
		switch group.Type {
		case "adjust_cpu":
			return adjustOperator.GetCPUAdjustRulesByGroupUUID(ctx, group.UUID, group.Type)
		case "adjust_in_bw", "adjust_out_bw":
			return adjustOperator.GetBWAdjustRulesByGroupUUID(ctx, group.UUID, group.Type)
		default:
			log.Printf("[GetCompleteRuleByRuleID] Unknown adjust rule type: %s for rule_id: %s", group.Type, ruleID)
			return nil, fmt.Errorf("unknown adjust rule type: %s", group.Type)
		}
	}

	// Rule not found in either table
	log.Printf("[GetCompleteRuleByRuleID] Rule not found: %s", ruleID)
	return nil, fmt.Errorf("rule not found: %s", ruleID)
}

// GetInstanceRuleLinks retrieves all rule links for specific instances
// Input: instanceUUIDs - list of instance UUIDs to query
// Output: map[instanceUUID][]VMRuleLink - grouped by instance UUID
func (a *AlarmOperator) GetInstanceRuleLinks(ctx context.Context, instanceUUIDs []string) (map[string][]model.VMRuleLink, error) {
	if len(instanceUUIDs) == 0 {
		return map[string][]model.VMRuleLink{}, nil
	}

	ctx, db := common.GetContextDB(ctx)
	var links []model.VMRuleLink

	if err := db.Where("vm_uuid IN (?)", instanceUUIDs).Find(&links).Error; err != nil {
		log.Printf("[GetInstanceRuleLinks] Query failed: %v", err)
		return nil, fmt.Errorf("failed to query rule links: %w", err)
	}

	// Group by instance UUID
	result := make(map[string][]model.VMRuleLink)
	for _, link := range links {
		result[link.VMUUID] = append(result[link.VMUUID], link)
	}

	log.Printf("[GetInstanceRuleLinks] Found %d links for %d instances", len(links), len(result))
	return result, nil
}

// cleanRuleData removes internal database fields from rule data
func cleanRuleData(data interface{}) interface{} {
	// Fields to remove
	fieldsToRemove := []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "Creater", "OwnerInfo", "GroupUUID"}

	// First convert to map if it's not already
	var dataMap map[string]interface{}

	switch v := data.(type) {
	case map[string]interface{}:
		dataMap = v
	default:
		// Convert struct to map via JSON
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			log.Printf("[cleanRuleData] Failed to marshal data: %v", err)
			return data
		}
		if err := json.Unmarshal(jsonBytes, &dataMap); err != nil {
			log.Printf("[cleanRuleData] Failed to unmarshal data: %v", err)
			return data
		}
	}

	// Clean the map
	for _, field := range fieldsToRemove {
		delete(dataMap, field)
	}

	// Clean nested details array
	if details, ok := dataMap["details"].([]interface{}); ok {
		cleanedDetails := make([]interface{}, len(details))
		for i, detail := range details {
			cleanedDetails[i] = cleanRuleData(detail)
		}
		dataMap["details"] = cleanedDetails
	}

	return dataMap
}

// GetInstanceRuleDetails retrieves complete rule group information for specific instances
// This is used by the API layer to return detailed rule information including rule details
// Input: instanceUUIDs - list of instance UUIDs to query
// Output: map with instance_id as key, containing complete rule information (both alarm and adjust rules)
// Each rule group includes an "interfaces" array field containing the network interfaces linked to that rule group
func (a *AlarmOperator) GetInstanceRuleDetails(ctx context.Context, instanceUUIDs []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// Process each instance
	for _, instanceUUID := range instanceUUIDs {
		// 1. Get all rule links for this instance to build group_uuid -> interfaces mapping
		ctx, db := common.GetContextDB(ctx)
		var links []model.VMRuleLink
		if err := db.Where("vm_uuid = ? AND deleted_at IS NULL", instanceUUID).Find(&links).Error; err != nil {
			log.Printf("[GetInstanceRuleDetails] Failed to get rule links for instance %s: %v", instanceUUID, err)
		}

		// 2. Build map: group_uuid -> []interfaces (using map for deduplication)
		groupInterfacesMap := make(map[string]map[string]bool) // group_uuid -> map[interface]bool
		for _, link := range links {
			if link.Interface != "" && link.GroupUUID != "" {
				if groupInterfacesMap[link.GroupUUID] == nil {
					groupInterfacesMap[link.GroupUUID] = make(map[string]bool)
				}
				groupInterfacesMap[link.GroupUUID][link.Interface] = true
			}
		}

		// Convert to final format: group_uuid -> []interfaces
		groupInterfaces := make(map[string][]string)
		for groupUUID, ifaceMap := range groupInterfacesMap {
			interfaces := make([]string, 0, len(ifaceMap))
			for iface := range ifaceMap {
				interfaces = append(interfaces, iface)
			}
			groupInterfaces[groupUUID] = interfaces
		}

		// 3. Get all rule_ids for this instance
		ruleIDs, err := a.GetRuleIDsByInstance(ctx, instanceUUID)
		if err != nil {
			log.Printf("[GetInstanceRuleDetails] Failed to get rule IDs for instance %s: %v", instanceUUID, err)
			// Continue with next instance instead of failing completely
			result[instanceUUID] = map[string]interface{}{
				"instance_id": instanceUUID,
				"rule_count":  0,
				"rule_groups": []interface{}{},
				"error":       err.Error(),
			}
			continue
		}

		// 4. Get complete rule information for each rule_id - return raw database model
		rules := make([]interface{}, 0)
		for _, ruleID := range ruleIDs {
			rule, err := a.GetCompleteRuleByRuleID(ctx, ruleID)
			if err != nil {
				log.Printf("[GetInstanceRuleDetails] Failed to get rule %s for instance %s: %v", ruleID, instanceUUID, err)
				// Continue with next rule instead of failing
				continue
			}

			// Clean and append the rule data
			if rule != nil {
				cleanedRule := cleanRuleData(rule)

				// 5. Add interfaces array to the rule group
				// Extract UUID from the cleaned rule to match with groupInterfaces
				// Convert to map if not already
				var ruleMap map[string]interface{}
				if m, ok := cleanedRule.(map[string]interface{}); ok {
					ruleMap = m
				} else {
					// Convert struct to map via JSON
					jsonBytes, err := json.Marshal(cleanedRule)
					if err == nil {
						if err := json.Unmarshal(jsonBytes, &ruleMap); err != nil {
							log.Printf("[GetInstanceRuleDetails] Failed to unmarshal rule to map: %v", err)
							ruleMap = make(map[string]interface{})
						}
					} else {
						log.Printf("[GetInstanceRuleDetails] Failed to marshal rule: %v", err)
						ruleMap = make(map[string]interface{})
					}
				}

				// Try to get UUID from different possible field names (UUID, uuid, Uuid)
				var groupUUID string
				if uuid, exists := ruleMap["UUID"].(string); exists && uuid != "" {
					groupUUID = uuid
				} else if uuid, exists := ruleMap["uuid"].(string); exists && uuid != "" {
					groupUUID = uuid
				} else if uuid, exists := ruleMap["Uuid"].(string); exists && uuid != "" {
					groupUUID = uuid
				}

				// Get interfaces for this group_uuid
				var interfaces []string
				if groupUUID != "" {
					interfaces = groupInterfaces[groupUUID]
					if interfaces == nil {
						interfaces = []string{} // Return empty array if no interfaces
					}
				} else {
					// If UUID not found, set empty array
					interfaces = []string{}
					log.Printf("[GetInstanceRuleDetails] Warning: Could not find UUID in rule for rule_id %s", ruleID)
				}

				ruleMap["interfaces"] = interfaces
				cleanedRule = ruleMap

				rules = append(rules, cleanedRule)
			} else {
				log.Printf("[GetInstanceRuleDetails] Nil rule returned for rule_id %s", ruleID)
			}
		}

		// 6. Build result for this instance
		result[instanceUUID] = map[string]interface{}{
			"instance_id": instanceUUID,
			"rule_count":  len(rules),
			"rule_groups": rules,
		}
	}

	// Ensure all instances have a result entry
	for _, instanceUUID := range instanceUUIDs {
		if _, exists := result[instanceUUID]; !exists {
			result[instanceUUID] = map[string]interface{}{
				"instance_id": instanceUUID,
				"rule_count":  0,
				"rule_groups": []interface{}{},
			}
		}
	}

	return result, nil
}

func (a *AlarmOperator) DeleteRuleGroupWithDependencies(ctx context.Context, groupUUID, ruleType string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		// delete detail db
		switch ruleType {
		case "cpu":
			if err := tx.Where("group_uuid = ?", groupUUID).
				Delete(&model.CPURuleDetail{}).Error; err != nil {
				log.Printf("CPU rules delete failed: group_uuid=%s, error=%v", groupUUID, err)
				return fmt.Errorf("CPU rules delete failed: %w", err)
			}
		case "memory":
			if err := tx.Where("group_uuid = ?", groupUUID).
				Delete(&model.MemoryRuleDetail{}).Error; err != nil {
				log.Printf("Memory rules delete failed: group_uuid=%s, error=%v", groupUUID, err)
				return fmt.Errorf("Memory rules delete failed: %w", err)
			}
		case "bw":
			if err := tx.Where("group_uuid = ?", groupUUID).
				Delete(&model.BWRuleDetail{}).Error; err != nil {
				log.Printf("bw rules delete failed: group_uuid=%s, error=%v", groupUUID, err)
				return fmt.Errorf("bw rules delete failed: %w", err)
			}
		default:
			return fmt.Errorf("unknow type: %s", ruleType)
		}
		// delete link db
		if err := tx.Where("group_uuid = ?", groupUUID).
			Delete(&model.VMRuleLink{}).Error; err != nil {
			log.Printf("failed to del vm link: groupUUID=%s, error=%v", groupUUID, err)
			return fmt.Errorf("failed to del vm link: %w", err)
		}
		// delete group rule db
		if err := tx.Where("uuid = ? AND type = ?", groupUUID, ruleType).
			Delete(&model.RuleGroupV2{}).Error; err != nil {
			log.Printf("group del failed: groupUUID=%s, error=%v", groupUUID, err)
			return fmt.Errorf("group del failed: %w", err)
		}

		return nil
	})
}

func Paginate(page, pageSize int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		offset := (page - 1) * pageSize
		return db.Offset(offset).Limit(pageSize)
	}
}

func (a *AlarmOperator) DeleteCPURulesByGroup(ctx context.Context, groupID string) error {
	ctx, db := common.GetContextDB(ctx)
	if err := db.Where("group_uuid = ?", groupID).
		Delete(&CPURule{}).Error; err != nil {
		log.Printf("CPU rule delete failed: groupID=%s, error=%v", groupID, err)
		return err
	}
	return nil
}

func (a *AlarmOperator) ListRuleGroups(ctx context.Context, params ListRuleGroupsParams) ([]model.RuleGroupV2, int64, error) {
	ctx, db := common.GetContextDB(ctx)
	var groups []model.RuleGroupV2
	var total int64

	query := db.Model(&model.RuleGroupV2{})
	if params.RuleType != "" {
		query = query.Where("type = ?", params.RuleType)
	}
	if params.GroupUUID != "" {
		query = query.Where("uuid = ?", params.GroupUUID)
	}
	// Added: Support query by rule_id
	if params.RuleID != "" {
		query = query.Where("rule_id = ?", params.RuleID)
	}

	if err := query.Count(&total).Error; err != nil {
		log.Printf("get rules count failed: ruleType=%s, error=%v", params.RuleType, err)
		return nil, 0, fmt.Errorf("get rules count failed: %w", err)
	}
	if err := query.Scopes(Paginate(params.Page, params.PageSize)).
		Find(&groups).Error; err != nil {
		log.Printf("page query failed: ruleType=%s, page=%d, pageSize=%d, error=%v",
			params.RuleType, params.Page, params.PageSize, err)
		return nil, 0, fmt.Errorf("page query failed: %w", err)
	}

	return groups, total, nil
}

func (a *AlarmOperator) GetCPURuleDetails(ctx context.Context, groupUUID string) ([]model.CPURuleDetail, error) {
	ctx, db := common.GetContextDB(ctx)
	var details []model.CPURuleDetail
	if err := db.Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		log.Printf("query CPU rules detail failed: groupUUID=%s, error=%v", groupUUID, err)
	}
	return details, nil
}

func (a *AlarmOperator) GetMemoryRuleDetails(ctx context.Context, groupUUID string) ([]model.MemoryRuleDetail, error) {
	ctx, db := common.GetContextDB(ctx)
	var details []model.MemoryRuleDetail
	if err := db.Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		log.Printf("query Memory rules detail failed: groupUUID=%s, error=%v", groupUUID, err)
	}
	return details, nil
}

func (a *AlarmOperator) IncrementTriggerCount(ctx context.Context, groupID string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Model(&model.RuleGroupV2{}).
		Where("uuid = ?", groupID).
		Update("trigger_cnt", gorm.Expr("trigger_cnt + 1")).Error
}

func (a *AlarmOperator) CreateCPURules(ctx context.Context, groupUUID string, rules []CPURule) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range rules {
			rule := &CPURule{
				GroupUUID:    groupUUID,
				Name:         rules[i].Name,
				Duration:     rules[i].Duration,
				Over:         rules[i].Over,
				DownDuration: rules[i].DownDuration,
				DownTo:       rules[i].DownTo,
			}
			if err := tx.Create(rule).Error; err != nil {
				log.Printf("create cpu rule failed: groupUUID=%s, rule=%+v, error=%v", groupUUID, rules[i], err)
				return fmt.Errorf("create cpu rule failed: %w", err)
			}
		}
		return nil
	})
}

func (a *AlarmOperator) CreateBWRuleDetail(ctx context.Context, detail *model.BWRuleDetail) error {
	ctx, db := common.GetContextDB(ctx)
	if err := db.Create(detail).Error; err != nil {
		log.Printf("create bw rule detail failed: groupUUID=%s, name=%s, error=%v",
			detail.GroupUUID, detail.Name, err)
		return fmt.Errorf("create bw rule detail failed: %w", err)
	}
	return nil
}

func (a *AlarmOperator) GetBWRuleDetails(ctx context.Context, groupUUID string) ([]model.BWRuleDetail, error) {
	ctx, db := common.GetContextDB(ctx)
	var details []model.BWRuleDetail
	if err := db.Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		log.Printf("query db BW rules detailed: groupUUID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("query db BW rules detailed: %w", err)
	}
	return details, nil
}

func (a *AlarmOperator) CreateRuleGroup(ctx context.Context, group *model.RuleGroupV2) error {
	ctx, db := common.GetContextDB(ctx)
	if err := db.Create(group).Error; err != nil {
		log.Printf("failed to create rule: UUID=%s, GroupUUID=%s, error=%v", uuid.New().String(), group.UUID, err)
		return fmt.Errorf("failed to create rule: %w", err)
	}
	return nil
}

func (a *AlarmOperator) CreateCPURuleDetail(ctx context.Context, detail *model.CPURuleDetail) error {
	ctx, db := common.GetContextDB(ctx)
	detail.UUID = uuid.NewString()
	if err := db.Create(detail).Error; err != nil {
		log.Printf("create cpu rule detail failed: groupUUID=%s, ruleName=%s, error=%v", detail.GroupUUID, detail.Name, err)
		return fmt.Errorf("create cpu rule detail failed: %w", err)
	}
	return nil
}

func (a *AlarmOperator) CreateMemoryRuleDetail(ctx context.Context, detail *model.MemoryRuleDetail) error {
	ctx, db := common.GetContextDB(ctx)
	detail.UUID = uuid.NewString()
	if err := db.Create(detail).Error; err != nil {
		log.Printf("create memory rule detail failed: groupUUID=%s, ruleName=%s, error=%v", detail.GroupUUID, detail.Name, err)
		return fmt.Errorf("create memory rule detail failed: %w", err)
	}
	return nil
}

func isLocalIP(ip string) bool {
	if ip == "localhost" || ip == "127.0.0.1" {
		return true
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Printf("get local network configuration failed: %v", err)
		return false
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.String() == ip {
				return true
			}
		}
	}
	return false
}

func AlertRUleClient(baseURL, certFile, keyFile string) (*PrometheusClient, error) {
	var client *http.Client
	if certFile != "" && keyFile == "" {
		caCert, err := os.ReadFile(certFile)
		if err != nil {
			return nil, fmt.Errorf("Read cert file failed: %v", err)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		tlsConfig := &tls.Config{
			RootCAs:            caCertPool,
			InsecureSkipVerify: false,
		}
		transport := &http.Transport{
			TLSClientConfig: tlsConfig,
		}

		client = &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		}
	} else if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("Load cert file failed: %v", err)
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: false,
		}

		caCertPath := filepath.Join(filepath.Dir(certFile), "ca.crt")
		if _, err := os.Stat(caCertPath); err == nil {
			caCert, err := os.ReadFile(caCertPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %v", err)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)
			tlsConfig.RootCAs = caCertPool
		}

		transport := &http.Transport{
			TLSClientConfig: tlsConfig,
		}

		client = &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		}
	} else {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &PrometheusClient{
		BaseURL:    baseURL,
		HTTPClient: client,
	}, nil
}

func (c *PrometheusClient) sendRequest(endpoint string, req RuleFileRequest) ([]byte, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("The request serialization failed: %v", err)
	}

	url := c.BaseURL + endpoint
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("Create HTTP request failed: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Read response failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return respBody, fmt.Errorf("server status code: %d, respbody: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (c *PrometheusClient) sendRequestNoResponse(endpoint string, req RuleFileRequest) error {
	_, err := c.sendRequest(endpoint, req)
	return err
}

func (c *PrometheusClient) ClientReadRuleFile(path string) ([]byte, error) {
	req := RuleFileRequest{
		Operation: "read",
		FilePath:  path,
		FileUser:  "prometheus",
	}

	respBody, err := c.sendRequest("/api/v1/rules/file", req)
	if err != nil {
		log.Printf("prometheus server read file failed: %v", err)
		return nil, err
	}

	// Parse response
	var response RuleFileResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		log.Printf("parse response failed: %v", err)
		return nil, fmt.Errorf("parse response failed: %v", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("read file failed: %s", response.Message)
	}

	return []byte(response.Content), nil
}
func (c *PrometheusClient) ClientWriteRuleFile(path string, content []byte, perm os.FileMode) error {
	req := RuleFileRequest{
		Operation: "write",
		FilePath:  path,
		Content:   string(content),
		FileUser:  "prometheus",
	}
	err := c.sendRequestNoResponse("/api/v1/rules/file", req)
	if err != nil {
		log.Printf("prometheus server create file failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientCreateSymlink(target, link string) error {
	req := RuleFileRequest{
		Operation: "symlink",
		FilePath:  target,
		LinkPath:  link,
		FileUser:  "prometheus",
	}

	err := c.sendRequestNoResponse("/api/v1/rules/symlink", req)
	if err != nil {
		log.Printf("prometheus server create link failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientSetFileOwner(path string) error {
	req := RuleFileRequest{
		Operation: "chown",
		FilePath:  path,
		FileUser:  "prometheus",
	}

	err := c.sendRequestNoResponse("/api/v1/rules/chown", req)
	if err != nil {
		log.Printf("prometheus server create link failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientSetSymlinkOwner(path string) error {
	req := RuleFileRequest{
		Operation: "chown_symlink",
		FilePath:  path,
		FileUser:  "prometheus",
	}

	err := c.sendRequestNoResponse("/api/v1/rules/chown", req)
	if err != nil {
		log.Printf("prometheus server set link owner failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientRemoveRuleFile(path string) error {
	req := RuleFileRequest{
		Operation: "delete",
		FilePath:  path,
	}

	err := c.sendRequestNoResponse("/api/v1/rules/file", req)
	if err != nil {
		log.Printf("prometheus server remove file failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientCheckFileExists(path string) (bool, error) {
	req := RuleFileRequest{
		Operation: "check",
		FilePath:  path,
	}
	respBody, err := c.sendRequest("/api/v1/rules/file", req)
	if err != nil {
		log.Printf("server check file failed: %v", err)
		return false, err
	}
	var resp RuleFileResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}
	return resp.Exists, nil
}

func (c *PrometheusClient) ClientRemoveSymlink(linkPath string) error {
	req := RuleFileRequest{
		Operation: "delete",
		FilePath:  linkPath,
	}

	err := c.sendRequestNoResponse("/api/v1/rules/file", req)
	if err != nil {
		log.Printf("prometheus server remove symlink failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientGetUser(username string) (int, int, error) {
	req := RuleFileRequest{
		Operation: "getuser",
		FileUser:  username,
	}

	resp, err := c.sendRequest("/api/v1/rules/user", req)
	if err != nil {
		return 0, 0, err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		UID     int    `json:"uid"`
		GID     int    `json:"gid"`
	}

	if err := json.Unmarshal(resp, &response); err != nil {
		return 0, 0, fmt.Errorf("failed to parse response: %v", err)
	}

	if !response.Success {
		return 0, 0, fmt.Errorf("failed to get user information: %s", response.Message)
	}

	return response.UID, response.GID, nil
}

func (c *PrometheusClient) ClientReloadPrometheus() error {
	req := RuleFileRequest{
		Operation: "reload",
	}

	return c.sendRequestNoResponse("/api/v1/rules/reload", req)
}

func GetUser(username string) (uid, gid int, err error) {
	if isRemotePrometheus {
		// Get user ID from remote server
		uid, gid, err = prometheusClient.ClientGetUser("prometheus")
		if err != nil {
			return 0, 0, fmt.Errorf("failed to get remote group ID for %s: %v", username, err)
		}

		return uid, gid, nil
	} else {
		// Get user information locally
		u, err := user.Lookup(username)
		if err != nil {
			return 0, 0, err
		}
		uid, _ = strconv.Atoi(u.Uid)
		gid, _ = strconv.Atoi(u.Gid)
		return uid, gid, nil
	}
}

func ReadFile(path string) ([]byte, error) {
	fmt.Printf("wngzhe ReadFile isRemotePrometheus: %t,  prometheusClient: %p", isRemotePrometheus, prometheusClient)
	if isRemotePrometheus {
		fmt.Printf("wngzhe ReadFile remote")
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return nil, fmt.Errorf("The Prometheus client has not been initialized.")
		}
		return prometheusClient.ClientReadRuleFile(path)
	} else {
		fmt.Printf("wngzhe ReadFile local")
		return os.ReadFile(path)
	}
}

func WriteFile(path string, content []byte, perm os.FileMode) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}
		return prometheusClient.ClientWriteRuleFile(path, content, perm)
	} else {
		os.WriteFile(path, content, perm)
		uid, gid, err := GetUser("prometheus")
		if err != nil {
			return err
		}
		return SetFileOwner(path, uid, gid)
	}
}

func SetFileOwner(path string, uid, gid int) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}

		return prometheusClient.ClientSetFileOwner(path)
	} else {
		return os.Chown(path, uid, gid)
	}
}

func SetSymlinkOwner(path string, uid, gid int) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}

		return prometheusClient.ClientSetSymlinkOwner(path)
	} else {
		uid, gid, err := GetUser("prometheus")
		if err != nil {
			return os.Lchown(path, uid, gid)
		} else {
			log.Printf("Prometheus server set link owenr failed with %s", err)
			return err
		}
	}
}

func CreateSymlink(target, link string) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}

		return prometheusClient.ClientCreateSymlink(target, link)
	} else {
		if _, err := os.Lstat(link); err == nil {
			os.Remove(link)
		}
		os.Symlink(target, link)
		uid, gid, err := GetUser("prometheus")
		if err != nil {
			return err
		}
		return SetSymlinkOwner(link, uid, gid)

	}
}

func RemoveSymlink(link string) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}

		return prometheusClient.ClientRemoveSymlink(link)
	} else {
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			return nil
		}
		return os.Remove(link)
	}
}

func ReloadPrometheus() error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}

		return prometheusClient.ClientReloadPrometheus()
	} else {
		cmd := exec.Command("sudo", "systemctl", "kill", "-s", "SIGHUP", "prometheus.service")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("SIGHUP operation failed: %v, output: %s", err, string(output))
		}
		return nil
	}
}

// ReloadPrometheusViaHTTP reloads Prometheus configuration via HTTP API (requires --web.enable-lifecycle)
func ReloadPrometheusViaHTTP() error {
	var reloadURL string

	// Step 1: Construct reload URL
	if isRemotePrometheus {
		// Remote scenario: use Prometheus IP and port
		prometheusIP := GetPrometheusIP()
		prometheusPort := GetPrometheusPort()
		reloadURL = fmt.Sprintf("http://%s:%d/-/reload", prometheusIP, prometheusPort)
		log.Printf("Reloading remote Prometheus via HTTP: %s", reloadURL)
	} else {
		// Local scenario: use localhost
		reloadURL = "http://localhost:9090/-/reload"
		log.Printf("Reloading local Prometheus via HTTP: %s", reloadURL)
	}

	// Step 2: Create HTTP client with timeout
	// Use a longer timeout to handle slow Prometheus reloads on large deployments
	client := &http.Client{
		Timeout: 2 * time.Minute,
	}

	// Step 3: Send POST request
	resp, err := client.Post(reloadURL, "", nil)
	if err != nil {
		log.Printf("Failed to send reload request to Prometheus: %v", err)
		return fmt.Errorf("failed to reload Prometheus via HTTP: %v", err)
	}
	defer resp.Body.Close()

	// Step 4: Check response status code
	if resp.StatusCode != 200 {
		// Read response body for detailed error information
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Prometheus reload failed with status %d: %s", resp.StatusCode, string(body))

		// Handle 403 error specifically (lifecycle API not enabled)
		if resp.StatusCode == 403 {
			return fmt.Errorf("Prometheus lifecycle API is not enabled (HTTP 403). Please add --web.enable-lifecycle flag")
		}

		return fmt.Errorf("reload failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Step 5: Success log
	if isRemotePrometheus {
		log.Printf("Successfully reloaded remote Prometheus configuration via HTTP API")
	} else {
		log.Printf("Successfully reloaded local Prometheus configuration via HTTP API")
	}

	return nil
}

func RemoveFile(path string) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return fmt.Errorf("The Prometheus client has not been initialized.")
		}

		return prometheusClient.ClientRemoveRuleFile(path)
	} else {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		return os.Remove(path)
	}
}

func CheckFileExists(path string) (bool, error) {
	if isRemotePrometheus {
		return prometheusClient.ClientCheckFileExists(path)
	} else {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	}
}

func handleAlarmRuleError(c *macaron.Context, err error) {
	// Define error type mapping
	errorResponses := map[string]struct {
		status  int
		message string
	}{
		"already exists":    {http.StatusConflict, "Rule already exists"},
		"not found":         {http.StatusNotFound, "Rule not found"},
		"invalid rule":      {http.StatusBadRequest, "Invalid rule configuration"},
		"permission denied": {http.StatusForbidden, "Permission denied"},
		"database error":    {http.StatusInternalServerError, "Database operation failed"},
		"invalid uuid":      {http.StatusBadRequest, "Invalid rule ID"},
		"in use":            {http.StatusConflict, "Rule is currently in use"},
	}

	// Iterate through error type mapping
	for errType, response := range errorResponses {
		if strings.Contains(strings.ToLower(err.Error()), errType) {
			c.JSON(response.status, map[string]interface{}{
				"error":   response.message,
				"details": err.Error(),
			})
			return
		}
	}

	// Default error handling
	c.JSON(http.StatusInternalServerError, map[string]interface{}{
		"error":   "Operation failed",
		"details": err.Error(),
	})
}

func (a *AlarmOperator) GetNodeAlarmRules(ctx context.Context, uuid string) ([]model.NodeAlarmRule, error) {
	ctx, db := common.GetContextDB(ctx)
	var rules []model.NodeAlarmRule
	if err := db.Where("uuid = ?", uuid).Find(&rules).Error; err != nil {
		log.Printf("query db node alarm rules: uuid=%s, error=%v", uuid, err)
		return nil, fmt.Errorf("query db node alarm rules: %w", err)
	}
	if uuid == "" {
		var allRules []model.NodeAlarmRule
		if err := db.Find(&allRules).Error; err != nil {
			log.Printf("query all node alarm rules: error=%v", err)
			return nil, fmt.Errorf("query all node alarm rules: %w", err)
		}
		return allRules, nil
	}
	return rules, nil
}

// CreateNodeAlarmRules creates a new node alarm rule
func (a *AlarmOperator) CreateNodeAlarmRules(ctx context.Context, rule *model.NodeAlarmRule) error {
	ctx, db := common.GetContextDB(ctx)
	rule.UUID = uuid.NewString()
	if err := db.Create(rule).Error; err != nil {
		log.Printf("Failed to create node alarm rule: ruleType=%s, name=%s, error=%v",
			rule.RuleType,
			rule.Name,
			err)
		return fmt.Errorf("failed to create node alarm rule: %w", err)
	}
	return nil
}

func (a *AlarmOperator) DeleteNodeAlarmRules(ctx context.Context, uuid string) error {
	ctx, db := common.GetContextDB(ctx)
	result := db.Where("uuid = ?", uuid).Delete(&model.NodeAlarmRule{})
	if result.Error != nil {
		log.Printf("Failed to delete node alarm rule: uuid=%s, error=%v", uuid, result.Error)
		return fmt.Errorf("failed to delete node alarm rule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("node alarm rule not found: %s", uuid)
	}
	return nil
}

func (a *AlarmOperator) UpdateNodeAlarmRule(ctx context.Context, uuid string, updates map[string]interface{}) error {
	ctx, db := common.GetContextDB(ctx)
	result := db.Model(&model.NodeAlarmRule{}).Where("uuid = ?", uuid).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to update node alarm rule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("node alarm rule not found: %s", uuid)
	}
	return nil
}

func (a *AlarmOperator) DeleteNodeAlarmRuleByUUID(ctx context.Context, uuid string) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Where("uuid = ?", uuid).Delete(&model.NodeAlarmRule{}).Error
}

func (a *AlarmOperator) UpdateNodeAlarmRuleByUUID(ctx context.Context, uuid string, updates map[string]interface{}) error {
	ctx, db := common.GetContextDB(ctx)
	return db.Model(&model.NodeAlarmRule{}).Where("uuid = ?", uuid).Updates(updates).Error
}

// GetNodeAlarmRulesByType retrieves node alarm rules by rule type
func (a *AlarmOperator) GetNodeAlarmRulesByType(ctx context.Context, ruleType string) ([]model.NodeAlarmRule, error) {
	ctx, db := common.GetContextDB(ctx)
	var rules []model.NodeAlarmRule

	if err := db.Where("rule_type = ?", ruleType).Find(&rules).Error; err != nil {
		log.Printf("Failed to get node alarm rules by type: ruleType=%s, error=%v", ruleType, err)
		return nil, fmt.Errorf("failed to get node alarm rules by type: %w", err)
	}

	return rules, nil
}

func ProcessTemplate(templateFile, outputFile string, data map[string]interface{}) error {
	templatePath := filepath.Join(RuleTemplate, templateFile)

	// All rule files are now stored in RulesGeneral directory (simplified from previous owner-based separation)
	outputPath := filepath.Join(RulesGeneral, outputFile)

	// Read template content
	templateContent, err := ReadFile(templatePath)
	if err != nil {
		log.Printf("Failed to read template file: path=%s, error=%v", templatePath, err)
		return fmt.Errorf("failed to read template file %s: %w", templatePath, err)
	}
	fmt.Printf("wngzhe ProcessTemplate templateContent: %s,  templatePath: %s", templateContent, templatePath)

	var renderedContent string
	if strings.Contains(string(templateContent), "name: compute-network-resources") {
		renderedContent, err = renderNetworkResourcesTemplate(data)
	} else {
		renderedContent, err = renderTemplateContent(string(templateContent), data, templateFile)
	}
	fmt.Printf("wngzhe ProcessTemplate templateContent: %s,  err: %s", templateContent, err)
	if err != nil {
		log.Printf("Failed to render template: template=%s, error=%v", templateFile, err)
		return fmt.Errorf("failed to render template %s: %w", templateFile, err)
	}
	fmt.Printf("wngzhe ProcessTemplate templateContent: %s,  outputPath: %s", templateContent, outputPath)
	// Write rendered content to output file
	if err := WriteFile(outputPath, []byte(renderedContent), 0640); err != nil {
		log.Printf("Failed to write output file: path=%s, error=%v", outputPath, err)
		return fmt.Errorf("failed to write output file %s: %w", outputPath, err)
	}

	// Create symlink to RulesEnabled directory
	enabledPath := filepath.Join(RulesEnabled, filepath.Base(outputPath))
	if err := CreateSymlink(outputPath, enabledPath); err != nil {
		log.Printf("Failed to create symlink: source=%s, target=%s, error=%v",
			outputPath, enabledPath, err)
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	return nil
}

func renderNetworkResourcesTemplate(data map[string]interface{}) (string, error) {
	templatePath := filepath.Join(RuleTemplate, "compute-network-resources.yml.j2")
	templateContent, err := ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read network resources template: %w", err)
	}

	templateStr := string(templateContent)
	ruleTemplate := ""
	if strings.Contains(templateStr, "{% for net_type, params in network_types.items() %}") {
		parts := strings.Split(templateStr, "{% for net_type, params in network_types.items() %}")
		if len(parts) >= 2 {
			ruleTemplate = strings.Split(parts[1], "{% endfor %}")[0]
		}
	}

	if ruleTemplate == "" {
		return "", fmt.Errorf("invalid template format: missing for loop")
	}

	var rulesContent strings.Builder
	rulesContent.WriteString("groups:\n- name: compute-network-resources\n  rules:\n")

	networkTypes, ok := data["network_types"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("network_types not found in data")
	}

	for netType, params := range networkTypes {
		paramsMap, ok := params.(map[string]interface{})
		if !ok {
			continue
		}

		data["net_type"] = netType
		data["net_type_cap"] = strings.ToUpper(netType[:1]) + netType[1:]

		for k, v := range paramsMap {
			data[fmt.Sprintf("params.%s", k)] = v
		}

		ruleContent := ruleTemplate
		for key, value := range data {
			if strings.HasPrefix(key, "$labels.") {
				continue
			}
			placeholder := fmt.Sprintf("{{ %s }}", key)
			strValue := fmt.Sprintf("%v", value)
			ruleContent = strings.ReplaceAll(ruleContent, placeholder, strValue)
		}

		defaultPattern := regexp.MustCompile(`{{ ([^}]+) \| default\(([^)]+)\) }}`)
		ruleContent = defaultPattern.ReplaceAllStringFunc(ruleContent, func(match string) string {
			matches := defaultPattern.FindStringSubmatch(match)
			if len(matches) != 3 {
				return match
			}
			key := strings.TrimSpace(matches[1])
			defaultValue := strings.TrimSpace(matches[2])
			if value, ok := data[key]; ok {
				return fmt.Sprintf("%v", value)
			}
			return defaultValue
		})

		rulesContent.WriteString(ruleContent)
	}

	return rulesContent.String(), nil
}

func renderTemplateContent(templateContent string, data map[string]interface{}, templateName string) (string, error) {
	if strings.Contains(templateContent, "name: compute-network-resources") {
		return "", fmt.Errorf("this template should be handled by renderNetworkResourcesTemplate")
	}

	result := templateContent

	// 1. Replace all variables in data (excluding $labels.)
	for key, value := range data {
		if strings.HasPrefix(key, "$labels.") {
			continue
		}
		placeholder := fmt.Sprintf("{{ %s }}", key)
		strValue := fmt.Sprintf("%v", value)
		result = strings.ReplaceAll(result, placeholder, strValue)

		// 1.1 Replace {{ key | default("xxx") }} format
		defaultPattern := regexp.MustCompile(fmt.Sprintf(`{{ %s \| default\("([^"]*)"\) }}`, key))
		result = defaultPattern.ReplaceAllString(result, strValue)

		// 1.2 Replace {{ key | default(xxx) }} format without quotes
		noQuotesPattern := regexp.MustCompile(fmt.Sprintf(`{{ *(?:\(?%s\)? *\| *default\(([^"\)]+)\)) *}}`, key))
		result = noQuotesPattern.ReplaceAllString(result, strValue)
	}

	// 2. Replace remaining default syntax variables (if no value provided in data)
	// 2.1 Default with quotes
	defaultPattern := regexp.MustCompile(`{{ ([a-zA-Z0-9_]+) \| default\("([^"]*)"\) }}`)
	result = defaultPattern.ReplaceAllStringFunc(result, func(match string) string {
		matches := defaultPattern.FindStringSubmatch(match)
		if len(matches) != 3 {
			return match
		}
		key := matches[1]
		if strings.HasPrefix(key, "$labels.") {
			return match
		}
		if val, ok := data[key]; ok {
			return fmt.Sprintf("%v", val)
		}
		return matches[2]
	})

	// 2.2 Default without quotes
	noQuotesPattern := regexp.MustCompile(`{{ *(?:\(?([a-zA-Z0-9_]+)\)? *\| *default\(([^"\)]+)\)) *}}`)
	result = noQuotesPattern.ReplaceAllStringFunc(result, func(match string) string {
		matches := noQuotesPattern.FindStringSubmatch(match)
		if len(matches) != 3 {
			return match
		}
		key := matches[1]
		if strings.HasPrefix(key, "$labels.") {
			return match
		}
		if val, ok := data[key]; ok {
			return fmt.Sprintf("%v", val)
		}
		return matches[2]
	})

	// 3. Unpack Prometheus template expressions, for example:
	// {{ "{{ if $labels.xxx }}a{{ else }}b{{ end }}" }} -> {{ if $labels.xxx }}a{{ else }}b{{ end }}
	promExprPattern := regexp.MustCompile(`{{ "{{ ([^{}]+) }}" }}`)
	result = promExprPattern.ReplaceAllString(result, "{{ $1 }}")

	return result, nil
}

func validateNodeAlarmRule(rule *model.NodeAlarmRule) error {
	if rule.RuleType == "" {
		return fmt.Errorf("rule_type is required")
	}
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}
	if rule.Owner == "" {
		return fmt.Errorf("owner is required")
	}
	if len(rule.Config.RawMessage) == 0 {
		return fmt.Errorf("config is required")
	}

	// Validate if config is valid JSON
	var temp interface{}
	if err := json.Unmarshal(rule.Config.RawMessage, &temp); err != nil {
		return fmt.Errorf("config must be valid JSON")
	}
	return nil
}

func createNodeAlarmRuleInternal(ctx context.Context, rule *model.NodeAlarmRule) (*model.NodeAlarmRule, error) {
	if err := validateNodeAlarmRule(rule); err != nil {
		return nil, err
	}

	operator := &AlarmOperator{}
	existingRules, err := operator.GetNodeAlarmRulesByType(ctx, rule.RuleType)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing rules: %v", err)
	}
	if len(existingRules) > 0 {
		return nil, fmt.Errorf("rule type %s already exists, only one rule per type is allowed", rule.RuleType)
	}

	newRule := &model.NodeAlarmRule{
		RuleType:    rule.RuleType,
		Name:        rule.Name,
		Config:      rule.Config,
		Description: rule.Description,
		Owner:       rule.Owner,
		Enabled:     true,
	}
	err = operator.CreateNodeAlarmRules(ctx, newRule)
	if err != nil {
		return nil, fmt.Errorf("failed to save rule to database: %v", err)
	}

	var templateFiles []string
	switch rule.RuleType {
	case RuleTypeAvailable:
		templateFiles = []string{"node-availability.yml.j2"}
	case RuleTypeControl:
		templateFiles = []string{"management-resources.yml.j2"}
	case RuleTypeCompute:
		templateFiles = []string{"compute-core-resources.yml.j2", "compute-network-resources.yml.j2"}
	case RuleTypeHypervisorVCPU:
		templateFiles = []string{"compute-vcpu-resources.yml.j2"}
	case RuleTypePacketDrop:
		templateFiles = []string{"packet-drop-monitor.yml.j2"}
	case RuleTypeIPBlock:
		templateFiles = []string{"ip-block-monitor.yml.j2"}
	default:
		operator.DeleteNodeAlarmRules(ctx, newRule.UUID)
		return nil, fmt.Errorf("unsupported rule type: %s", rule.RuleType)
	}

	for _, templateFile := range templateFiles {
		var configData map[string]interface{}
		if err = json.Unmarshal(rule.Config.RawMessage, &configData); err != nil {
			operator.DeleteNodeAlarmRules(ctx, newRule.UUID)
			return nil, fmt.Errorf("failed to parse config JSON: %v", err)
		}

		if rule.RuleType == RuleTypeAvailable {
			if nodeDownDuration, ok := configData["node_down_duration"].(string); ok {
				duration, err := time.ParseDuration(nodeDownDuration)
				if err != nil {
					operator.DeleteNodeAlarmRules(ctx, newRule.UUID)
					return nil, fmt.Errorf("invalid node_down_duration format: %v", err)
				}
				configData["node_down_duration_minutes"] = int(duration.Minutes())
			} else {
				configData["node_down_duration_minutes"] = 5
			}
		}

		outputFile := strings.TrimSuffix(templateFile, ".j2")

		err = ProcessTemplate(templateFile, outputFile, configData)
		if err != nil {
			operator.DeleteNodeAlarmRules(ctx, newRule.UUID)
			return nil, fmt.Errorf("failed to process template %s: %v", templateFile, err)
		}
	}

	if err := ReloadPrometheusViaHTTP(); err != nil {
		log.Printf("Failed to reload Prometheus: %v", err)
	}

	return newRule, nil
}

func (a *AlarmAdmin) CreateNodeAlarmRule(ctx context.Context, rule *model.NodeAlarmRule) (*model.NodeAlarmRule, error) {
	return createNodeAlarmRuleInternal(ctx, rule)
}

func (v *AlarmView) CreateNodeAlarmRule(c *macaron.Context) {
	var rule model.NodeAlarmRule

	// Get parameters from query or form data, similar to FlavorView.Create
	ruleType := c.Query("rule_type")
	name := c.Query("name")
	description := c.Query("description")
	owner := c.Query("owner")
	enabledStr := c.Query("enabled")
	configStr := c.Query("config") // Get config as a string

	// Convert enabled string to boolean
	enabled := true // Default to true if not specified or invalid
	if enabledStr != "" {
		var parseErr error
		enabled, parseErr = strconv.ParseBool(enabledStr)
		if parseErr != nil {
			// Handle parse error if necessary, or just use default true
			log.Printf("Failed to parse enabled status: %v, using default true", parseErr)
			enabled = true // Ensure it's true on parse error
		}
	}

	// Unmarshal config string into json.RawMessage
	var configRaw json.RawMessage
	if configStr != "" {
		configRaw = json.RawMessage(configStr)
	}

	// Populate the rule struct
	rule = model.NodeAlarmRule{
		RuleType:    ruleType,
		Name:        name,
		Description: description,
		Owner:       owner,
		Enabled:     enabled,
		Config:      model.ConfigWrapper{RawMessage: configRaw},
	}

	// Perform validation (can reuse validateNodeAlarmRule if applicable)
	if err := validateNodeAlarmRule(&rule); err != nil {
		c.JSON(http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}

	// ... existing code to call createNodeAlarmRuleInternal and handle response ...
	rulePtr, err := createNodeAlarmRuleInternal(c.Req.Context(), &rule)
	if err != nil {
		handleAlarmRuleError(c, err)
		return
	}

	c.JSON(http.StatusCreated, map[string]interface{}{
		"message": "Node alarm rule created successfully",
		"rule": map[string]interface{}{
			"uuid":        rulePtr.UUID,
			"rule_type":   rulePtr.RuleType,
			"name":        rulePtr.Name,
			"description": rulePtr.Description,
			"created_at":  rulePtr.CreatedAt,
			"owner":       rulePtr.Owner,
			"enabled":     rulePtr.Enabled, // Include enabled status
		},
	})
}

func getNodeAlarmRulesInternal(ctx context.Context, uuid, ruleType string) ([]model.NodeAlarmRule, error) {
	operator := &AlarmOperator{}
	if uuid != "" {
		return operator.GetNodeAlarmRules(ctx, uuid)
	} else if ruleType != "" {
		return operator.GetNodeAlarmRulesByType(ctx, ruleType)
	} else {
		return operator.GetNodeAlarmRules(ctx, "")
	}
}

func (a *AlarmAdmin) GetNodeAlarmRules(ctx context.Context, uuid, ruleType string) ([]model.NodeAlarmRule, error) {
	return getNodeAlarmRulesInternal(ctx, uuid, ruleType)
}

func (v *AlarmView) GetNodeAlarmRules(c *macaron.Context, store session.Store) {
	uuid := c.Query("uuid")
	ruleType := c.Query("rule_type")

	rules, err := getNodeAlarmRulesInternal(c.Req.Context(), uuid, ruleType)
	if err != nil {
		log.Printf("Failed to get node alarm rules: error=%v", err)
		c.JSON(http.StatusInternalServerError, map[string]interface{}{"error": "failed to get node alarm rules"})
		return
	}

	// Prepare data for template with formatted config
	var rulesForTemplate []map[string]interface{}
	for _, rule := range rules {
		ruleMap := make(map[string]interface{})

		// Copy fields from original rule (excluding Config which needs special handling)
		ruleMap["ID"] = rule.ID
		ruleMap["UUID"] = rule.UUID
		ruleMap["RuleType"] = rule.RuleType
		ruleMap["Name"] = rule.Name
		ruleMap["Description"] = rule.Description
		ruleMap["Owner"] = rule.Owner
		ruleMap["Enabled"] = rule.Enabled
		ruleMap["CreatedAt"] = rule.CreatedAt

		// Format Config JSON for display
		if len(rule.Config.RawMessage) > 0 {
			var js bytes.Buffer
			// Use json.Indent for pretty printing
			err := json.Indent(&js, rule.Config.RawMessage, "", "  ")
			if err != nil {
				log.Printf("Failed to format config JSON for rule %s: %v", rule.UUID, err)
				ruleMap["ConfigFormatted"] = string(rule.Config.RawMessage) // Fallback to raw
			} else {
				ruleMap["ConfigFormatted"] = js.String()
			}
		} else {
			ruleMap["ConfigFormatted"] = "{}"
		}

		rulesForTemplate = append(rulesForTemplate, ruleMap)
	}

	// Add data required for template
	c.Data["Rules"] = rulesForTemplate
	c.Data["Total"] = len(rules)
	c.Data["Query"] = c.Query("q")
	c.Data["Link"] = "/alarms/node"

	// Ensure i18n object is properly set
	if c.Locale != nil {
		c.Data["i18n"] = c.Locale
	} else {
		log.Printf("Warning: i18n object is nil")
		c.Data["i18n"] = &i18n.Locale{} // Provide an empty i18n object as fallback
	}

	// Copy other necessary context data
	if isAdmin, ok := c.Data["IsAdmin"].(bool); ok {
		c.Data["IsAdmin"] = isAdmin
	}
	if isSignedIn, ok := c.Data["IsSignedIn"].(bool); ok {
		c.Data["IsSignedIn"] = isSignedIn
	}
	if org, ok := c.Data["Organization"].(string); ok {
		c.Data["Organization"] = org
	}
	if members, ok := c.Data["Members"].([]*model.Member); ok {
		c.Data["Members"] = members
	}

	c.HTML(http.StatusOK, "alarms")
}

func deleteNodeAlarmRuleInternal(ctx context.Context, uuid string) ([]string, error) {
	operator := &AlarmOperator{}

	rules, err := operator.GetNodeAlarmRules(ctx, uuid)
	if err != nil {
		return nil, fmt.Errorf("failed to get rule information: %v", err)
	}

	if len(rules) == 0 {
		return nil, fmt.Errorf("node alarm rule not found")
	}

	rule := rules[0]

	if err := operator.DeleteNodeAlarmRules(ctx, uuid); err != nil {
		return nil, fmt.Errorf("failed to delete rule from database: %v", err)
	}

	var templateFiles []string
	switch rule.RuleType {
	case RuleTypeAvailable:
		templateFiles = []string{"node-availability.yml"}
	case RuleTypeControl:
		templateFiles = []string{"management-resources.yml"}
	case RuleTypeCompute:
		templateFiles = []string{
			"compute-core-resources.yml",
			"compute-network-resources.yml",
		}
	case RuleTypeHypervisorVCPU:
		templateFiles = []string{"compute-vcpu-resources.yml"}
	case RuleTypePacketDrop:
		templateFiles = []string{"packet-drop-monitor.yml"}
	case RuleTypeIPBlock:
		templateFiles = []string{"ip-block-monitor.yml"}
	case "service_monitoring":
		templateFiles = []string{"service_monitoring.yml"}
	}

	deletedFiles := []string{}
	for _, templateFile := range templateFiles {
		outputPath := filepath.Join(RulesGeneral, templateFile)
		enabledPath := filepath.Join(RulesEnabled, templateFile)

		if err := RemoveFile(enabledPath); err != nil {
			log.Printf("Failed to remove symlink: path=%s, error=%v", enabledPath, err)
		} else {
			deletedFiles = append(deletedFiles, enabledPath)
		}

		if err := RemoveFile(outputPath); err != nil {
			log.Printf("Failed to remove rule file: path=%s, error=%v", outputPath, err)
		} else {
			deletedFiles = append(deletedFiles, outputPath)
		}
	}

	if err := ReloadPrometheusViaHTTP(); err != nil {
		log.Printf("Failed to reload Prometheus configuration: error=%v", err)
	}

	return deletedFiles, nil
}

func (a *AlarmAdmin) DeleteNodeAlarmRule(ctx context.Context, uuid string) ([]string, error) {
	return deleteNodeAlarmRuleInternal(ctx, uuid)
}

func (v *AlarmView) DeleteNodeAlarmRule(c *macaron.Context) {
	uuid := c.Params(":uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "uuid is required"})
		return
	}

	deletedFiles, err := deleteNodeAlarmRuleInternal(c.Req.Context(), uuid)
	if err != nil {
		log.Printf("Failed to delete node alarm rule: uuid=%s, error=%v", uuid, err)
		c.JSON(http.StatusInternalServerError, map[string]interface{}{"error": fmt.Sprintf("failed to delete node alarm rule: %v", err)})
		return
	}

	c.JSON(http.StatusOK, map[string]interface{}{"status": "success", "message": "Node alarm rule deleted successfully", "deleted_files": deletedFiles})
}

func (v *AlarmView) NewNodeAlarmRule(c *macaron.Context) {
	c.HTML(200, "alarms_new")
}

// NotifyParams notification parameters
type NotifyParams struct {
	Alerts []struct {
		State       string            `json:"state"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
	} `json:"alerts"`
}

// SendNotification sends notification directly to the specified URL without authentication
// This is a generic notification function that can be used by both alarm and adjust rules
func (a *AlarmOperator) SendNotification(ctx context.Context, notifyURL string, params NotifyParams) error {
	// Send notification directly to notify_url (no authentication)
	jsonData, err := json.Marshal(params)
	if err != nil {
		log.Printf("[SendNotification] Failed to marshal params: %v", err)
		return fmt.Errorf("failed to marshal params: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", notifyURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("[SendNotification] Failed to create request: %v", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[SendNotification] HTTP request failed: %v", err)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[SendNotification] Request failed with status %d, response: %s", resp.StatusCode, string(body))
		return fmt.Errorf("notification service returned status %d", resp.StatusCode)
	}

	log.Printf("[SendNotification] Successfully sent notification to %s", notifyURL)
	return nil
}

// UpdateMatchedVMsJSON updates the matched_vms.json file for VM cleanup
// This is a public function that can be called from both apis and routes packages
func UpdateMatchedVMsJSON(ctx context.Context, vmUUIDs []string, groupUUID, operation, ruleType string, targetDevice ...string) error {
	// Path to matched_vms.json file
	matchedVMsFile := "/etc/prometheus/lists/matched_vms.json"

	// Read existing matched_vms.json
	var matchedVMs []map[string]interface{}
	existingData, err := ReadFile(matchedVMsFile)
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
		var notFoundVMs []string
		for _, instanceid := range vmUUIDs {
			domain, err := GetDomainByInstanceUUID(ctx, instanceid)
			if err != nil {
				log.Printf("Failed to get domain for instanceid=%s: %v", instanceid, err)
				notFoundVMs = append(notFoundVMs, instanceid)
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
				"targets": []string{"localhost:9109"},
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

		// If any VMs were not found, return an error
		if len(notFoundVMs) > 0 {
			return fmt.Errorf("instances not found: %v", notFoundVMs)
		}
	} else if operation == "remove" {
		// If targetDevice is provided (optional variadic parameter), delete by triple match;
		// Otherwise, use the original "delete all by groupUUID" logic (unchanged).
		// Build a non-empty device list to distinguish three cases:
		// 1) targetDevice not provided -> no device filter
		// 2) provided but empty string -> also no device filter
		// 3) provided non-empty -> enable device filter (triple match)
		nonEmptyDevices := []string{}
		for _, d := range targetDevice {
			if d != "" {
				nonEmptyDevices = append(nonEmptyDevices, d)
			}
		}
		hasDeviceFilter := len(nonEmptyDevices) > 0

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

			// No device filter:
			// - If vmUUIDs is empty: delete all entries by groupUUID (used by rule group deletion)
			// - Else: delete by group + specified vmUUIDs (clear all target devices for those VMs)
			if !hasDeviceFilter {
				if strings.HasSuffix(ruleID, "-"+groupUUID) {
					if len(vmUUIDs) == 0 {
						domain, _ := labels["domain"].(string)
						instanceID, _ := labels["instance_id"].(string)
						log.Printf("Removing mapping by group(all): domain=%s, rule_id=%s, instance_id=%s", domain, ruleID, instanceID)
						removedCount++
						continue
					}

					instanceID, _ := labels["instance_id"].(string)
					inVM := false
					for _, id := range vmUUIDs {
						if id == instanceID {
							inVM = true
							break
						}
					}
					if inVM {
						domain, _ := labels["domain"].(string)
						log.Printf("Removing mapping by group: domain=%s, rule_id=%s, instance_id=%s", domain, ruleID, instanceID)
						removedCount++
						continue
					}
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
			inVM := false
			for _, id := range vmUUIDs {
				if id == instanceID {
					inVM = true
					break
				}
			}
			inDev := false
			for _, d := range nonEmptyDevices {
				if d == labels["target_device"] {
					inDev = true
					break
				}
			}

			if inVM && inDev {
				domain, _ := labels["domain"].(string)
				log.Printf("Removing mapping by triple: domain=%s, rule_id=%s, instance_id=%s, target_device=%s",
					domain, ruleID, instanceID, labels["target_device"])
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

	err = WriteFile(matchedVMsFile, matchedVMsData, 0644)
	if err != nil {
		log.Printf("Failed to write matched_vms.json: %v", err)
		return err
	}

	// Force reload Prometheus configuration
	if err := ReloadPrometheusViaHTTP(); err != nil {
		log.Printf("Warning: Failed to reload Prometheus after updating matched_vms.json: %v", err)
		// Don't return error as the file update was successful
	} else {
		log.Printf("Successfully reloaded Prometheus configuration after updating matched_vms.json")
	}

	return nil
}
