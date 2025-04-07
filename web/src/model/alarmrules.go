package model

import (
	"time"

	"web/src/dbs" // 通过dbs包获取数据库实例
)

// 初始化数据库连接（与interface.go模式一致）
func init() {
	dbs.AutoMigrate(&RuleGroupV2{}, &CPURuleDetail{}, &BWRuleDetail{}, &VMRuleLink{})
}

// 新增规则组表
type RuleGroupV2 struct {
	ID         string `gorm:"type:varchar(36);primaryKey"`
	Name       string `gorm:"type:varchar(128);uniqueIndex"`
	Type       string `gorm:"type:varchar(32)"`
	Owner      string `gorm:"type:varchar(255);index"` // 修改为字符串类型
	Enabled    bool   `gorm:"default:true"`
	TriggerCnt int    `gorm:"default:0"`
	Model
	CreatedAt time.Time `gorm:"index"`
}

// CPU规则详情表
type CPURuleDetail struct {
	Model
	GroupID   string `gorm:"type:varchar(36);index"` // 外键关联RuleGroupV2
	Name      string `gorm:"type:varchar(128)"`      // 规则名称
	Threshold int    `gorm:"check:threshold >= 1"`   // 阈值百分比
	Duration  int    `gorm:"check:duration >= 1"`    // 持续时间(秒)
	Cooldown  int    `gorm:"check:cooldown >= 1"`    // 冷却时间
	Recovery  int    `gorm:"check:recovery <= 100"`  // 恢复阈值
}

// 带宽规则详情表
type BWRuleDetail struct {
	Model
	GroupID      string `gorm:"type:varchar(36);index"` // 外键关联RuleGroupV2
	Name         string `gorm:"type:varchar(128)"`
	InThreshold  int    `gorm:"check:in_threshold >= 1"` // 入站阈值(Mbps)
	InDuration   int    `gorm:"check:in_duration >= 1"`
	OutThreshold int    `gorm:"check:out_threshold >= 1"` // 出站阈值(Mbps)
	OutDuration  int    `gorm:"check:out_duration >= 1"`
}

// 现有结构体补充索引（原VMRuleLink结构保持不变）
type VMRuleLink struct {
	Model
	GroupID   string `gorm:"type:varchar(36);index"` // 外键关联RuleGroupV2
	VMName    string `gorm:"type:varchar(128);index"`
	Interface string `gorm:"type:varchar(32)"`
}
