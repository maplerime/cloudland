package common

import (
	"context"
	"fmt"
	"time"

	"web/src/model"
	"web/src/utils/log"

	"github.com/jinzhu/gorm"
)

var alarmLogger = log.MustGetLogger("alarm")

type ListRuleGroupsParams struct {
	RuleType string
	Page     int
	PageSize int
}

// 在文件顶部添加以下结构体定义（约在23行附近）
type (
	// 虚拟机关联表
	VMRuleLink struct {
		ID        uint      `gorm:"primaryKey;autoIncrement"`
		GroupID   string    `gorm:"type:varchar(36);index;not null"`
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
		ID        int       `gorm:"primaryKey;autoIncrement"`
		GroupID   string    `gorm:"type:varchar(36);index"`
		Name      string    `gorm:"size:255"`
		Duration  int       `gorm:"check:duration >= 1"`
		Threshold int       `gorm:"check:threshold >= 1"`
		Cooldown  int       `gorm:"check:cooldown >= 1"`
		Recovery  int       `gorm:"check:recovery >= 0"`
		CreatedAt time.Time `gorm:"autoCreateTime"`
	}

	// 带宽规则表
	BWRule struct {
		ID           int       `gorm:"primaryKey;autoIncrement"`
		GroupID      string    `gorm:"type:varchar(36);index"`
		Name         string    `gorm:"size:255"`
		InDuration   int       `gorm:"check:in_duration >= 1"`
		InThreshold  int       `gorm:"check:in_threshold >= 1"`
		InCooldown   int       `gorm:"check:in_cooldown >= 1"`
		OutDuration  int       `gorm:"check:out_duration >= 1"`
		OutThreshold int       `gorm:"check:out_threshold >= 1"`
		OutCooldown  int       `gorm:"check:out_cooldown >= 1"`
		CreatedAt    time.Time `gorm:"autoCreateTime"`
	}
)

type AlarmOperator struct {
	DB *gorm.DB // 改为可导出字段
}

func (a *AlarmOperator) GetCPURulesByGroupID(ctx context.Context, groupID string, rules *[]model.CPURuleDetail) error {
	ctx, db := GetContextDB(ctx)
	return db.Where("group_id = ?", groupID).Find(rules).Error
}

// 新增带锁查询实现
func (a *AlarmOperator) GetRuleGroupByID(ctx context.Context, groupID string) (*model.RuleGroupV2, error) {
	ctx, db := GetContextDB(ctx)
	var group model.RuleGroupV2
	err := db.Set("gorm:query_option", "FOR UPDATE").
		Where("id = ?", groupID).
		Preload("CPURuleDetails").
		Take(&group).Error
	if err != nil {
		alarmLogger.Error("规则组查询失败", "groupID", groupID, "error", err)
		return nil, fmt.Errorf("规则组查询失败: %w", err)
	}
	return &group, nil
}

// 状态更新事务实现
func (a *AlarmOperator) UpdateRuleGroupStatus(ctx context.Context, groupID string, enabled bool) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.RuleGroupV2{}).
			Where("id = ?", groupID).
			Update("enabled", enabled)
		if result.Error != nil {
			alarmLogger.Error("状态更新失败", "groupID", groupID, "error", result.Error)
			return fmt.Errorf("状态更新失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("规则组不存在")
		}
		return nil
	})
}

// 批量关联虚拟机实现
func (a *AlarmOperator) BatchLinkVMs(ctx context.Context, groupID string, vmNames []string) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for _, vmName := range vmNames {
			link := &model.VMRuleLink{
				GroupID: groupID,
				VMName:  vmName,
			}
			if err := tx.Create(link).Error; err != nil {
				alarmLogger.Error("创建关联记录失败",
					"groupID", groupID,
					"vmName", vmName,
					"error", err)
				return fmt.Errorf("创建关联失败: %w", err)
			}
		}
		return nil
	})
}

// 在 AlarmOperator 结构体添加以下方法
func (a *AlarmOperator) DeleteRuleGroup(ctx context.Context, id, ruleType string) error {
	ctx, db := GetContextDB(ctx)
	result := db.Where("id = ? AND type = ?", id, ruleType).
		Delete(&model.RuleGroupV2{})
	if result.Error != nil {
		alarmLogger.Error("规则组删除失败", "id", id, "type", ruleType, "error", result.Error)
	}
	return result.Error
}

// 新增虚拟机关联操作方法
func (a *AlarmOperator) DeleteVMLink(ctx context.Context, groupID, vmName, ruleType string) (int64, error) {
	ctx, db := GetContextDB(ctx)
	result := db.Where("group_id = ? AND vm_name = ?", groupID, vmName).
		Delete(&model.VMRuleLink{})
	if result.Error != nil {
		alarmLogger.Error("删除虚拟机关联失败",
			"groupID", groupID,
			"vmName", vmName,
			"error", result.Error)
	}
	return result.RowsAffected, result.Error
}

// 新增获取关联VM列表方法
// 新增获取关联VM的方法
func (a *AlarmOperator) GetLinkedVMs(ctx context.Context, groupID string) ([]model.VMRuleLink, error) {
	ctx, db := GetContextDB(ctx)
	var links []model.VMRuleLink
	if err := db.Where("group_id = ?", groupID).Find(&links).Error; err != nil {
		alarmLogger.Error("获取关联VM失败", "groupID", groupID, "error", err)
		return nil, err
	}
	return links, nil
}

func (a *AlarmOperator) DeleteRuleGroupWithDependencies(ctx context.Context, groupID, ruleType string) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		// 删除规则组（添加完整查询条件）
		if err := tx.Where("id = ? AND type = ?", groupID, ruleType).
			Delete(&model.RuleGroupV2{}).Error; err != nil {
			alarmLogger.Error("规则组删除失败", "groupID", groupID, "error", err)
			return fmt.Errorf("规则组删除失败: %w", err)
		}

		switch ruleType {
		case "cpu":
			// 使用本地CPURule结构体
			if err := tx.Where("group_id = ?", groupID).
				Delete(&CPURule{}).Error; err != nil {
				alarmLogger.Error("CPU规则删除失败",
					"groupID", groupID,
					"error", err)
				return fmt.Errorf("CPU规则删除失败: %w", err)
			}
		case "bw":
			// 使用本地BWRule结构体
			if err := tx.Where("group_id = ?", groupID).
				Delete(&BWRule{}).Error; err != nil {
				alarmLogger.Error("带宽规则删除失败",
					"groupID", groupID,
					"error", err)
				return fmt.Errorf("带宽规则删除失败: %w", err)
			}
		default:
			return fmt.Errorf("未知的规则类型: %s", ruleType)
		}

		// 删除虚拟机关联（使用本地VMRuleLink结构体）
		if err := tx.Where("group_id = ?", groupID).
			Delete(&VMRuleLink{}).Error; err != nil {
			alarmLogger.Error("虚拟机关联删除失败",
				"groupID", groupID,
				"error", err)
			return fmt.Errorf("虚拟机关联删除失败: %w", err)
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
		alarmLogger.Error("CPU规则删除失败", "groupID", groupID, "error", err)
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

	// 获取总数
	if err := query.Count(&total).Error; err != nil {
		alarmLogger.Error("总数查询失败",
			"ruleType", params.RuleType,
			"error", err)
		return nil, 0, fmt.Errorf("总数查询失败: %w", err)
	}

	// 执行分页查询
	if err := query.Scopes(Paginate(params.Page, params.PageSize)).
		Find(&groups).Error; err != nil {
		alarmLogger.Error("分页查询失败",
			"ruleType", params.RuleType,
			"page", params.Page,
			"pageSize", params.PageSize,
			"error", err)
		return nil, 0, fmt.Errorf("分页查询失败: %w", err)
	}

	return groups, total, nil
}

// 新增触发次数更新方法
func (a *AlarmOperator) IncrementTriggerCount(ctx context.Context, groupID string) error {
	ctx, db := GetContextDB(ctx)
	return db.Model(&model.RuleGroupV2{}).
		Where("id = ?", groupID).
		Update("trigger_cnt", gorm.Expr("trigger_cnt + 1")).Error
}

func (a *AlarmOperator) CreateCPURules(ctx context.Context, groupID string, rules []CPURule) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range rules {
			rule := &CPURule{
				GroupID:   groupID,
				Name:      rules[i].Name,
				Duration:  rules[i].Duration,
				Threshold: rules[i].Threshold,
				Cooldown:  rules[i].Cooldown,
				Recovery:  rules[i].Recovery,
			}
			if err := tx.Create(rule).Error; err != nil {
				alarmLogger.Error("创建CPU规则失败",
					"groupID", groupID,
					"rule", rules[i],
					"error", err)
				return fmt.Errorf("创建CPU规则失败: %w", err)
			}
		}
		return nil
	})
}

// 修正后的带宽规则创建方法（匹配数据库模型）
func (a *AlarmOperator) CreateBWRules(ctx context.Context, groupID string, rules []BWRule) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range rules {
			rule := &model.BWRuleDetail{
				GroupID:      groupID,
				Name:         rules[i].Name,
				InDuration:   rules[i].InDuration,
				InThreshold:  rules[i].InThreshold,
				OutDuration:  rules[i].OutDuration,
				OutThreshold: rules[i].OutThreshold,
			}
			if err := tx.Create(rule).Error; err != nil {
				alarmLogger.Error("创建带宽规则失败",
					"groupID", groupID,
					"rule", rules[i],
					"error", err)
				return fmt.Errorf("创建带宽规则失败: %w", err)
			}
		}
		return nil
	})
}

// 修正后的规则组创建方法（保持上下文一致性）
func (a *AlarmOperator) CreateRuleGroup(ctx context.Context, group *model.RuleGroupV2) error {
	ctx, db := GetContextDB(ctx)
	if err := db.Create(group).Error; err != nil {
		alarmLogger.Error("规则组创建失败",
			"groupID", group.ID,
			"error", err)
		return fmt.Errorf("规则组创建失败: %w", err)
	}
	return nil
}
