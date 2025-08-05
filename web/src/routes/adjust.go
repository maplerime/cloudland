package routes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"web/src/common"
	"web/src/dbs"
	"web/src/model"
)

// 不需要重复声明instanceAdmin，已在instance.go中定义

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
func (o *AdjustOperator) AdjustCPUResource(ctx context.Context, record *AdjustmentRecord, domain string, limit bool) error {
	// 查询虚拟机实例以获取计算节点信息
	uuid, err := GetInstanceUUIDByDomain(ctx, domain)
	if err != nil {
		log.Printf("Failed to get instance UUID: %v", err)
		return fmt.Errorf("Failed to get instance UUID: %v", err)
	}

	// 使用instanceAdmin调用实例方法
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuid)
	if err != nil {
		log.Printf("Failed to get instance info: %v", err)
		return fmt.Errorf("Failed to get instance info: %v", err)
	}

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// 确定CPU核心数
	cpuCount := instance.Cpu // 默认使用实例配置的CPU数

	if limit {
		// 根据规则组获取限制值
		details, err := o.GetCPUAdjustRuleDetails(ctx, record.RuleGroupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get CPU adjustment rule details: %v", err)
			return fmt.Errorf("Failed to get CPU adjustment rule details: %v", err)
		}

		// 检查单核CPU情况
		if instance.Cpu == 1 {
			log.Printf("Cannot limit CPU for single-core instance: %s", domain)
			return fmt.Errorf("Cannot limit CPU for single-core instance")
		}

		// 计算限制后的CPU数量
		limitPercent := details[0].LimitPercent
		cpuCount = instance.Cpu * int32(limitPercent) / 100
		if cpuCount < 1 {
			cpuCount = 1 // 至少保留一个CPU核心
		}
	}

	// 使用virsh setvcpus命令动态调整CPU
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' '%d'",
		domain, cpuCount)

	// 执行命令
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		log.Printf("Failed to adjust CPU: %v", err)
		return fmt.Errorf("Failed to adjust CPU resources: %v", err)
	}

	log.Printf("Successfully adjusted CPU resources: domain=%s, limit=%v, cpuCount=%d", domain, limit, cpuCount)
	return nil
}

// RestoreCPUResource 恢复CPU资源
func (o *AdjustOperator) RestoreCPUResource(ctx context.Context, record *AdjustmentRecord, domain string) error {
	// 查询虚拟机实例以获取计算节点信息和原始CPU配置
	uuid, err := GetInstanceUUIDByDomain(ctx, domain)
	if err != nil {
		log.Printf("Failed to get instance UUID: %v", err)
		return fmt.Errorf("Failed to get instance UUID: %v", err)
	}

	// 使用instanceAdmin调用实例方法
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuid)
	if err != nil {
		log.Printf("Failed to get instance info: %v", err)
		return fmt.Errorf("Failed to get instance info: %v", err)
	}

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// 恢复原始CPU数量
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/adjust_cpu_hotplug.sh '%s' '%d'",
		domain, instance.Cpu)

	// 执行命令
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		log.Printf("Failed to restore CPU: %v", err)
		return fmt.Errorf("Failed to restore CPU resources: %v", err)
	}

	log.Printf("Successfully restored CPU resources: domain=%s, cpuCount=%d", domain, instance.Cpu)
	return nil
}

// AdjustBandwidthResource 调整带宽资源
func (o *AdjustOperator) AdjustBandwidthResource(ctx context.Context, record *AdjustmentRecord, domain string, device string, limit bool) error {
	// 查询虚拟机实例以获取计算节点信息
	uuid, err := GetInstanceUUIDByDomain(ctx, domain)
	if err != nil {
		log.Printf("Failed to get instance UUID: %v", err)
		return fmt.Errorf("Failed to get instance UUID: %v", err)
	}

	// 使用instanceAdmin调用实例方法
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuid)
	if err != nil {
		log.Printf("Failed to get instance info: %v", err)
		return fmt.Errorf("Failed to get instance info: %v", err)
	}

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// 设置带宽限制值
	inBw := 0  // 默认值，无限制
	outBw := 0 // 默认值，无限制

	if limit {
		// 根据规则组设置带宽限制
		details, err := o.GetBWAdjustRuleDetails(ctx, record.RuleGroupUUID)
		if err != nil || len(details) == 0 {
			log.Printf("Failed to get bandwidth adjustment rule details: %v", err)
			return fmt.Errorf("Failed to get bandwidth adjustment rule details: %v", err)
		}

		if record.AdjustType == "limit_in_bw" || record.AdjustType == model.RuleTypeAdjustInBW {
			inBw = details[0].LimitValue
		} else if record.AdjustType == "limit_out_bw" || record.AdjustType == model.RuleTypeAdjustOutBW {
			outBw = details[0].LimitValue
		}
	}

	// 调用set_nic_speed.sh脚本
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '%d' '%d'",
		domain, device, inBw, outBw)

	// 执行命令
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		log.Printf("Failed to adjust bandwidth: %v", err)
		return fmt.Errorf("Failed to adjust bandwidth resources: %v", err)
	}

	log.Printf("Successfully adjusted bandwidth resources: domain=%s, device=%s, limit=%v, inBw=%d, outBw=%d",
		domain, device, limit, inBw, outBw)
	return nil
}

// RestoreBandwidthResource 恢复带宽资源
func (o *AdjustOperator) RestoreBandwidthResource(ctx context.Context, record *AdjustmentRecord, domain string, device string) error {
	// 查询虚拟机实例以获取计算节点信息
	uuid, err := GetInstanceUUIDByDomain(ctx, domain)
	if err != nil {
		log.Printf("Failed to get instance UUID: %v", err)
		return fmt.Errorf("Failed to get instance UUID: %v", err)
	}

	// 使用instanceAdmin调用实例方法
	instance, err := instanceAdmin.GetInstanceByUUID(ctx, uuid)
	if err != nil {
		log.Printf("Failed to get instance info: %v", err)
		return fmt.Errorf("Failed to get instance info: %v", err)
	}

	// 构建命令
	control := fmt.Sprintf("inter=%d", instance.Hyper)

	// 恢复带宽，设置为0表示无限制
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/set_nic_speed.sh '%s' '%s' '%d' '%d'",
		domain, device, 0, 0)

	// 执行命令
	err = common.HyperExecute(ctx, control, command)
	if err != nil {
		log.Printf("Failed to restore bandwidth: %v", err)
		return fmt.Errorf("Failed to restore bandwidth resources: %v", err)
	}

	log.Printf("Successfully restored bandwidth resources: domain=%s, device=%s", domain, device)
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
