package common

import (
	"context"
	"fmt"
	"time"
	"unsafe"
	"github.com/google/uuid"
	"web/src/model"
	"web/src/utils/log"

	"github.com/jinzhu/gorm"
)

var alarmLogger = log.MustGetLogger("alarm")

type ListRuleGroupsParams struct {
	RuleType string
	Page     int
	PageSize int
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
		Duration     int 	   `gorm:"check:duration >= 1"`
        Over         int       `json:"over" gorm:"check:over >= 1"`                // 对应请求参数中的 over
        DownTo       int       `json:"down_to" gorm:"check:down_to >= 0"`           // 对应请求参数中的 down_to
        DownDuration int       `json:"down_duration" gorm:"check:down_duration >= 1"` // 对应请求参数中的 down_duration
		CreatedAt time.Time `gorm:"autoCreateTime"`
	}

	// 带宽规则表
	BWRule struct {
		ID           int       `gorm:"primaryKey;autoIncrement"`
		GroupUUID    string    `gorm:"column:group_uuid;type:varchar(36);index"`
		Name         string    `gorm:"size:255"`
		InDuration   int       `gorm:"check:in_duration >= 1"`
		InThreshold  int       `gorm:"check:in_threshold >= 1"`
		InCooldown   int       `gorm:"check:in_cooldown >= 1"`
		OutDuration  int       `gorm:"check:out_duration >= 1"`
		OutThreshold int       `gorm:"check:out_threshold >= 1"`
		OutCooldown  int       `gorm:"check:out_cooldown >= 1"`
		CreatedAt    time.Time `gorm:"autoCreateTime"`
	}

	    Alert struct {
        ID           uint      `gorm:"primaryKey;autoIncrement"`
        Name         string    `gorm:"size:255"`
        Status       string    `gorm:"type:varchar(20)"`
        InstanceUUID string    `gorm:"type:varchar(36);index"`
        Severity     string    `gorm:"type:varchar(20)"`
        Summary      string    `gorm:"type:text"`
        Description  string    `gorm:"type:text"`
        StartsAt     time.Time
        EndsAt       time.Time
        CreatedAt    time.Time `gorm:"autoCreateTime"`
    }
)

type AlarmOperator struct {
	DB *gorm.DB
}

func (a *AlarmOperator) GetCPURulesByGroupID(ctx context.Context, groupUUID string, rules *[]model.CPURuleDetail) error {
	ctx, db := GetContextDB(ctx)
	return db.Where("group_uuid = ?", groupUUID).Find(rules).Error
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


func (a *AlarmOperator) BatchLinkVMs(ctx context.Context, GroupUUID string, vmUUIDs []string) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for _, vmUUID := range vmUUIDs {
			link := &model.VMRuleLink{
				GroupUUID: GroupUUID,
				VMUUID:    vmUUID,
			}
			if err := tx.Create(link).Error; err != nil {
				alarmLogger.Error("create link failed",
					"GroupUUID", GroupUUID,
					"vmUUID", vmUUID,
					"error", err)
				return fmt.Errorf("create link failed: %w", err)
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

func (a *AlarmOperator) DeleteVMLink(ctx context.Context, groupUUID, vmUUID, ruleType string) (int64, error) {
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

func (a *AlarmOperator) CreateBWRules(ctx context.Context, groupUUID string, rules []BWRule) error {
	ctx, db := GetContextDB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range rules {
			rule := &model.BWRuleDetail{
				GroupUUID:      groupUUID,
				Name:         rules[i].Name,
				InDuration:   rules[i].InDuration,
				InThreshold:  rules[i].InThreshold,
				OutDuration:  rules[i].OutDuration,
				OutThreshold: rules[i].OutThreshold,
			}
			if err := tx.Create(rule).Error; err != nil {
				alarmLogger.Error("create bw rule failed",
					"groupUUID", groupUUID,
					"rule", rules[i],
					"error", err)
				return fmt.Errorf("create bw rule failed: %w", err)
			}
		}
		return nil
	})
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