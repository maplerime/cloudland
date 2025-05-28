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
	RuleTypeCPU       = "cpu"
	RuleTypeBW        = "bw"
	RuleTypeCompute   = "compute_node"
	RuleTypeControl   = "control_node"
	RuleTypeAvailable = "node_available"
	RulesEnabled      = "/etc/prometheus/rules_enabled"
	RulesGeneral      = "/etc/prometheus/general_rules"
	RulesSpecial      = "/etc/prometheus/special_rules"
	RulesNode         = "/etc/prometheus/node_rules"
	RuleTemplate      = "/etc/prometheus/node_templates"
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
		Type       string    `gorm:"type:varchar(10);index"` // cpu/bw
		Enabled    bool      `gorm:"default:true"`
		Owner      string    `gorm:"type:varchar(255);index"`
		CreatedAt  time.Time `gorm:"autoCreateTime"`
		TriggerCnt int       `gorm:"default:0"`
		UpdatedAt  time.Time
	}

	CPURule struct {
		ID           int       `gorm:"primaryKey;autoIncrement"`
		GroupUUID    string    `gorm:"column:group_uuid;type:varchar(36);index"`
		Name         string    `gorm:"size:255"`
		Duration     int       `gorm:"check:duration >= 1"`
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
		Severity      string `gorm:"type:varchar(20)"`
		Summary       string `gorm:"type:text"`
		Description   string `gorm:"type:text"`
		StartsAt      time.Time
		EndsAt        time.Time
		CreatedAt     time.Time `gorm:"autoCreateTime"`
		AlertType     string    `gorm:"type:varchar(20)" json:"alert_type"`
		TargetDevice  string    `gorm:"type:varchar(255)" json:"target_device"`
	}
)

// 完善的配置结构
type NodeAvailabilityConfig struct {
	NodeDownDuration     string `json:"node_down_duration"`
	AlertDurationMinutes int    `json:"alert_duration_minutes"`
}

type ManagementConfig struct {
	// CPU监控
	CPUUsageThreshold int    `json:"cpu_usage_threshold"`
	CPUAlertDuration  string `json:"cpu_alert_duration"`
	CPUAlertMinutes   int    `json:"cpu_alert_minutes"`

	// 内存监控
	MemoryUsageThreshold int    `json:"memory_usage_threshold"`
	MemoryAlertDuration  string `json:"memory_alert_duration"`
	MemoryAlertMinutes   int    `json:"memory_alert_minutes"`

	// 磁盘监控
	DiskSpaceThreshold int    `json:"disk_space_threshold"`
	DiskAlertDuration  string `json:"disk_alert_duration"`
	DiskAlertMinutes   int    `json:"disk_alert_minutes"`

	// 网络监控 - 新增
	NetworkTrafficThresholdGB float64 `json:"network_traffic_threshold_gb"`
	NetworkAlertDuration      string  `json:"network_alert_duration"`
	NetworkAlertMinutes       int     `json:"network_alert_minutes"`
}

type ComputeConfig struct {
	// CPU监控
	CPUUsageThreshold int    `json:"cpu_usage_threshold"`
	CPUAlertDuration  string `json:"cpu_alert_duration"`
	CPUAlertMinutes   int    `json:"cpu_alert_minutes"`

	// 内存监控 - 新增
	MemoryUsageThreshold int    `json:"memory_usage_threshold"`
	MemoryAlertDuration  string `json:"memory_alert_duration"`
	MemoryAlertMinutes   int    `json:"memory_alert_minutes"`

	// 磁盘监控
	DiskSpaceThreshold int    `json:"disk_space_threshold"`
	DiskAlertDuration  string `json:"disk_alert_duration"`
	DiskAlertMinutes   int    `json:"disk_alert_minutes"`

	// 核心网络监控
	NetworkTrafficThresholdGB float64 `json:"network_traffic_threshold_gb"`
	NetworkAlertDuration      string  `json:"network_alert_duration"`
	NetworkAlertMinutes       int     `json:"network_alert_minutes"`

	// 多业务类型网络监控
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
	if err != nil || len(groups) == 0 {
		log.Printf("rules query failed: groupID=%s, error=%v", groupUUID, err)
		return nil, fmt.Errorf("rules query failed: %w", err)
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
				return nil, fmt.Errorf("读取CA证书失败: %v", err)
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

	// 解析响应
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
		Operation: "unlink",
		LinkPath:  linkPath,
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
		return 0, 0, fmt.Errorf("解析响应失败: %v", err)
	}

	if !response.Success {
		return 0, 0, fmt.Errorf("获取用户信息失败: %s", response.Message)
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
	if isRemotePrometheus {
		if prometheusClient == nil {
			log.Printf("The Prometheus client has not been initialized.")
			return nil, fmt.Errorf("The Prometheus client has not been initialized.")
		}
		return prometheusClient.ClientReadRuleFile(path)
	} else {
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

func RulePaths(ruleType, groupID string) (generalPath string, specialPath string) {
	const (
		RulesGeneral = "/etc/prometheus/general_rules"
		RulesSpecial = "/etc/prometheus/special_rules"
	)
	return fmt.Sprintf("%s/%s-general-%s.yml", RulesGeneral, ruleType, groupID),
		fmt.Sprintf("%s/%s-special-%s.yml", RulesSpecial, ruleType, groupID)
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
	outputPath := filepath.Join(RulesNode, outputFile)

	// Read template content
	templateContent, err := ReadFile(templatePath)
	if err != nil {
		log.Printf("Failed to read template file: path=%s, error=%v", templatePath, err)
		return fmt.Errorf("failed to read template file %s: %w", templatePath, err)
	}

	var renderedContent string
	if strings.Contains(string(templateContent), "name: compute-network-resources") {
		renderedContent, err = renderNetworkResourcesTemplate(data)
	} else {
		renderedContent, err = renderTemplateContent(string(templateContent), data, templateFile)
	}
	if err != nil {
		log.Printf("Failed to render template: template=%s, error=%v", templateFile, err)
		return fmt.Errorf("failed to render template %s: %w", templateFile, err)
	}

	// Write rendered content to output file
	if err := WriteFile(outputPath, []byte(renderedContent), 0640); err != nil {
		log.Printf("Failed to write output file: path=%s, error=%v", outputPath, err)
		return fmt.Errorf("failed to write output file %s: %w", outputPath, err)
	}

	// Create symlink to RulesEnabled directory
	enabledPath := filepath.Join(RulesEnabled, outputFile)
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

	// Replace all variables with data values first
	for key, value := range data {
		// Skip Prometheus labels (e.g., {{ $labels.xxx }})
		if strings.HasPrefix(key, "$labels.") {
			continue
		}

		// Replace simple variables: {{ variable }}
		placeholder := fmt.Sprintf("{{ %s }}", key)
		strValue := fmt.Sprintf("%v", value)
		result = strings.ReplaceAll(result, placeholder, strValue)

		// Handle variables with default values: {{ variable | default("value") }} with data value
		defaultPattern := regexp.MustCompile(fmt.Sprintf(`{{ %s \| default\("([^"]*)"\) }}`, key))
		result = defaultPattern.ReplaceAllString(result, strValue)

		// Handle variables with unquoted default values: {{ variable | default(value) }} or {{ (variable | default(value)) }}
		noQuotesPattern := regexp.MustCompile(fmt.Sprintf(`{{ *(?:\(?%s\)? *\| *default\(([^"\)]+)\)) *}}`, key))
		result = noQuotesPattern.ReplaceAllString(result, strValue)
	}

	// Handle remaining default value variables (use template defaults for missing keys)
	// For quoted defaults: {{ variable | default("value") }}
	defaultPattern := regexp.MustCompile(`{{ ([a-zA-Z0-9_]+) \| default\("([^"]*)"\) }}`)
	result = defaultPattern.ReplaceAllStringFunc(result, func(match string) string {
		matches := defaultPattern.FindStringSubmatch(match)
		if len(matches) != 3 {
			return match
		}
		key := matches[1]
		// Skip Prometheus labels
		if strings.HasPrefix(key, "$labels.") {
			return match
		}
		// Use data value if available, otherwise use default from template
		if _, ok := data[key]; ok {
			return fmt.Sprintf("%v", data[key])
		}
		return matches[2] // Use the default value from template
	})

	// For unquoted defaults: {{ variable | default(value) }} or {{ (variable | default(value)) }}
	noQuotesPattern := regexp.MustCompile(`{{ *(?:\(?([a-zA-Z0-9_]+)\)? *\| *default\(([^"\)]+)\)) *}}`)
	result = noQuotesPattern.ReplaceAllStringFunc(result, func(match string) string {
		matches := noQuotesPattern.FindStringSubmatch(match)
		if len(matches) != 3 {
			return match
		}
		key := matches[1]
		// Skip Prometheus labels
		if strings.HasPrefix(key, "$labels.") {
			return match
		}
		// Use data value if available, otherwise use default from template
		if _, ok := data[key]; ok {
			return fmt.Sprintf("%v", data[key])
		}
		return matches[2] // Use the default value from template
	})

	// Validate the result: ensure no unresolved templates remain (except Prometheus labels)
	remainingPattern := regexp.MustCompile(`{{ ([^}]+) }}`)
	remainingMatches := remainingPattern.FindAllStringSubmatch(result, -1)
	for _, match := range remainingMatches {
		if len(match) > 1 {
			key := match[1]
			// Allow Prometheus labels to remain
			if !strings.HasPrefix(key, "$labels.") {
				return "", fmt.Errorf("unresolved template variable: %s", key)
			}
		}
	}

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

	// 验证 config 是否为有效的 JSON
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

	if err := ReloadPrometheus(); err != nil {
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
		// ... existing error handling ...
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

	// 添加模板所需的数据
	c.Data["Rules"] = rulesForTemplate
	c.Data["Total"] = len(rules)
	c.Data["Query"] = c.Query("q")
	c.Data["Link"] = "/alarms/node"

	// 确保 i18n 对象被正确设置
	if c.Locale != nil {
		c.Data["i18n"] = c.Locale
	} else {
		log.Printf("Warning: i18n object is nil")
		c.Data["i18n"] = &i18n.Locale{} // 提供一个空的 i18n 对象作为后备
	}

	// 复制其他必要的上下文数据
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
	case "service_monitoring":
		templateFiles = []string{"service_monitoring.yml"}
	}

	deletedFiles := []string{}
	for _, templateFile := range templateFiles {
		outputPath := filepath.Join(RulesNode, templateFile)
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

	if err := ReloadPrometheus(); err != nil {
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
