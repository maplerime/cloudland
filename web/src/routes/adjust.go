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

// 不需要重复声明instanceAdmin，已在instance.go中定义

// findInterfaceByTargetDevice 通过target_device找到对应的接口
// target_device格式: tapXXXXXX (tap + MAC地址后6位无冒号)
// 例如: tapdb4c44 对应 MAC 52:54:21:db:4c:44
func findInterfaceByTargetDevice(instance *model.Instance, targetDevice string) *model.Interface {
	if !strings.HasPrefix(targetDevice, "tap") || len(targetDevice) != 9 {
		log.Printf("Invalid target_device format: %s, expected tapXXXXXX", targetDevice)
		return nil
	}

	// 提取MAC地址后6位
	macSuffix := targetDevice[3:] // 去掉 "tap" 前缀

	for _, iface := range instance.Interfaces {
		if iface.MacAddr == "" {
			continue
		}

		// 提取MAC地址后6位（去掉冒号）
		macParts := strings.Split(iface.MacAddr, ":")
		if len(macParts) >= 3 {
			// 取最后3段，去掉冒号
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

// getAdminPassword 从配置文件读取admin密码
func getAdminPassword() string {
	// 设置配置文件路径并读取配置文件
	viper.SetConfigFile("conf/config.toml")
	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Failed to read config file, using default password: %v", err)
		return "passw0rd" // 默认密码，与admin.go中保持一致
	}

	password := viper.GetString("admin.password")
	if password == "" {
		password = "passw0rd" // 默认密码，与admin.go中保持一致
	}
	return password
}

// CreateAdminContext 为webhook请求创建具有admin权限的上下文
// 这个函数用于解决webhook请求没有经过授权中间件的问题
// 注意：这个函数现在会验证admin密码以确保安全
func CreateAdminContext(ctx context.Context) (context.Context, error) {
	fmt.Printf("wngzhe CreateAdminContext - Creating admin context for webhook request\n")

	// 从配置文件读取admin密码
	adminPassword := getAdminPassword()
	fmt.Printf("wngzhe CreateAdminContext - Retrieved admin password from config\n")

	// 使用正常的认证流程验证admin用户和密码
	user, err := userAdmin.Validate(ctx, "admin", adminPassword)
	if err != nil {
		log.Printf("Failed to validate admin user with password: %v", err)
		return ctx, fmt.Errorf("failed to validate admin user with password: %v", err)
	}
	fmt.Printf("wngzhe CreateAdminContext - Admin password validation successful: ID=%d, Name=%s\n", user.ID, user.Username)

	// 获取admin组织信息
	org, err := orgAdmin.GetOrgByName(ctx, "admin")
	if err != nil {
		log.Printf("Failed to get admin org: %v", err)
		return ctx, fmt.Errorf("failed to get admin org: %v", err)
	}
	fmt.Printf("wngzhe CreateAdminContext - Got admin org: ID=%d, Name=%s\n", org.ID, org.Name)

	// 获取membership信息
	memberShip, err := common.GetDBMemberShip(user.ID, org.ID)
	if err != nil {
		log.Printf("Failed to get admin membership: %v", err)
		return ctx, fmt.Errorf("failed to get admin membership: %v", err)
	}

	// 确保admin权限
	memberShip.Role = model.Admin
	fmt.Printf("wngzhe CreateAdminContext - Created membership: UserID=%d, OrgID=%d, Role=%d\n",
		memberShip.UserID, memberShip.OrgID, memberShip.Role)

	// 设置上下文
	adminCtx := memberShip.SetContext(ctx)
	fmt.Printf("wngzhe CreateAdminContext - Admin context created successfully with password validation\n")

	return adminCtx, nil
}

// GetInstanceByUUIDWithAuth 使用admin权限获取实例信息的辅助函数
// 这个函数专门用于webhook调用，不修改原有的GetInstanceByUUID函数
func GetInstanceByUUIDWithAuth(ctx context.Context, instanceID string) (*model.Instance, error) {
	fmt.Printf("wngzhe GetInstanceByUUIDWithAuth - Getting instance with admin auth: %s\n", instanceID)

	// 创建admin上下文
	adminCtx, err := CreateAdminContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin context: %v", err)
	}

	// 使用admin上下文调用GetInstanceByUUID
	instance, err := instanceAdmin.GetInstanceByUUID(adminCtx, instanceID)
	if err != nil {
		log.Printf("Failed to get instance with admin auth: %v", err)
		return nil, fmt.Errorf("failed to get instance with admin auth: %v", err)
	}

	log.Printf("Successfully got instance: ID=%d, UUID=%s, CPU=%d, Hyper=%d",
		instance.ID, instance.UUID, instance.Cpu, instance.Hyper)

	return instance, nil
}

// AlertWebhookRequest Prometheus告警Webhook请求结构
type AlertWebhookRequest struct {
	Status string        `json:"status"`
	Alerts []AdjustAlert `json:"alerts"`
}

// AdjustAlert 告警信息结构
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

// AdjustmentRecord 调整记录
type AdjustmentRecord struct {
	Name          string    // 告警名称
	RuleGroupUUID string    // 规则组UUID
	Summary       string    // 告警摘要
	Description   string    // 告警描述
	StartsAt      time.Time // 告警开始时间
	AdjustType    string    // 调整类型
	TargetDevice  string    // 目标设备(适用于带宽调整)
}

// AdjustOperator 资源自动调整操作
type AdjustOperator struct{}

// NewAdjustOperator 创建资源自动调整操作对象
func NewAdjustOperator() *AdjustOperator {
	return &AdjustOperator{}
}

// ListAdjustRuleGroupsParams 列出资源调整规则组的参数
type ListAdjustRuleGroupsParams struct {
	RuleType   string
	GroupUUID  string
	Owner      string
	Page       int
	PageSize   int
	EnabledSQL string
}

// CreateAdjustRuleGroup 创建资源调整规则组
func (o *AdjustOperator) CreateAdjustRuleGroup(ctx context.Context, group *model.AdjustRuleGroup) error {
	if group.UUID == "" {
		group.UUID = uuid.New().String()
	}

	return dbs.DB().Create(group).Error
}

// GetAdjustRulesByGroupUUID 通过UUID获取资源调整规则组
func (o *AdjustOperator) GetAdjustRulesByGroupUUID(ctx context.Context, uuid string) (*model.AdjustRuleGroup, error) {
	var group model.AdjustRuleGroup
	if err := dbs.DB().Where("uuid = ?", uuid).First(&group).Error; err != nil {
		return nil, err
	}
	return &group, nil
}

// ListAdjustRuleGroups 列出资源调整规则组
func (o *AdjustOperator) ListAdjustRuleGroups(ctx context.Context, params ListAdjustRuleGroupsParams) ([]model.AdjustRuleGroup, int64, error) {
	var groups []model.AdjustRuleGroup
	var total int64

	query := dbs.DB().Model(&model.AdjustRuleGroup{})

	// 应用过滤条件
	if params.RuleType != "" {
		query = query.Where("type = ?", params.RuleType)
	}
	if params.GroupUUID != "" {
		query = query.Where("uuid = ?", params.GroupUUID)
	}
	if params.Owner != "" {
		query = query.Where("owner = ?", params.Owner)
	}
	if params.EnabledSQL != "" {
		query = query.Where(params.EnabledSQL)
	}

	// 获取总数
	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 应用分页
	offset := (params.Page - 1) * params.PageSize
	query = query.Offset(offset).Limit(params.PageSize)

	// 排序
	query = query.Order("created_at desc")

	// 执行查询
	if err := query.Find(&groups).Error; err != nil {
		return nil, 0, err
	}

	return groups, total, nil
}

// CreateCPUAdjustRuleDetail 创建CPU调整规则详情
func (o *AdjustOperator) CreateCPUAdjustRuleDetail(ctx context.Context, detail *model.CPUAdjustRuleDetail) error {
	return dbs.DB().Create(detail).Error
}

// GetCPUAdjustRuleDetails 获取CPU调整规则详情
func (o *AdjustOperator) GetCPUAdjustRuleDetails(ctx context.Context, groupUUID string) ([]model.CPUAdjustRuleDetail, error) {
	var details []model.CPUAdjustRuleDetail
	if err := dbs.DB().Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		return nil, err
	}
	return details, nil
}

// CreateBWAdjustRuleDetail 创建带宽调整规则详情
func (o *AdjustOperator) CreateBWAdjustRuleDetail(ctx context.Context, detail *model.BWAdjustRuleDetail) error {
	if detail.UUID == "" {
		detail.UUID = uuid.New().String()
	}
	return dbs.DB().Create(detail).Error
}

// GetBWAdjustRuleDetails 获取带宽调整规则详情
func (o *AdjustOperator) GetBWAdjustRuleDetails(ctx context.Context, groupUUID string) ([]model.BWAdjustRuleDetail, error) {
	var details []model.BWAdjustRuleDetail
	if err := dbs.DB().Where("group_uuid = ?", groupUUID).Find(&details).Error; err != nil {
		return nil, err
	}
	return details, nil
}

// DeleteAdjustRuleGroupWithDependencies 删除资源调整规则组及其依赖
func (o *AdjustOperator) DeleteAdjustRuleGroupWithDependencies(ctx context.Context, groupUUID string) error {
	tx := dbs.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除CPU调整规则详情
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.CPUAdjustRuleDetail{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 删除带宽调整规则详情
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.BWAdjustRuleDetail{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 删除VM链接
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.VMRuleLink{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 删除调整历史记录
	if err := tx.Where("group_uuid = ?", groupUUID).Delete(&model.AdjustmentHistory{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 删除规则组
	if err := tx.Where("uuid = ?", groupUUID).Delete(&model.AdjustRuleGroup{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// RecordAdjustmentHistory 记录调整历史
func (o *AdjustOperator) RecordAdjustmentHistory(ctx context.Context, history *model.AdjustmentHistory) error {
	history.AdjustTime = time.Now()
	return dbs.DB().Create(history).Error
}

// IsInCooldown 检查是否在冷却期内
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

	// 检查是否在冷却期内
	cooldownDuration := time.Duration(cooldownSeconds) * time.Second
	return time.Since(history.AdjustTime) < cooldownDuration, nil
}

// GetAdjustmentHistory 获取调整历史
func (o *AdjustOperator) GetAdjustmentHistory(ctx context.Context, groupUUID string, limit int) ([]model.AdjustmentHistory, error) {
	var history []model.AdjustmentHistory
	query := dbs.DB().Where("group_uuid = ?", groupUUID).Order("adjust_time desc")

	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Find(&history).Error
	return history, err
}

// SaveAdjustmentHistory 保存调整历史
func (o *AdjustOperator) SaveAdjustmentHistory(ctx context.Context, history *model.AdjustmentHistory) error {
	return dbs.DB().Create(history).Error
}

// SendAdjustEmail 发送调整通知邮件
func (o *AdjustOperator) SendAdjustEmail(email string, record *AdjustmentRecord, domain string) error {
	// 可以在此实现邮件发送逻辑，或者调用公共邮件服务
	log.Printf("发送调整通知邮件到 %s: domain=%s, adjustType=%s", email, domain, record.AdjustType)
	// 这里可以实现实际的邮件发送逻辑，暂时只记录日志
	return nil
}

// AdjustCPUResource 调整CPU资源
func (o *AdjustOperator) AdjustCPUResource(ctx context.Context, record *AdjustmentRecord, domain string, limit bool, instanceID string) error {
	fmt.Printf("wngzhe AdjustCPUResource - domain: %s, limit: %v, ruleGroupUUID: %s, instanceID: %s\n", domain, limit, record.RuleGroupUUID, instanceID)
	log.Printf("wngzhe AdjustCPUResource - Starting CPU adjustment for domain: %s, limit: %v", domain, limit)

	var instance *model.Instance
	var err error

	// 如果提供了instanceID，直接使用它查询实例信息
	if instanceID != "" {
		fmt.Printf("wngzhe AdjustCPUResource - Using provided instanceID: %s\n", instanceID)
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance info by instanceID: %v", err)
			return fmt.Errorf("failed to get instance info by instanceID: %v", err)
		}
	} else {
		// 如果没有提供instanceID，则使用原来的方法通过domain查询
		fmt.Printf("wngzhe AdjustCPUResource - No instanceID provided, querying by domain: %s\n", domain)
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}
		fmt.Printf("wngzhe AdjustCPUResource - Got instance UUID: %s\n", uuid)

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance info: %v", err)
			return fmt.Errorf("failed to get instance info: %v", err)
		}
	}
	fmt.Printf("wngzhe AdjustCPUResource - Got instance info: CPU=%d, Hyper=%d\n", instance.Cpu, instance.Hyper)

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	fmt.Printf("wngzhe AdjustCPUResource - Control command: %s\n", control)

	var command string

	if limit {
		fmt.Printf("wngzhe AdjustCPUResource - Applying CPU limit for ruleGroupUUID: %s\n", record.RuleGroupUUID)

		// 根据规则组获取限制值
		details, err := o.GetCPUAdjustRuleDetails(ctx, record.RuleGroupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get CPU adjustment rule details: %v", err)
			return fmt.Errorf("failed to get CPU adjustment rule details: %v", err)
		}
		fmt.Printf("wngzhe AdjustCPUResource - Got %d CPU adjustment rule details\n", len(details))

		// 获取限制百分比
		limitPercent := details[0].LimitPercent
		fmt.Printf("wngzhe AdjustCPUResource - LimitPercent: %d%%, Original CPU: %d\n", limitPercent, instance.Cpu)

		// 使用CPU配额限制方式，不再需要检查单核CPU限制
		command = fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' '%d'",
			domain, limitPercent)
	} else {
		// 恢复CPU资源
		command = fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' 'restore'",
			domain)
	}
	fmt.Printf("wngzhe AdjustCPUResource - Executing command: %s\n", command)

	// 执行命令
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		fmt.Printf("wngzhe AdjustCPUResource - Command execution failed: %v\n", err)
		log.Printf("Failed to adjust CPU: %v", err)
		return fmt.Errorf("failed to adjust CPU resources: %v", err)
	}

	// 成功调整CPU后，更新自定义指标状态
	status := 0
	if limit {
		status = 1 // 限制状态
	}
	updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_cpu_adjustment_status.sh --domain '%s' --rule-id '%s' --status %d",
		domain, fmt.Sprintf("%s-%s", domain, record.RuleGroupUUID), status)
	fmt.Printf("wngzhe AdjustCPUResource - Updating CPU adjustment metric: %s\n", updateCommand)

	err = common.HyperExecute(ctx, control, updateCommand)
	if err != nil {
		// 只记录警告，不影响主要操作的成功状态
		fmt.Printf("wngzhe AdjustCPUResource - Warning: Failed to update CPU adjustment metric: %v\n", err)
		log.Printf("Warning: Failed to update CPU adjustment metric for domain %s: %v", domain, err)
	} else {
		fmt.Printf("wngzhe AdjustCPUResource - Successfully updated CPU adjustment metric\n")
	}

	if limit {
		fmt.Printf("wngzhe AdjustCPUResource - CPU adjustment completed successfully: domain=%s, limit applied\n", domain)
		log.Printf("Successfully adjusted CPU resources: domain=%s, limit=%v", domain, limit)
	} else {
		fmt.Printf("wngzhe AdjustCPUResource - CPU adjustment completed successfully: domain=%s, limits restored\n", domain)
		log.Printf("Successfully restored CPU resources: domain=%s", domain)
	}
	return nil
}

// RestoreCPUResource 恢复CPU资源
func (o *AdjustOperator) RestoreCPUResource(ctx context.Context, record *AdjustmentRecord, domain string, instanceID string) error {
	fmt.Printf("wngzhe RestoreCPUResource - Starting CPU restore for domain: %s, ruleGroupUUID: %s, instanceID: %s\n", domain, record.RuleGroupUUID, instanceID)
	log.Printf("wngzhe RestoreCPUResource - Starting CPU restore for domain: %s, instanceID: %s", domain, instanceID)

	var instance *model.Instance
	var err error

	// 如果提供了instanceID，直接使用它查询实例信息
	if instanceID != "" {
		fmt.Printf("wngzhe RestoreCPUResource - Using provided instanceID: %s\n", instanceID)
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance info by instanceID: %v", err)
			return fmt.Errorf("failed to get instance info by instanceID: %v", err)
		}
	} else {
		// 如果没有提供instanceID，则使用原来的方法通过domain查询
		fmt.Printf("wngzhe RestoreCPUResource - No instanceID provided, querying by domain: %s\n", domain)
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}
		fmt.Printf("wngzhe RestoreCPUResource - Got instance UUID: %s\n", uuid)

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance info: %v", err)
			return fmt.Errorf("failed to get instance info: %v", err)
		}
	}
	fmt.Printf("wngzhe RestoreCPUResource - Got instance info: Original CPU=%d, Hyper=%d\n", instance.Cpu, instance.Hyper)

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)
	fmt.Printf("wngzhe RestoreCPUResource - Control command: %s\n", control)

	// 恢复CPU资源（移除限制）
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' 'restore'",
		domain)
	fmt.Printf("wngzhe RestoreCPUResource - Executing restore command: %s\n", command)

	// 执行命令
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		fmt.Printf("wngzhe RestoreCPUResource - Command execution failed: %v\n", err)
		log.Printf("Failed to restore CPU: %v", err)
		return fmt.Errorf("failed to restore CPU resources: %v", err)
	}

	// 成功恢复CPU后，更新自定义指标状态
	status := 0
	updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_cpu_adjustment_status.sh --domain '%s' --rule-id '%s' --status %d",
		domain, fmt.Sprintf("%s-%s", domain, record.RuleGroupUUID), status)
	fmt.Printf("wngzhe RestoreCPUResource - Updating CPU adjustment metric: %s\n", updateCommand)

	err = common.HyperExecute(ctx, control, updateCommand)
	if err != nil {
		// 只记录警告，不影响主要操作的成功状态
		fmt.Printf("wngzhe RestoreCPUResource - Warning: Failed to update CPU adjustment metric: %v\n", err)
		log.Printf("Warning: Failed to update CPU adjustment metric for domain %s: %v", domain, err)
	} else {
		fmt.Printf("wngzhe RestoreCPUResource - Successfully updated CPU adjustment metric\n")
	}

	fmt.Printf("wngzhe RestoreCPUResource - CPU restore completed successfully: domain=%s, limits removed\n", domain)
	log.Printf("Successfully restored CPU resources: domain=%s", domain)
	return nil
}

// AdjustBandwidthResource 调整带宽资源
func (o *AdjustOperator) AdjustBandwidthResource(ctx context.Context, record *AdjustmentRecord, domain string, device string, limit bool, instanceID string) error {
	var instance *model.Instance
	var err error

	// 如果提供了instanceID，直接使用它查询实例信息
	if instanceID != "" {
		fmt.Printf("wngzhe AdjustBandwidthResource - Using provided instanceID: %s\n", instanceID)
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance info by instanceID: %v", err)
			return fmt.Errorf("failed to get instance info by instanceID: %v", err)
		}
	} else {
		// 如果没有提供instanceID，则使用原来的方法通过domain查询
		fmt.Printf("wngzhe AdjustBandwidthResource - No instanceID provided, querying by domain: %s\n", domain)
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance info: %v", err)
			return fmt.Errorf("failed to get instance info: %v", err)
		}
	}

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// 获取接口的原始带宽配置
	var targetInterface *model.Interface
	if device != "" {
		targetInterface = findInterfaceByTargetDevice(instance, device)
	} else {
		// 如果没有提供device，则尝试通过target_device匹配
		targetInterface = findInterfaceByTargetDevice(instance, record.TargetDevice)
	}

	if targetInterface == nil {
		log.Printf("Interface not found: device=%s, instanceID=%s", device, instanceID)
		return fmt.Errorf("interface not found: device=%s", device)
	}

	// 获取接口的原始带宽配置
	originalInBw := int(targetInterface.Inbound)   // 原始入站带宽 (Mbps)
	originalOutBw := int(targetInterface.Outbound) // 原始出站带宽 (Mbps)

	fmt.Printf("wngzhe AdjustBandwidthResource - Original bandwidth: interface=%s, inbound=%d, outbound=%d\n",
		targetInterface.Name, originalInBw, originalOutBw)

	// 设置带宽限制值
	inBw := originalInBw   // 默认保持原始值
	outBw := originalOutBw // 默认保持原始值

	// 检查是否需要实际执行带宽限制
	needActualLimit := false
	var bwType string

	if limit {
		// 获取带宽调整规则详情来计算限制值
		details, err := o.GetBWAdjustRuleDetails(ctx, record.RuleGroupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get bandwidth adjustment rule details: %v", err)
			return fmt.Errorf("failed to get bandwidth adjustment rule details: %v", err)
		}

		// 根据调整类型设置限制值
		if record.AdjustType == "limit_in_bw" || record.AdjustType == model.RuleTypeAdjustInBW {
			bwType = "in"
			// 只有原始入站带宽大于0时才需要实际限制
			if originalInBw > 0 {
				needActualLimit = true
				// 限制入站带宽到规则定义的值 (从 kB/s 转换为 MB/s)
				inBw = details[0].LimitValue / 1024 // 转换为 MB/s
				if inBw < 1 {
					inBw = 1 // 最小1MB/s
				}
				// 使用单向设置，不影响出站带宽
				outBw = 0 // 占位符，实际不使用
				fmt.Printf("wngzhe AdjustBandwidthResource - Will limit inbound bandwidth from %d to %d MB/s (using single-direction mode)\n", originalInBw, inBw)
			} else {
				fmt.Printf("wngzhe AdjustBandwidthResource - Original inbound bandwidth is 0 (unlimited), skipping actual limit but setting metric\n")
			}
		} else if record.AdjustType == "limit_out_bw" || record.AdjustType == model.RuleTypeAdjustOutBW {
			bwType = "out"
			// 只有原始出站带宽大于0时才需要实际限制
			if originalOutBw > 0 {
				needActualLimit = true
				// 限制出站带宽到规则定义的值 (从 kB/s 转换为 MB/s)
				outBw = details[0].LimitValue / 1024 // 转换为 MB/s
				if outBw < 1 {
					outBw = 1 // 最小1MB/s
				}
				// 使用单向设置，不影响入站带宽
				inBw = 0 // 占位符，实际不使用
				fmt.Printf("wngzhe AdjustBandwidthResource - Will limit outbound bandwidth from %d to %d MB/s (using single-direction mode)\n", originalOutBw, outBw)
			} else {
				fmt.Printf("wngzhe AdjustBandwidthResource - Original outbound bandwidth is 0 (unlimited), skipping actual limit but setting metric\n")
			}
		}
	}

	// 使用target_device作为nic_name
	nicName := record.TargetDevice
	if nicName == "" {
		// 如果target_device为空，使用device参数
		nicName = device
	}

	fmt.Printf("wngzhe AdjustBandwidthResource - Using nic_name: %s\n", nicName)

	// 只有在需要时才执行实际的带宽限制命令
	if needActualLimit {
		// 提取domain中的ID部分（去掉inst-前缀）
		// domain格式为"inst-6"，需要提取出"6"传给脚本
		vmID := domain
		if strings.HasPrefix(domain, "inst-") {
			vmID = strings.TrimPrefix(domain, "inst-")
		}
		fmt.Printf("wngzhe AdjustBandwidthResource - Extracted VM ID: %s from domain: %s\n", vmID, domain)

		var command string
		if bwType == "in" {
			// 只限制入站带宽，使用单向模式
			command = fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '%d' '0' --inbound-only",
				vmID, nicName, inBw)
			fmt.Printf("wngzhe AdjustBandwidthResource - Executing inbound bandwidth limit: %d MB/s\n", inBw)
		} else if bwType == "out" {
			// 只限制出站带宽，使用单向模式
			command = fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '0' '%d' --outbound-only",
				vmID, nicName, outBw)
			fmt.Printf("wngzhe AdjustBandwidthResource - Executing outbound bandwidth limit: %d MB/s\n", outBw)
		}

		fmt.Printf("wngzhe AdjustBandwidthResource - Executing bandwidth limit command: %s\n", command)

		// 执行命令
		err = common.HyperExecute(ctx, control, command)
		if err != nil {
			log.Printf("Failed to adjust bandwidth: %v", err)
			return fmt.Errorf("failed to adjust bandwidth resources: %v", err)
		}
		fmt.Printf("wngzhe AdjustBandwidthResource - Successfully executed bandwidth limit\n")
	} else {
		fmt.Printf("wngzhe AdjustBandwidthResource - Skipped actual bandwidth limit operation\n")
	}

	// 成功调整带宽后，更新自定义指标状态
	status := 0
	if limit {
		status = 1 // 限制状态
		// bwType 已在前面设置
	}

	if bwType != "" {
		updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_bandwidth_adjustment_status.sh --domain '%s' --rule-id '%s' --type '%s' --status %d --target-device '%s'",
			domain, record.RuleGroupUUID, bwType, status, nicName)
		fmt.Printf("wngzhe AdjustBandwidthResource - Updating bandwidth adjustment metric: %s\n", updateCommand)

		err = common.HyperExecute(ctx, control, updateCommand)
		if err != nil {
			// 只记录警告，不影响主要操作的成功状态
			fmt.Printf("wngzhe AdjustBandwidthResource - Warning: Failed to update bandwidth adjustment metric: %v\n", err)
			log.Printf("Warning: Failed to update bandwidth adjustment metric for domain %s: %v", domain, err)
		} else {
			fmt.Printf("wngzhe AdjustBandwidthResource - Successfully updated bandwidth adjustment metric\n")
		}
	}

	log.Printf("Successfully adjusted bandwidth resources: domain=%s, device=%s, nicName=%s, limit=%v, inBw=%d, outBw=%d",
		domain, device, nicName, limit, inBw, outBw)
	return nil
}

// RestoreBandwidthResource 恢复带宽资源
func (o *AdjustOperator) RestoreBandwidthResource(ctx context.Context, record *AdjustmentRecord, domain string, device string, instanceID string) error {
	var instance *model.Instance
	var err error

	// 如果提供了instanceID，直接使用它查询实例信息
	if instanceID != "" {
		fmt.Printf("wngzhe RestoreBandwidthResource - Using provided instanceID: %s\n", instanceID)
		instance, err = GetInstanceByUUIDWithAuth(ctx, instanceID)
		if err != nil {
			log.Printf("Failed to get instance info by instanceID: %v", err)
			return fmt.Errorf("failed to get instance info by instanceID: %v", err)
		}
	} else {
		// 如果没有提供instanceID，则使用原来的方法通过domain查询
		fmt.Printf("wngzhe RestoreBandwidthResource - No instanceID provided, querying by domain: %s\n", domain)
		uuid, err := GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			log.Printf("Failed to get instance UUID: %v", err)
			return fmt.Errorf("failed to get instance UUID: %v", err)
		}

		instance, err = GetInstanceByUUIDWithAuth(ctx, uuid)
		if err != nil {
			log.Printf("Failed to get instance info: %v", err)
			return fmt.Errorf("failed to get instance info: %v", err)
		}
	}

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// 获取接口的原始带宽配置
	var targetInterface *model.Interface
	if device != "" {
		targetInterface = findInterfaceByTargetDevice(instance, device)
	} else {
		// 如果没有提供device，则尝试通过target_device匹配
		targetInterface = findInterfaceByTargetDevice(instance, record.TargetDevice)
	}

	if targetInterface == nil {
		log.Printf("Interface not found: device=%s, instanceID=%s", device, instanceID)
		return fmt.Errorf("interface not found: device=%s", device)
	}

	// 恢复到原始的带宽配置
	originalInBw := int(targetInterface.Inbound)   // 原始入站带宽 (Mbps)
	originalOutBw := int(targetInterface.Outbound) // 原始出站带宽 (Mbps)

	fmt.Printf("wngzhe RestoreBandwidthResource - Restoring to original bandwidth: interface=%s, inbound=%d, outbound=%d\n",
		targetInterface.Name, originalInBw, originalOutBw)

	// 检查是否需要实际执行带宽恢复
	needActualRestore := false
	var bwType string

	// 根据调整类型确定之前限制的带宽方向，用于清理相应的指标
	if record.AdjustType == "limit_in_bw" || record.AdjustType == model.RuleTypeAdjustInBW {
		bwType = "in"
	} else if record.AdjustType == "limit_out_bw" || record.AdjustType == model.RuleTypeAdjustOutBW {
		bwType = "out"
	}

	// 对于恢复操作，如果原始带宽配置中有任何限制（非0值），就需要执行恢复
	if originalInBw > 0 || originalOutBw > 0 {
		needActualRestore = true
		fmt.Printf("wngzhe RestoreBandwidthResource - Will restore bandwidth to original: inbound=%d Mbps, outbound=%d Mbps\n", originalInBw, originalOutBw)
	} else {
		fmt.Printf("wngzhe RestoreBandwidthResource - Both original bandwidths are 0 (unlimited), skipping actual restore but clearing metric\n")
	}

	// 使用target_device作为nic_name
	nicName := record.TargetDevice
	if nicName == "" {
		// 如果target_device为空，使用device参数
		nicName = device
	}

	fmt.Printf("wngzhe RestoreBandwidthResource - Using nic_name: %s\n", nicName)

	// 只有在需要时才执行实际的带宽恢复命令
	if needActualRestore {
		// 提取domain中的ID部分（去掉inst-前缀）
		// domain格式为"inst-6"，需要提取出"6"传给脚本
		vmID := domain
		if strings.HasPrefix(domain, "inst-") {
			vmID = strings.TrimPrefix(domain, "inst-")
		}
		fmt.Printf("wngzhe RestoreBandwidthResource - Extracted VM ID: %s from domain: %s\n", vmID, domain)

		// 恢复带宽到原始配置值
		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '%d' '%d'",
			vmID, nicName, originalInBw, originalOutBw)

		fmt.Printf("wngzhe RestoreBandwidthResource - Executing bandwidth restore command: %s\n", command)

		// 执行命令
		err = common.HyperExecute(ctx, control, command)
		if err != nil {
			log.Printf("Failed to restore bandwidth: %v", err)
			return fmt.Errorf("failed to restore bandwidth resources: %v", err)
		}
		fmt.Printf("wngzhe RestoreBandwidthResource - Successfully executed bandwidth restore\n")
	} else {
		fmt.Printf("wngzhe RestoreBandwidthResource - Skipped actual bandwidth restore operation\n")
	}

	// 成功恢复带宽后，更新自定义指标状态（清理指标）
	status := 0

	if bwType != "" {
		updateCommand := fmt.Sprintf("/opt/cloudland/scripts/kvm/update_vm_bandwidth_adjustment_status.sh --domain '%s' --rule-id '%s' --type '%s' --status %d --target-device '%s'",
			domain, record.RuleGroupUUID, bwType, status, nicName)
		fmt.Printf("wngzhe RestoreBandwidthResource - Updating bandwidth adjustment metric: %s\n", updateCommand)

		err = common.HyperExecute(ctx, control, updateCommand)
		if err != nil {
			// 只记录警告，不影响主要操作的成功状态
			fmt.Printf("wngzhe RestoreBandwidthResource - Warning: Failed to update bandwidth adjustment metric: %v\n", err)
			log.Printf("Warning: Failed to update bandwidth adjustment metric for domain %s: %v", domain, err)
		} else {
			fmt.Printf("wngzhe RestoreBandwidthResource - Successfully updated bandwidth adjustment metric\n")
		}
	}

	log.Printf("Successfully restored bandwidth resources: domain=%s, device=%s, nicName=%s, originalInBw=%d, originalOutBw=%d",
		domain, device, nicName, originalInBw, originalOutBw)
	return nil
}

// GetAdjustmentCooldownConfig 从数据库获取调整冷却期配置
func (o *AdjustOperator) GetAdjustmentCooldownConfig(ctx context.Context, adjustType string, groupUUID string) int {
	// 默认冷却时间为5分钟（300秒）
	defaultCooldown := 300

	// 根据调整类型获取对应的规则配置
	switch adjustType {
	case model.RuleTypeAdjustCPU, "limit_cpu", "restore_cpu":
		// 查询CPU调整规则详情
		details, err := o.GetCPUAdjustRuleDetails(ctx, groupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("获取CPU调整规则详情失败或不存在: %v", err)
			return defaultCooldown
		}
		// 使用恢复持续时间作为冷却期
		return details[0].RestoreDuration
	case model.RuleTypeAdjustInBW, model.RuleTypeAdjustOutBW, "limit_in_bw", "restore_in_bw", "limit_out_bw", "restore_out_bw":
		// 查询带宽调整规则详情
		details, err := o.GetBWAdjustRuleDetails(ctx, groupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("获取带宽调整规则详情失败或不存在: %v", err)
			return defaultCooldown
		}
		// 使用恢复持续时间作为冷却期
		return details[0].RestoreDuration
	default:
		log.Printf("未知的调整类型: %s，使用默认冷却期", adjustType)
		return defaultCooldown
	}
}
