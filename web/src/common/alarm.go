package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"
	"web/src/model"
	"web/src/utils/log"

	"github.com/google/uuid"
	"github.com/spf13/viper"

	"github.com/jinzhu/gorm"
)

const (
	RuleTypeCPU  = "cpu"
	RuleTypeBW   = "bw"
	RulesEnabled = "/etc/prometheus/rules_enabled"
	RulesGeneral = "/etc/prometheus/general_rules"
	RulesSpecial = "/etc/prometheus/special_rules"
)

var (
	alarmLogger            = log.MustGetLogger("alarm")
	alarmPrometheusIP      string
	alarmPrometheusPort    int
	alarmPrometheusSSHPort int
	isRemotePrometheus     bool
	sshKeyPath             string
	prometheusClient       *PrometheusClient
)

type PrometheusClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

type RuleFileRequest struct {
	Operation string `json:"operation"` // 操作类型: write, symlink, chown, reload, delete
	FileUser  string `json:"file_user"` // 文件所有者用户名
	Content   string `json:"content"`   // 规则文件内容
	FilePath  string `json:"file_path"` // 文件路径
	LinkPath  string `json:"link_path"` // 链接路径(用于symlink)
}

// RuleFileResponse 表示规则文件操作响应
type RuleFileResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type ListRuleGroupsParams struct {
	RuleType  string
	Page      int
	PageSize  int
	GroupUUID string
}

// 在文件顶部添加以下结构体定义（约在23行附近）
type (
	// 虚拟机关联表
	VMRuleLink struct {
		ID        uint      `gorm:"primaryKey;autoIncrement"`
		GroupUUID string    `gorm:"column:group_uuid;type:varchar(36);index;not null"`
		VMName    string    `gorm:"type:varchar(255);index;not null"`
		CreatedAt time.Time `gorm:"autoCreateTime"`
	}

	// 规则组结构
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

	// CPU规则表
	CPURule struct {
		ID           int       `gorm:"primaryKey;autoIncrement"`
		GroupUUID    string    `gorm:"column:group_uuid;type:varchar(36);index"`
		Name         string    `gorm:"size:255"`
		Duration     int       `gorm:"check:duration >= 1"`
		Over         int       `json:"over" gorm:"check:over >= 1"`                   // 对应请求参数中的 over
		DownTo       int       `json:"down_to" gorm:"check:down_to >= 0"`             // 对应请求参数中的 down_to
		DownDuration int       `json:"down_duration" gorm:"check:down_duration >= 1"` // 对应请求参数中的 down_duration
		CreatedAt    time.Time `gorm:"autoCreateTime"`
	}

	// 带宽规则表
	BWRule struct {
		ID        uint   `gorm:"primaryKey;autoIncrement"`
		GroupUUID string `gorm:"column:group_uuid;type:varchar(36);index"`
		Name      string `gorm:"size:255"`

		// 入站参数
		InEnabled      bool   `gorm:"default:false"` // 标记入站规则是否启用
		InThreshold    int    `gorm:"check:in_threshold >= 0"`
		InDuration     int    `gorm:"check:in_duration >= 0"`
		InOverType     string `gorm:"type:varchar(20);default:'absolute'"`
		InDownTo       int    `gorm:"default:0"`
		InDownDuration int    `gorm:"default:0"`

		// 出站参数
		OutEnabled      bool   `gorm:"default:false"` // 标记出站规则是否启用
		OutThreshold    int    `gorm:"check:out_threshold >= 0"`
		OutDuration     int    `gorm:"check:out_duration >= 0"`
		OutOverType     string `gorm:"type:varchar(20);default:'absolute'"`
		OutDownTo       int    `gorm:"default:0"`
		OutDownDuration int    `gorm:"default:0"`

		CreatedAt time.Time `gorm:"autoCreateTime"`
	}

	Alert struct {
		ID           uint   `gorm:"primaryKey;autoIncrement"`
		Name         string `gorm:"size:255"`
		Status       string `gorm:"type:varchar(20)"`
		InstanceUUID string `gorm:"type:varchar(36);index"`
		Severity     string `gorm:"type:varchar(20)"`
		Summary      string `gorm:"type:text"`
		Description  string `gorm:"type:text"`
		StartsAt     time.Time
		EndsAt       time.Time
		CreatedAt    time.Time `gorm:"autoCreateTime"`
	}
)

type AlarmOperator struct {
	DB *gorm.DB
}

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
		baseURL := fmt.Sprintf("http://%s:%d", alarmPrometheusIP, alarmPrometheusPort)
		certFile := viper.GetString("monitor.cert")
		keyFile := viper.GetString("monitor.key")
		client, err := NewPrometheusClient(baseURL, certFile, keyFile)
		if err != nil {
			alarmLogger.Errorf("初始化Prometheus客户端失败: %v", err)
		} else {
			prometheusClient = client
			alarmLogger.Infof("Prometheus客户端初始化成功，基础URL: %s", baseURL)
		}
	}
	alarmLogger.Infof("Prometheus配置: IP=%s, 端口=%d, SSH端口=%d, 远程模式=%v",
		alarmPrometheusIP, alarmPrometheusPort, alarmPrometheusSSHPort, isRemotePrometheus)
	fmt.Printf("wngzhe alarm Prometheus配置: IP=%s, 端口=%d, SSH端口=%d, 远程模式=%v",
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
	ctx, db := GetContextDB(ctx)
	return db.Where("group_uuid = ?", groupUUID).Find(rules).Error
}

func (a *AlarmOperator) GetRulesByGroupUUID(ctx context.Context, groupUUID string) (*model.RuleGroupV2, error) {
	//fmt.Printf("groupUUID is %s", groupUUID)
	groups, _, err := a.ListRuleGroups(ctx, ListRuleGroupsParams{
		GroupUUID: groupUUID,
		PageSize:  1,
	})
	if err != nil || len(groups) == 0 {
		alarmLogger.Error("rules query failed", "groupID", groupUUID, "error", err)
		return nil, fmt.Errorf("rules query failed: %w", err)
	}

	// 获取规则类型
	ruleType := groups[0].Type

	// 根据规则类型获取详细规则
	if ruleType == "cpu" {
		// CPU 类型规则
		details, err := a.GetCPURuleDetails(ctx, groupUUID)
		if err != nil {
			alarmLogger.Error("detail rules query failed", "groupID", groupUUID, "error", err)
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
		// 带宽类型规则
		details, err := a.GetBWRuleDetails(ctx, groupUUID)
		if err != nil {
			alarmLogger.Error("detail rules query failed", "groupID", groupUUID, "error", err)
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
		// 不支持的规则类型
		alarmLogger.Error("unsupported rule type", "groupID", groupUUID, "type", ruleType)
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
		alarmLogger.Error("rules query failed", "groupID", groupUUID, "error", err)
		return nil, fmt.Errorf("rules query failed: %w", err)
	}

	details, err := a.GetCPURuleDetails(ctx, groupUUID)
	if err != nil {
		alarmLogger.Error("detail rules query failed", "groupID", groupUUID, "error", err)
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
		alarmLogger.Error("rules query failed", "groupID", groupUUID, "error", err)
		return nil, fmt.Errorf("rules query failed: %w", err)
	}

	details, err := a.GetBWRuleDetails(ctx, groupUUID)
	if err != nil {
		alarmLogger.Error("detail rules query failed", "groupID", groupUUID, "error", err)
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
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.RuleGroupV2{}).
			Where("uuid = ?", groupID).
			Update("enabled", enabled)
		if result.Error != nil {
			alarmLogger.Error("update satus failed", "groupID", groupID, "error", result.Error)
			return fmt.Errorf("update satus failed: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("group rules no found")
		}
		return nil
	})
}

func (a *AlarmOperator) BatchLinkVMs(ctx context.Context, GroupUUID string, vmUUIDs []string, iface string) error {
	ctx, db := GetContextDB(ctx)
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
					alarmLogger.Error("create link failed",
						"GroupUUID", GroupUUID,
						"vmUUID", vmUUID,
						"interface", iface,
						"error", err)
					return fmt.Errorf("create link failed: %w", err)
				}
			} else {
				alarmLogger.Info("link already exists, skipping",
					"GroupUUID", GroupUUID,
					"vmUUID", vmUUID,
					"interface", iface)
			}
		}
		return nil
	})
}

// 在 AlarmOperator 结构体添加以下方法
func (a *AlarmOperator) DeleteRuleGroup(ctx context.Context, groupUUID, ruleType string) error {
	ctx, db := GetContextDB(ctx)
	result := db.Where("uuid = ? AND type = ?", groupUUID, ruleType).
		Delete(&model.RuleGroupV2{})
	if result.Error != nil {
		alarmLogger.Error("delete rule failed", "groupUUID", groupUUID, "type", ruleType, "error", result.Error)
	}
	return result.Error
}

func (a *AlarmOperator) DeleteVMLink(ctx context.Context, groupUUID, vmUUID, iface string) (int64, error) {
	ctx, db := GetContextDB(ctx)
	query := db.Where("group_uuid = ? AND vm_uuid = ?", groupUUID, vmUUID)

	// 如果指定了接口，则只删除该接口的链接
	if iface != "" {
		query = query.Where("interface = ?", iface)
	}

	result := query.Delete(&model.VMRuleLink{})
	if result.Error != nil {
		alarmLogger.Error("delete link failed",
			"groupUUID", groupUUID,
			"vmUUID", vmUUID,
			"interface", iface,
			"error", result.Error)
	}
	return result.RowsAffected, result.Error
}

func (a *AlarmOperator) GetLinkedVMs(ctx context.Context, groupUUID string) ([]model.VMRuleLink, error) {
	ctx, db := GetContextDB(ctx)
	var links []model.VMRuleLink
	query := db.Model(&model.VMRuleLink{})

	// 添加条件判断
	if groupUUID != "" {
		query = query.Where("group_uuid = ?", groupUUID)
	} else {
		alarmLogger.Debug("query all goup found, TBD")
	}

	if err := query.Find(&links).Error; err != nil {
		alarmLogger.Error("get link data failed",
			"groupUUID", groupUUID,
			"error", err)
		return nil, err
	}
	return links, nil
}

func (a *AlarmOperator) DeleteRuleGroupWithDependencies(ctx context.Context, groupUUID, ruleType string) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		// delete detail db
		switch ruleType {
		case "cpu":
			if err := tx.Where("group_uuid = ?", groupUUID).
				Delete(&model.CPURuleDetail{}).Error; err != nil {
				alarmLogger.Error("CPU rules delete failed", "group_uuid", groupUUID, "error", err)
				return fmt.Errorf("CPU rules delete failed: %w", err)
			}
		case "bw":
			if err := tx.Where("group_uuid = ?", groupUUID).
				Delete(&model.BWRuleDetail{}).Error; err != nil {
				alarmLogger.Error("bw rules delete failed", "group_uuid", groupUUID, "error", err)
				return fmt.Errorf("bw rules delete failed: %w", err)
			}
		default:
			return fmt.Errorf("unknow type: %s", ruleType)
		}
		// delete link db
		if err := tx.Where("group_uuid = ?", groupUUID).
			Delete(&model.VMRuleLink{}).Error; err != nil {
			alarmLogger.Error("failed to del vm link", "groupUUID", groupUUID, "error", err)
			return fmt.Errorf("failed to del vm link: %w", err)
		}
		// delete group rule db
		if err := tx.Where("uuid = ? AND type = ?", groupUUID, ruleType).
			Delete(&model.RuleGroupV2{}).Error; err != nil {
			alarmLogger.Error("group del failed", "groupUUID", groupUUID, "error", err)
			return fmt.Errorf("group del failed: %w", err)
		}

		return nil
	})
}

// 补充分页函数实现
func Paginate(page, pageSize int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		offset := (page - 1) * pageSize
		return db.Offset(offset).Limit(pageSize)
	}
}

func (a *AlarmOperator) DeleteCPURulesByGroup(ctx context.Context, groupID string) error {
	ctx, db := GetContextDB(ctx)
	if err := db.Where("group_uuid = ?", groupID).
		Delete(&CPURule{}).Error; err != nil { // 修改为本地结构体
		alarmLogger.Error("CPU rule delete failed", "groupID", groupID, "error", err)
		return err
	}
	return nil
}

func (a *AlarmOperator) ListRuleGroups(ctx context.Context, params ListRuleGroupsParams) ([]model.RuleGroupV2, int64, error) {
	ctx, db := GetContextDB(ctx)
	var groups []model.RuleGroupV2
	var total int64

	// 构建基础查询
	query := db.Model(&model.RuleGroupV2{})
	if params.RuleType != "" {
		query = query.Where("type = ?", params.RuleType)
	}
	if params.GroupUUID != "" {
		query = query.Where("uuid = ?", params.GroupUUID)
	}

	if err := query.Count(&total).Error; err != nil {
		alarmLogger.Error("get rules count failed",
			"ruleType", params.RuleType,
			"error", err)
		return nil, 0, fmt.Errorf("get rules count failed: %w", err)
	}
	//fmt.Printf("query.Count no error")
	// 执行分页查询
	if err := query.Scopes(Paginate(params.Page, params.PageSize)).
		Find(&groups).Error; err != nil {
		alarmLogger.Error("page query failed",
			"ruleType", params.RuleType,
			"page", params.Page,
			"pageSize", params.PageSize,
			"error", err)
		return nil, 0, fmt.Errorf("page query failed: %w", err)
	}

	return groups, total, nil
}

func (a *AlarmOperator) GetCPURuleDetails(ctx context.Context, groupUUID string) ([]model.CPURuleDetail, error) {
	ctx, db := GetContextDB(ctx)
	var details []model.CPURuleDetail
	if err := db.Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		alarmLogger.Error("query CPU rules detail failed",
			"groupUUID", groupUUID,
			"error", err)
		return nil, fmt.Errorf("query CPU rules detail failed: %w", err)
	}
	return details, nil
}

// 新增触发次数更新方法
func (a *AlarmOperator) IncrementTriggerCount(ctx context.Context, groupID string) error {
	ctx, db := GetContextDB(ctx)
	return db.Model(&model.RuleGroupV2{}).
		Where("uuid = ?", groupID).
		Update("trigger_cnt", gorm.Expr("trigger_cnt + 1")).Error
}

func (a *AlarmOperator) CreateCPURules(ctx context.Context, groupUUID string, rules []CPURule) error {
	ctx, db := GetContextDB(ctx)
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
				alarmLogger.Error("create cpu rule failed",
					"groupUUID", groupUUID,
					"rule", rules[i],
					"error", err)
				return fmt.Errorf("create cpu rule failed: %w", err)
			}
		}
		return nil
	})
}

func (a *AlarmOperator) CreateBWRuleDetail(ctx context.Context, detail *model.BWRuleDetail) error {
	ctx, db := GetContextDB(ctx)
	if err := db.Create(detail).Error; err != nil {
		alarmLogger.Error("create bw rule detail failed",
			"groupUUID", detail.GroupUUID,
			"name", detail.Name,
			"error", err)
		return fmt.Errorf("create bw rule detail failed: %w", err)
	}
	return nil
}

func (a *AlarmOperator) GetBWRuleDetails(ctx context.Context, groupUUID string) ([]model.BWRuleDetail, error) {
	ctx, db := GetContextDB(ctx)
	var details []model.BWRuleDetail
	if err := db.Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		alarmLogger.Error("query db BW rules detailed",
			"groupUUID", groupUUID,
			"error", err)
		return nil, fmt.Errorf("query db BW rules detailed: %w", err)
	}
	return details, nil
}

func (a *AlarmOperator) CreateRuleGroup(ctx context.Context, group *model.RuleGroupV2) error {
	ctx, db := GetContextDB(ctx)
	if err := db.Create(group).Error; err != nil {
		alarmLogger.Error("failed to create rule",
			"UUID", uuid.New().String(),
			"GroupUUID", group.UUID,
			"error", err)
		return fmt.Errorf("failed to create rule: %w", err)
	}
	return nil
}

func (a *AlarmOperator) CreateCPURuleDetail(ctx context.Context, detail *model.CPURuleDetail) error {
	ctx, db := GetContextDB(ctx)
	detail.UUID = uuid.NewString()
	if err := db.Create(detail).Error; err != nil {
		alarmLogger.Error("create cpu rule detail failed",
			"groupUUID", detail.GroupUUID,
			"ruleName", detail.Name,
			"error", err)
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
		alarmLogger.Error("get local network configuration failed: %v", err)
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

func NewPrometheusClient(baseURL, certFile, keyFile string) (*PrometheusClient, error) {
	var client *http.Client

	// 检查是否使用HTTPS
	if certFile != "" && keyFile != "" {
		// 加载客户端证书
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("加载证书失败: %v", err)
		}

		// 创建TLS配置
		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: false,
		}

		// 如果存在CA证书，加载它
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
		// 使用普通HTTP客户端
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
	// 将请求转换为JSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("请求序列化失败: %v", err)
	}

	// 构建请求URL
	url := c.BaseURL + endpoint

	// 创建HTTP请求
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建HTTP请求失败: %v", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return respBody, fmt.Errorf("服务器返回错误状态码: %d, 响应: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// 为了保持兼容性，添加一个不返回响应的版本
func (c *PrometheusClient) sendRequestNoResponse(endpoint string, req RuleFileRequest) error {
	_, err := c.sendRequest(endpoint, req)
	return err
}

// WriteRuleFile 向远程服务器写入规则文件
func (c *PrometheusClient) ClientWriteRuleFile(path string, content []byte, perm os.FileMode) error {
	req := RuleFileRequest{
		Operation: "write",
		FilePath:  path,
		Content:   string(content),
		FileUser:  "prometheus", // 默认使用prometheus用户
	}

	err := c.sendRequestNoResponse("/api/v1/rules/file", req)
	if err != nil {
		alarmLogger.Errorf("prometheus server create file failed: %v", err)
	}
	return err
}

// CreateSymlink 在远程服务器上创建符号链接
func (c *PrometheusClient) ClientCreateSymlink(target, link string) error {
	req := RuleFileRequest{
		Operation: "symlink",
		FilePath:  target,
		LinkPath:  link,
		FileUser:  "prometheus", // 默认使用prometheus用户
	}

	err := c.sendRequestNoResponse("/api/v1/rules/symlink", req)
	if err != nil {
		alarmLogger.Errorf("prometheus server create link failed: %v", err)
	}
	return err
}

// SetFileOwner 设置远程服务器上文件的所有者
func (c *PrometheusClient) ClientSetFileOwner(path string) error {
	req := RuleFileRequest{
		Operation: "chown",
		FilePath:  path,
		FileUser:  "prometheus",
	}

	err := c.sendRequestNoResponse("/api/v1/rules/chown", req)
	if err != nil {
		alarmLogger.Errorf("prometheus server create link failed: %v", err)
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
		alarmLogger.Errorf("prometheus server set link owner failed: %v", err)
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
		alarmLogger.Errorf("prometheus server remove file failed: %v", err)
	}
	return err
}

func (c *PrometheusClient) ClientRemoveSymlink(linkPath string) error {
	req := RuleFileRequest{
		Operation: "unlink",
		LinkPath:  linkPath,
	}

	err := c.sendRequestNoResponse("/api/v1/rules/file", req)
	if err != nil {
		alarmLogger.Errorf("prometheus server remove symlink failed: %v", err)
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

// ReloadPrometheus 重新加载远程Prometheus配置
func (c *PrometheusClient) ClientReloadPrometheus() error {
	req := RuleFileRequest{
		Operation: "reload",
	}

	return c.sendRequestNoResponse("/api/v1/rules/reload", req)
}

func GetUser(username string) (uid, gid int, err error) {
	if isRemotePrometheus {
		// Get user ID from remote server
		uid, gid, err := prometheusClient.ClientGetUser("prometheus")
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

// WriteFile 写入文件内容
func WriteFile(path string, content []byte, perm os.FileMode) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
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

// SetFileOwner 设置文件所有者
func SetFileOwner(path string, uid, gid int) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
		}

		return prometheusClient.ClientSetFileOwner(path)
	} else {
		return os.Chown(path, uid, gid)
	}
}

// SetSymlinkOwner 设置符号链接所有者
func SetSymlinkOwner(path string, uid, gid int) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
		}

		return prometheusClient.ClientSetSymlinkOwner(path)
	} else {
		uid, gid, err := GetUser("prometheus")
		if err != nil {
			return os.Lchown(path, uid, gid)
		} else {
			alarmLogger.Error("Prometheus server set link owenr failed with %s", err)
			return err
		}
	}
}

// CreateSymlink 创建符号链接
func CreateSymlink(target, link string) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
		}

		return prometheusClient.ClientCreateSymlink(target, link)
	} else {
		os.Symlink(target, link)
		uid, gid, err := GetUser("prometheus")
		if err != nil {
			return err
		}
		return SetSymlinkOwner(link, uid, gid)

	}
}

// RemoveSymlink 删除符号链接
func RemoveSymlink(link string) error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
		}

		return prometheusClient.ClientRemoveSymlink(link)
	} else {
		// 检查链接是否存在
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			return nil // 链接不存在，视为成功
		}
		return os.Remove(link)
	}
}

func ReloadPrometheus() error {
	if isRemotePrometheus {
		if prometheusClient == nil {
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
		}

		return prometheusClient.ClientReloadPrometheus()
	} else {
		cmd := exec.Command("sudo", "systemctl", "kill", "-s", "SIGHUP", "prometheus.service")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("SIGHUP操作失败: %v, 输出: %s", err, string(output))
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
			alarmLogger.Error("Prometheus客户端未初始化")
			return fmt.Errorf("Prometheus客户端未初始化")
		}

		return prometheusClient.ClientRemoveRuleFile(path)
	} else {
		// 检查文件是否存在
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil // 文件不存在，视为成功
		}
		return os.Remove(path)
	}
}
