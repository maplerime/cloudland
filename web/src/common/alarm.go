package common

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
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
)

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

/*
func (a *AlarmOperator) DeleteVMLink1(ctx context.Context, groupUUID, vmUUID, ruleType string) (int64, error) {
	ctx, db := GetContextDB(ctx)
	result := db.Where("group_uuid = ? AND vm_uuid = ?", groupUUID, vmUUID).
		Delete(&model.VMRuleLink{})
	if result.Error != nil {
		alarmLogger.Error("delete link failed",
			"groupUUID", groupUUID,
			"vmUUID", vmUUID,
			"error", result.Error)
	}
	return result.RowsAffected, result.Error
}*/

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
	if err := db.Where("group_id = ?", groupID).
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
		alarmLogger.Error("page qurey failed",
			"ruleType", params.RuleType,
			"page", params.Page,
			"pageSize", params.PageSize,
			"error", err)
		return nil, 0, fmt.Errorf("page qurey failed: %w", err)
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
		Where("id = ?", groupID).
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
func ExecuteRemoteCommand(command string) (string, error) {
	sshCmd := fmt.Sprintf("ssh -p %d -i %s -o StrictHostKeyChecking=no root@%s '%s'",
		alarmPrometheusSSHPort, sshKeyPath, alarmPrometheusIP, command)

	cmd := exec.Command("bash", "-c", sshCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("远程命令执行失败: %v, 输出: %s", err, string(output))
	}

	return string(output), nil
}
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if isRemotePrometheus {
		// 创建目录（如果需要）
		dirPath := filepath.Dir(path)
		mkdirCmd := fmt.Sprintf("mkdir -p %s", dirPath)
		if _, err := ExecuteRemoteCommand(mkdirCmd); err != nil {
			return fmt.Errorf("创建远程目录失败: %v", err)
		}

		// 写入临时文件
		tempFile, err := os.CreateTemp("", "prometheus_rule")
		if err != nil {
			return fmt.Errorf("创建临时文件失败: %v", err)
		}
		defer os.Remove(tempFile.Name())

		if _, err := tempFile.Write(data); err != nil {
			return fmt.Errorf("写入临时文件失败: %v", err)
		}
		tempFile.Close()

		// 使用scp上传文件
		scpCmd := fmt.Sprintf("scp -P %d -i %s -o StrictHostKeyChecking=no %s root@%s:%s",
			alarmPrometheusSSHPort, sshKeyPath, tempFile.Name(), alarmPrometheusIP, path)
		cmd := exec.Command("bash", "-c", scpCmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("上传文件失败: %v, 输出: %s", err, string(output))
		}

		// 设置权限
		chmodCmd := fmt.Sprintf("chmod %o %s", perm, path)
		if _, err := ExecuteRemoteCommand(chmodCmd); err != nil {
			return fmt.Errorf("设置文件权限失败: %v", err)
		}

		return nil
	} else {
		// 本地文件操作
		return os.WriteFile(path, data, perm)
	}
}

// 原子写入文件
func AtomicWrite(path string, content string) error {
	tempPath := path + ".tmp"
	if err := WriteFile(tempPath, []byte(content), 0640); err != nil {
		return err
	}

	if isRemotePrometheus {
		mvCmd := fmt.Sprintf("mv %s %s", tempPath, path)
		_, err := ExecuteRemoteCommand(mvCmd)
		return err
	} else {
		return os.Rename(tempPath, path)
	}
}

// 设置文件所有者（本地或远程）
func SetFileOwner(path string, uid, gid int) error {
	if isRemotePrometheus {
		chownCmd := fmt.Sprintf("chown %d:%d %s", uid, gid, path)
		_, err := ExecuteRemoteCommand(chownCmd)
		return err
	} else {
		return os.Chown(path, uid, gid)
	}
}

// 设置符号链接所有者（本地或远程）
func SetSymlinkOwner(path string, uid, gid int) error {
	if isRemotePrometheus {
		chownCmd := fmt.Sprintf("chown -h %d:%d %s", uid, gid, path)
		_, err := ExecuteRemoteCommand(chownCmd)
		return err
	} else {
		return os.Lchown(path, uid, gid)
	}
}

func CreateSymlink(target, link string) error {
	if isRemotePrometheus {
		// 先检查链接是否已存在
		checkCmd := fmt.Sprintf("test -e %s && echo 'exists'", link)
		output, _ := ExecuteRemoteCommand(checkCmd)
		if strings.TrimSpace(output) == "exists" {
			return os.ErrExist
		}

		// 创建符号链接
		linkCmd := fmt.Sprintf("ln -s %s %s", target, link)
		_, err := ExecuteRemoteCommand(linkCmd)
		return err
	} else {
		return os.Symlink(target, link)
	}
}

func RemoveFile(path string) error {
	if isRemotePrometheus {
		rmCmd := fmt.Sprintf("rm -f %s", path)
		_, err := ExecuteRemoteCommand(rmCmd)
		return err
	} else {
		return os.Remove(path)
	}
}

func GetUser(username string) (uid, gid int, err error) {
	if isRemotePrometheus {
		// Get user ID from remote server
		output, err := ExecuteRemoteCommand(fmt.Sprintf("id -u %s", username))
		if err != nil {
			return 0, 0, fmt.Errorf("failed to get remote user ID for %s: %v", username, err)
		}
		uid, err = strconv.Atoi(strings.TrimSpace(output))
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse remote user ID for %s: %v", username, err)
		}

		// Get group ID from remote server
		output, err = ExecuteRemoteCommand(fmt.Sprintf("id -g %s", username))
		if err != nil {
			return 0, 0, fmt.Errorf("failed to get remote group ID for %s: %v", username, err)
		}
		gid, err = strconv.Atoi(strings.TrimSpace(output))
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse remote group ID for %s: %v", username, err)
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

func ReloadPrometheus() error {
	if isRemotePrometheus {
		_, err := ExecuteRemoteCommand("systemctl kill -s SIGHUP prometheus.service")
		return err
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
