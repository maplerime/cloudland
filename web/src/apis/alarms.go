package apis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template" // 移动到标准库分组
	"time"

	// 项目内部包
	"web/src/common"
	"web/src/model"

	// 第三方包
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jinzhu/gorm"
)

type AlarmAPI struct {
	operator *common.AlarmOperator // 添加 operator 字段
}

var alarmAPI = &AlarmAPI{
	operator: &common.AlarmOperator{},
}

/****************** 常量定义 ******************/
const (
	RuleTypeCPU  = "cpu"
	RuleTypeBW   = "bw"
	RulesEnabled = "/etc/prometheus/rules_enabled"
	RulesGeneral = "/etc/prometheus/general_rules"
	RulesSpecial = "/etc/prometheus/special_rules"
)

/****************** 结构体定义 ******************/
// 通用规则请求结构
type CreateRuleRequest struct {
	Name  string          `json:"name" binding:"required"`
	Rules json.RawMessage `json:"rules" binding:"required"`
}

// 新增通用规则详情结构体
type RuleDetail struct {
	ID      int    `gorm:"primaryKey;autoIncrement"`
	GroupID string `gorm:"type:varchar(36);index"`
	Name    string `json:"name"`
	// 修正字段映射关系
	Duration          int `json:"duration"`      // 对应duration字段
	Threshold         int `json:"over"`          // 对应over字段
	Cooldown          int `json:"down_duration"` // 对应down_duration
	RecoveryThreshold int `json:"down_to"`       // 对应down_to
	// 新增必要的时间字段
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// 修改后的带宽规则生成器（添加索引）
func generateBWRuleContent(rules []common.BWRule) (string, error) {
	var sb strings.Builder
	for i, rule := range rules {
		sb.WriteString(fmt.Sprintf(`
  - alert: HighInBandwidth_%s_%d
    expr: avg(rate(node_network_receive_bytes_total{device!="lo"}[1m])) *8 > %d
    for: "%ds"
    labels:
      direction: in

  - alert: HighOutBandwidth_%s_%d
    expr: avg(rate(node_network_transmit_bytes_total{device!="lo"}[1m])) *8 > %d
    for: "%ds"`,
			rule.Name, i, rule.InThreshold*1000000,
			rule.InDuration,
			rule.Name, i, rule.OutThreshold*1000000,
			rule.OutDuration))
	}
	return sb.String(), nil
}

/****************** 辅助函数 ******************/
func reloadPrometheus() error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", "http://localhost:9090/-/reload", nil)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus reload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus reload error: %s", resp.Status)
	}
	return nil
}

func rulePaths(ruleType, groupID string) (generalPath string, specialPath string) {
	return fmt.Sprintf("%s/cpu-general-%s.yml", RulesGeneral, groupID),
		fmt.Sprintf("%s/cpu-special-%s.yml", RulesSpecial, groupID)
}

func (a *AlarmAPI) LinkRuleToVM(c *gin.Context) {
	groupID := c.Param("group_id")
	vmName := c.Param("vm_name")

	// 使用operator替代直接数据库操作
	if err := a.operator.BatchLinkVMs(c.Request.Context(), groupID, []string{vmName}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 获取规则组改为使用operator方法
	group, err := a.operator.GetRuleGroupByID(c.Request.Context(), groupID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取规则组失败"})
		return
	}

	// 获取关联VM列表改为使用operator方法
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取VM列表失败"})
		return
	}

	// 构建排除列表
	var vmList []string
	for _, l := range vmLinks {
		vmList = append(vmList, l.VMName)
	}

	// 获取原始规则
	rules := a.getRulesFromDB(groupID)

	// 生成规则内容（使用operator返回的group.Type）
	generalPath, specialPath := rulePaths(group.Type, groupID)
	rawContent, err := generateCPURuleContent(rules)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成规则内容失败"})
		return
	}
	generalContent := editCPURuleContent(rawContent, vmList)
	specialContent, err := genspecialCPURuleContent(rules, vmList, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "特殊规则生成失败: " + err.Error()})
		return
	}

	// 写入文件（添加错误处理）
	if err := os.WriteFile(generalPath, []byte(generalContent), 0640); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "通用规则生成失败"})
		return
	}
	if err := os.WriteFile(specialPath, []byte(specialContent), 0640); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "特殊规则生成失败"})
		return
	}

	enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupID))
	if err := os.Symlink(specialPath, enabledPath); err != nil && !os.IsExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则激活失败"})
		return
	}

	if err := reloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "配置重载失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// 修改解绑接口
func (a *AlarmAPI) UnlinkRuleFromVM(c *gin.Context) {
	groupID := c.Param("group_id")
	vmName := c.Param("vm_name")

	group, err := a.operator.GetRuleGroupByID(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	}
	ruleType := group.Type

	// 修改前：DeleteVMLink
	// 修改后使用operator方法
	deleted, err := a.operator.DeleteVMLink(c.Request.Context(), groupID, vmName, ruleType)
	if err != nil || deleted == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解绑失败"})
		return
	}

	// 获取更新后的VM列表改为使用operator方法
	vms, err := a.operator.GetLinkedVMs(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取VM列表失败"})
		return
	}
	vmList := make([]string, 0, len(vms))
	for _, l := range vms {
		vmList = append(vmList, l.VMName)
	}

	// 更新规则文件（统一到组文件）
	rules := a.getRulesFromDB(groupID)
	_, specialPath := rulePaths(RuleTypeCPU, groupID)

	// 生成新的特殊规则内容
	specialContent, err := genspecialCPURuleContent(rules, vmList, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则更新失败"})
		return
	}

	// 原子写入文件
	if err := atomicWrite(specialPath, specialContent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件保存失败"})
		return
	}

	// 重载配置
	if err := reloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "配置重载失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// 新增原子写入函数
func atomicWrite(path string, content string) error {
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(content), 0640); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

/****************** 初始化函数 ******************/
// 修改参数类型为 AlarmOperator
func NewAlarmAPIv2(operator *common.AlarmOperator) *AlarmAPI {
	return &AlarmAPI{
		operator: operator,
	}
}

func (a *AlarmAPI) CreateCPURule(c *gin.Context) {
	var req struct {
		Name  string           `json:"name" binding:"required"`
		Owner string           `json:"owner" binding:"required"`
		Rules []common.CPURule `json:"rules" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 生成规则ID
	groupID := uuid.New().String() // 生成唯一规则ID
	// 该ID会同时存储到数据库和规则文件中

	// 获取已关联VM列表
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取VM关联列表失败"})
		return
	}

	// 生成排除列表
	var excludeVMs []string
	for _, link := range vmLinks {
		excludeVMs = append(excludeVMs, link.VMName)
	}

	// 生成规则内容
	generalRaw, err := generateCPURuleContent(req.Rules)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则生成失败"})
		return
	}
	generalContent := editCPURuleContent(generalRaw, excludeVMs)
	specialContent, _ := genspecialCPURuleContent(req.Rules, excludeVMs, groupID)

	enabledPath := RulesEnabled
	// 保存文件
	generalPath, specialPath := rulePaths(RuleTypeCPU, groupID)
	os.WriteFile(generalPath, []byte(generalContent), 0640)
	os.WriteFile(specialPath, []byte(specialContent), 0640)
	os.Symlink(generalPath, enabledPath)

	// 数据库存储
	group := &model.RuleGroupV2{
		ID:        groupID,
		Name:      req.Name,
		Type:      RuleTypeCPU,
		Owner:     req.Owner,
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则组创建失败"})
		return
	}

	reloadPrometheus()
}

// 修改生成函数参数类型
func generateCPURuleContent(rules []common.CPURule) (string, error) {
	var sb strings.Builder
	for i, rule := range rules {
		sb.WriteString(fmt.Sprintf(`
  - alert: HighCPUUsage_%s_%d
    expr: avg by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[1m])) * 100 > %d
    for: "%ds"
    labels:
      severity: warning
    annotations:
      summary: "High CPU Usage ({{ $value }}%%)"
      description: "Instance {{ $labels.instance }} has high CPU usage for %d seconds"

  - alert: CPUUsageRecovered_%s_%d
    expr: avg by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[1m])) * 100 < %d
    for: "%ds"
    labels:
      severity: info
    annotations:
      summary: "CPU Usage Recovered ({{ $value }}%%)"`,
			rule.Name, i, rule.Threshold,
			rule.Duration, rule.Duration, // 修正参数顺序
			rule.Name, i, rule.Recovery, // 使用正确的 Recovery 字段
			rule.Cooldown)) // 使用正确的 Cooldown 字段
	}
	return sb.String(), nil
}

func (a *AlarmAPI) GetCPURules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	// 使用operator分页查询
	groups, total, err := a.operator.ListRuleGroups(c.Request.Context(),
		common.ListRuleGroupsParams{ // 使用统一的分页参数结构
			RuleType: RuleTypeCPU,
			Page:     page,
			PageSize: pageSize,
		})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}

	response := gin.H{
		"data": groups,
		"meta": gin.H{
			"total":        total,
			"current_page": page,
			"per_page":     pageSize,
			"total_pages":  int(math.Ceil(float64(total) / float64(pageSize))),
		},
	}
	c.JSON(http.StatusOK, response)
}

func cleanExpiredRules(dir string, retention time.Duration) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if time.Since(info.ModTime()) > retention {
			os.Remove(path)
		}
		return nil
	})
}

func (a *AlarmAPI) DeleteCPURule(c *gin.Context) {
	groupID := c.Param("id")
	var vmList []string

	// 执行数据库事务
	if err := a.deleteCPURuleTransaction(groupID, &vmList); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 更新通用规则文件（移出黑名单）
	generalPath, _ := rulePaths(RuleTypeCPU, groupID)
	if content, err := os.ReadFile(generalPath); err == nil {
		newContent := editCPURuleContent(string(content), []string{})
		_ = atomicWrite(generalPath, newContent) // 静默失败不影响主流程
	}

	// 清理相关文件
	_, specialPath := rulePaths(RuleTypeCPU, groupID)
	enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupID))

	// 原子删除文件（允许文件不存在）
	os.Remove(generalPath) // 删除通用规则文件
	os.Remove(specialPath) // 删除特殊规则文件
	os.Remove(enabledPath) // 删除启用链接

	// 异步重载配置
	go func() {
		if err := reloadPrometheus(); err != nil {
			log.Printf("[DeleteCPURule] 配置重载失败: %v", err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// 事务处理函数
func (a *AlarmAPI) deleteCPURuleTransaction(groupID string, vmList *[]string) error {
	return a.operator.DeleteRuleGroupWithDependencies(context.Background(), groupID, RuleTypeCPU)
}

func (a *AlarmAPI) getLinkedVMs(groupID string) ([]string, error) {
	vmLinks, err := a.operator.GetLinkedVMs(context.Background(), groupID)
	if err != nil {
		return nil, fmt.Errorf("获取VM列表失败: %w", err)
	}

	vmList := make([]string, 0, len(vmLinks))
	for _, l := range vmLinks {
		vmList = append(vmList, l.VMName)
	}
	return vmList, nil
}

// 状态切换接口
func (a *AlarmAPI) ToggleRuleStatus(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	groupID := c.Param("id")
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupID, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "状态更新失败"})
		return
	}

	// 重载Prometheus配置使状态变更生效
	if err := reloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "配置重载失败"})
		return
	}
}

// 新增模板生成函数（放在 generateCPURuleContent 函数上方）
func generateRuleContent(ruleType, templatePath string, rules interface{}) (string, error) {
	// 读取模板文件
	tmplData, err := os.ReadFile(templatePath) // 替换为 os.ReadFile
	if err != nil {
		return "", fmt.Errorf("读取模板文件失败: %w", err)
	}

	// 创建模板
	tmpl, err := template.New(ruleType + "_alert_rules").Parse(string(tmplData))
	if err != nil {
		return "", fmt.Errorf("解析模板失败: %w", err)
	}

	// 执行模板渲染
	var output strings.Builder
	err = tmpl.Execute(&output, map[string]interface{}{
		"Rules": rules,
		"Type":  ruleType,
	})
	if err != nil {
		return "", fmt.Errorf("模板渲染失败: %w", err)
	}

	// 根据规则类型选择后续处理
	switch ruleType {
	case RuleTypeCPU:
		// 先获取内容再处理错误
		content, err := generateCPURuleContent(rules.([]common.CPURule))
		if err != nil {
			return "", fmt.Errorf("CPU规则生成失败: %w", err)
		}
		return content, nil
	case RuleTypeBW:
		return generateBWRuleContent(rules.([]common.BWRule))
	default:
		return "", fmt.Errorf("unsupported rule type: %s", ruleType)
	}
}

// 在常量定义部分添加
const (
	bwTemplatePath = "/etc/prometheus/rules.d/bw.yml"
)

// 带宽告警规则结构
type BWRuleV2 struct {
	Name         string `json:"name" binding:"required"`
	InDuration   int    `json:"in_bw_duration" binding:"required,min=1"`
	InThreshold  int    `json:"in_bw_over" binding:"required,min=1"`
	InDownTo     int    `json:"in_bw_down_to" binding:"required"`
	InCooldown   int    `json:"in_bw_down_duration" binding:"required,min=1"`
	OutDuration  int    `json:"out_bw_duration" binding:"required,min=1"`
	OutThreshold int    `json:"out_bw_over" binding:"required,min=1"`
	OutDownTo    int    `json:"out_bw_down_to" binding:"required"`
	OutCooldown  int    `json:"out_bw_down_duration" binding:"required,min=1"`
	RuleID       string `json:"rule_id" gorm:"index"`
}

// 新增 webhook 处理函数
func (a *AlarmAPI) HandleAlertWebhook(c *gin.Context) {
	var notification struct {
		Alerts []struct {
			Labels map[string]string `json:"labels"`
			Status string            `json:"status"`
		} `json:"alerts"`
	}

	if err := c.ShouldBindJSON(&notification); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的告警格式"})
		return
	}

	for _, alert := range notification.Alerts {
		if alert.Status == "firing" {
			ruleID := alert.Labels["rule_id"]
			if ruleID != "" {
				go func(id string) {
					if err := a.operator.IncrementTriggerCount(context.Background(), id); err != nil {
						log.Printf("更新触发次数失败: %v", err)
					}
				}(ruleID)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"processed": true})
}

// 生成特殊规则内容
// 修改函数签名添加 groupID 参数
func genspecialCPURuleContent(rules []common.CPURule, vmList []string, groupID string) (string, error) {
	if len(rules) == 0 {
		return "", fmt.Errorf("空规则列表")
	}

	var sb strings.Builder
	sb.WriteString("groups:\n- name: special_cpu_alerts\n  rules:\n")

	for i, rule := range rules {
		sb.WriteString(fmt.Sprintf(`
  - alert: SpecialCPU_%s_%d
    expr: avg by (instance) (
        rate(node_cpu_seconds_total{
            mode!="idle",
            instance=~"%s"
        }[1m])
    ) * 100 > %d
    for: "%ds"
    labels:
        rule_group: "%s"
        severity: critical
    annotations:
        description: "特殊CPU告警 - 实例 {{ $labels.instance }} 使用率超过 %d%% 持续 %d 秒"`,
			rule.Name, i,
			strings.Join(vmList, "|"), // 匹配所有关联的VM实例
			rule.Threshold,
			rule.Duration,
			groupID, // 添加规则组ID作为标签
			rule.Threshold,
			rule.Duration))
	}
	return sb.String(), nil
}

// 编辑通用规则内容
func editCPURuleContent(original string, excludeVMs []string) string {
	exclusion := ""
	if len(excludeVMs) > 0 {
		exclusion = fmt.Sprintf(",instance!~\"%s\"", strings.Join(excludeVMs, "|"))
	}
	return strings.ReplaceAll(original, "{mode!=\"idle\"}", fmt.Sprintf("{mode!=\"idle\"%s}", exclusion))
}

func (a *AlarmAPI) EnableRules(c *gin.Context) {
	groupID := c.Param("id")

	// 使用operator获取规则组
	group, err := a.operator.GetRuleGroupByID(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	}

	if group.Enabled {
		c.JSON(http.StatusOK, gin.H{"status": "already enabled"})
		return
	}

	// 获取关联的VM列表
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取VM关联失败"})
		return
	}

	var vmList []string
	for _, l := range vmLinks {
		vmList = append(vmList, l.VMName)
	}

	enabledPath := RulesEnabled
	generalPath, specialPath := rulePaths(group.Type, groupID)

	// 检查文件是否存在
	if _, err := os.Stat(generalPath); os.IsNotExist(err) {
		rules := a.getRulesFromDB(groupID)
		rawContent, err := generateCPURuleContent(rules)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "规则生成失败"})
			return
		}
		generalContent := editCPURuleContent(rawContent, vmList)
		os.WriteFile(generalPath, []byte(generalContent), 0640)
	}

	if _, err := os.Stat(specialPath); os.IsNotExist(err) {
		rules := a.getRulesFromDB(groupID)
		specialContent, _ := genspecialCPURuleContent(rules, vmList, groupID)
		os.WriteFile(specialPath, []byte(specialContent), 0640)
	}

	// 创建启用链接
	if _, err := os.Stat(enabledPath); os.IsNotExist(err) {
		os.Symlink(generalPath, enabledPath)
	}

	// 使用operator更新状态
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupID, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "状态更新失败"})
		return
	}

	reloadPrometheus()
}

func (a *AlarmAPI) DisableRules(c *gin.Context) {
	groupID := c.Param("id")

	// 使用operator获取规则组
	group, err := a.operator.GetRuleGroupByID(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	}

	if !group.Enabled {
		c.JSON(http.StatusOK, gin.H{"status": "already disabled"})
		return
	}

	// 删除启用链接
	enabledPath := RulesEnabled
	if _, err := os.Stat(enabledPath); err == nil {
		os.Remove(enabledPath)
	}

	// 使用operator更新状态
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupID, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "状态更新失败"})
		return
	}

	reloadPrometheus()
}

// 告警查询接口
func (a *AlarmAPI) GetCurrentAlarms(c *gin.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:9090/api/v1/alerts")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "响应解析失败"})
		return
	}

	// 新增过滤逻辑
	if data, exists := result["data"]; exists {
		if alerts, ok := data.([]interface{}); ok {
			filtered := filterActiveAlerts(alerts)
			result["data"] = filtered
		} else {
			log.Printf("告警数据格式异常: %T", data)
		}
	}

	c.JSON(http.StatusOK, result)
}

func (a *AlarmAPI) GetHistoryAlarm(c *gin.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:9090/api/v1/query?query=ALERTS[30d]")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	c.JSON(http.StatusOK, result)
}

func (a *AlarmAPI) getRulesFromDB(groupID string) []common.CPURule {
	var rules []model.CPURuleDetail
	if err := a.operator.GetCPURulesByGroupID(context.Background(), groupID, &rules); err != nil {
		return nil
	}

	// 模型转换
	var result []common.CPURule
	for _, r := range rules {
		result = append(result, common.CPURule{
			Name:      r.Name,
			Threshold: r.Threshold,
			Duration:  r.Duration,
			Cooldown:  r.Cooldown,
		})
	}
	return result
}

func filterActiveAlerts(alerts []interface{}) []interface{} {
	var filtered []interface{}
	for _, a := range alerts {
		alert, ok := a.(map[string]interface{})
		if !ok {
			continue
		}

		// 检查状态是否为 firing
		if status, ok := alert["state"].(string); ok && status == "firing" {
			// 添加规则组过滤逻辑（根据需要扩展）
			if labels, ok := alert["labels"].(map[string]interface{}); ok {
				if _, hasRuleGroup := labels["rule_group"]; hasRuleGroup {
					filtered = append(filtered, alert)
				}
			}
		}
	}
	return filtered
}

func validateRuleJSON(ruleType string, raw json.RawMessage) error {
	switch ruleType {
	case RuleTypeCPU:
		var rules []common.CPURule
		return json.Unmarshal(raw, &rules)
	case RuleTypeBW:
		var rules []common.BWRule
		return json.Unmarshal(raw, &rules)
	default:
		return fmt.Errorf("unsupported rule type")
	}
}
