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
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
    "os/user"
	"web/src/common"
	"web/src/model"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

type AlarmAPI struct {
	operator *common.AlarmOperator
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
	Duration          int `json:"duration"`      // 对应duration字段
	Threshold         int `json:"over"`          // 对应over字段
	Cooldown          int `json:"down_duration"` // 对应down_duration
	RecoveryThreshold int `json:"down_to"`       // 对应down_to
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

func reloadPrometheus() error {
    cmd := exec.Command("sudo", "systemctl", "kill", "-s", "SIGHUP", "prometheus.service")
    if output, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("SIGHUP operation failed: %v, output: %s", err, string(output))
    }
    return nil
}

func rulePaths(ruleType, groupID string) (generalPath string, specialPath string) {
	return fmt.Sprintf("%s/cpu-general-%s.yml", RulesGeneral, groupID),
		fmt.Sprintf("%s/cpu-special-%s.yml", RulesSpecial, groupID)
}

func (a *AlarmAPI) LinkRuleToVM(c *gin.Context) {
	groupUUID := c.Param("group_uuid")
	vmName := c.Param("vm_name")

	// 使用operator替代直接数据库操作
	if err := a.operator.BatchLinkVMs(c.Request.Context(), groupUUID, []string{vmName}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 获取规则组改为使用operator方法
	group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取规则组失败"})
		return
	}

	// 获取关联VM列表改为使用operator方法
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
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
	rules := a.getRulesFromDB(groupUUID)

	// 生成规则内容（使用operator返回的group.Type）
	generalPath, specialPath := rulePaths(group.Type, groupUUID)
	rawContent, err := generateCPURuleContent(rules, group.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成规则内容失败"})
		return
	}
	generalContent := addExcludedVMsToRule(rawContent, vmList)
	specialContent, err := genspecialCPURuleContent(rules, vmList, groupUUID)
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

	enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupUUID))
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
	groupUUID := c.Param("group_uuid")
	vmName := c.Param("vm_name")

	group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	}
	ruleType := group.Type

	// 修改前：DeleteVMLink
	// 修改后使用operator方法
	deleted, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, vmName, ruleType)
	if err != nil || deleted == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解绑失败"})
		return
	}

	// 获取更新后的VM列表改为使用operator方法
	vms, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取VM列表失败"})
		return
	}
	vmList := make([]string, 0, len(vms))
	for _, l := range vms {
		vmList = append(vmList, l.VMName)
	}

	// 更新规则文件（统一到组文件）
	rules := a.getRulesFromDB(groupUUID)
	_, specialPath := rulePaths(RuleTypeCPU, groupUUID)

	// 生成新的特殊规则内容
	specialContent, err := genspecialCPURuleContent(rules, vmList, groupUUID)
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
	fmt.Printf("step0\n")
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fmt.Printf("step1\n")	
	group := &model.RuleGroupV2{
		Name:      req.Name,
		Type:      RuleTypeCPU,
		Owner:     req.Owner,
		Enabled:   true,
	}
	fmt.Printf("step2\n")
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(
            http.StatusInternalServerError, 
            gin.H{ "error": "operator failed: " + err.Error(),},
        )
        return
	}
	fmt.Printf("step3\n")
	for _, rule := range req.Rules {
        detail := &model.CPURuleDetail{
            GroupUUID:  group.UUID, 
            Name:       rule.Name,
            Over:       rule.Over,
        	Duration:   rule.Duration,
        	DownDuration: rule.DownDuration,
        	DownTo:     rule.DownTo,
        }
        if err := a.operator.CreateCPURuleDetail(c.Request.Context(), detail); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "创建规则明细失败: " + err.Error()})
            return
        }
    }
	fmt.Printf("step4\n")	

    groupUUID := group.UUID
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"call get link vm error": err.Error()})
		return
	}
    pu, err := user.Lookup("prometheus")
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "查找prometheus用户失败: " + err.Error()})
        return
    }
    uid, _ := strconv.Atoi(pu.Uid)
    gid, _ := strconv.Atoi(pu.Gid)
	var excludeVMs []string
	if len(vmLinks) > 0 {
        for _, link := range vmLinks {
            excludeVMs = append(excludeVMs, link.VMName)
        }
    }
	fmt.Printf("step5\n")	
	// 生成规则内容
	generalRaw, err := generateCPURuleContent(req.Rules, group.Name, excludeVMs...)
	fmt.Printf("完整规则内容：\n%s\n", generalRaw)
	fmt.Printf("err内容：\n%s\n", err)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"generate general rules failed": err.Error()})
		return
	}
	fmt.Printf("step6：\n%s\n", err)
	generalPath, specialPath := rulePaths(RuleTypeCPU, groupUUID)
	fmt.Printf("specialPath is: %s\n",  specialPath)
	if err := os.WriteFile(generalPath, []byte(generalRaw), 0640); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"write general rules filed failed": err.Error()})
        return
    }
	if err := os.Chown(generalPath, uid, gid); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "set file owner filed: " + err.Error()})
        return
    }
	fmt.Printf("step7：\n%s\n", err)
	if len(excludeVMs) > 0 {
		fmt.Printf("excludeVMs existing but may uelsess for this part\n")
		/*
    	var specialContent string
		if specialContent, err = genspecialCPURuleContent(req.Rules, excludeVMs, groupUUID); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "generate special rules failed: " + err.Error()})
            return
        }
        if err := os.WriteFile(specialPath, []byte(specialContent), 0640); err != nil {
            c.JSON(http.StatusInternalServerError,gin.H{"write special rules filed failed": err.Error()})
            return
        }
		if err := os.Chown(specialPath, uid, gid); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "set file owner filed: " + err.Error()})
            return
        }
        enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupUUID))
		fmt.Printf("1enabledPath is: %s\n",  enabledPath)
        if err := os.Symlink(specialPath, enabledPath); err != nil && !os.IsExist(err) {
            c.JSON(http.StatusInternalServerError, gin.H{"enable special rules filed failed": err.Error()})
            return
        }
		if err := os.Lchown(enabledPath, uid, gid); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "set file link failed: " + err.Error()})
            return
        }
		*/
    }else {
        enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-general-%s.yml", groupUUID))
		fmt.Printf("2enabledPath is: %s\n",  enabledPath)
        if err := os.Symlink(generalPath, enabledPath); err != nil && !os.IsExist(err) {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "enable general rules filed failed: " + err.Error()})
            return
        }
		if err := os.Lchown(enabledPath, uid, gid); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "set file link failed: " + err.Error()})
            return
        }
    }
	reloadPrometheus()
	c.JSON(http.StatusOK, gin.H{
        "status": "success",
        "data": gin.H{
            "group_uuid":    groupUUID,
            "enabled":      true,
        },
    })
}

func generateCPURuleContent(rules []common.CPURule, groupName string, excludeVMs ...string) (string, error) {
	var sb strings.Builder
	fmt.Printf("正在生成CPU规则，共 %d 条规则\n", len(rules))
	sb.WriteString(fmt.Sprintf("groups:\n- name: %s\n  rules:", groupName))
	filter := ""
    if len(excludeVMs) > 0 && len(excludeVMs[0]) > 0 {
        filter = fmt.Sprintf(`{instance!~"%s"}`, strings.Join(excludeVMs, "|"))
    }
	for i, rule := range rules {
		if rule.Over <= 0 || rule.DownTo <= 0 {
			return "", fmt.Errorf("规则 #%d 校验失败：阈值参数必须大于0", i)
		}
		if rule.Over <= rule.DownTo {
			return "", fmt.Errorf("规则 #%d 校验失败：触发阈值(%d%%)必须大于恢复阈值(%d%%)", 
				i, rule.Over, rule.DownTo)
		}
		sb.WriteString(fmt.Sprintf(`
  - alert: HighCPUUsage_%s_%d
    expr: |-
      (sum by (domain) (rate(libvirt_domain_info_cpu_time_seconds_total%s[1m]))
        / on (domain) group_left() libvirt_domain_info_virtual_cpus) * 100 > %d
    for: %ds
    labels:
      severity: warning
    annotations:
      summary: "High VM Usage ({{ $value }})"
      description: "VM {{ $labels.domain }} has high CPU usage for %d seconds"
  - alert: CPUUsageRecovered_%s_%d
    expr: |-
      (sum by (domain) (rate(libvirt_domain_info_cpu_time_seconds_total%s[1m]))
        / on (domain) group_left() libvirt_domain_info_virtual_cpus) * 100 < %d
    for: %ds
    labels:
      severity: info
    annotations:
      summary: "VM CPU Usage Recovered ({{ $value }})"
      description: "VM {{ $labels.domain }} CPU usage has recovered below threshold for %d seconds"`,
            rule.Name, i, filter,
            rule.Over,
            rule.Duration, rule.Duration,
            rule.Name, i, filter,
            rule.DownTo,
            rule.DownDuration, rule.DownDuration))
	}
	result := sb.String()
	fmt.Printf("生成完整规则内容：\n%s\n", result)
	fmt.Printf("wngzhe return nil")
	return sb.String() + "\n", nil
}

func (a *AlarmAPI) GetCPURules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	groupUUID := c.Param("uuid")
	if page < 1 {
        page = 1
    }
    if pageSize < 1 || pageSize > 1000 {
        pageSize = 20  // 仅当非法值时重置为默认值
    }

	fmt.Printf("[Debug] 查询参数 => RuleType:%s GroupUUID:%s Page:%d PageSize:%d\n", 
        RuleTypeCPU, groupUUID, page, pageSize)
	queryParams := common.ListRuleGroupsParams{
        RuleType: RuleTypeCPU,
        Page:     page,
        PageSize: pageSize,
    }
    
    // 当存在UUID时添加精确查询条件
    if groupUUID != "" {
        queryParams.GroupUUID = groupUUID
        queryParams.PageSize = 1 // 单个查询时限制为1条
    }

	// 使用operator分页查询
	fmt.Printf("[Debug] 查询参数结构 => RuleType:%s Page:%d PageSize:%d GroupUUID:%s\n", 
        queryParams.RuleType, queryParams.Page, queryParams.PageSize, queryParams.GroupUUID)
	fmt.Printf("GetCPURules: groupUUID is: %s\n",  groupUUID)
	fmt.Printf("GetCPURules: queryParams => %+v\n", queryParams)
	groups, total, err := a.operator.ListRuleGroups(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query rules group failed: " + err.Error()})
		return
	}
	fmt.Printf("wngzhe ListRuleGroups result - groups: %+v\n", groups)
	fmt.Printf("wngzhe ListRuleGroups result - total records: %d\n", total)
    // 构建响应数据结构
    responseData := make([]gin.H, 0, len(groups))
    for _, group := range groups {
        details, err := a.operator.GetCPURuleDetails(c.Request.Context(), group.UUID)
		fmt.Printf("wngzhe ListRuleGroups result - details: %+v\n", details)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "function get cpu rule detailed failed: " + err.Error()})
            return
        }

        ruleDetails := make([]gin.H, 0, len(details))
        for _, d := range details {
            ruleDetails = append(ruleDetails, gin.H{
				"id":          	 d.ID,
                "uuid":          d.UUID,
                "name":          d.Name,
                "duration":      d.Duration,
                "over":          d.Over,
                "down_to":       d.DownTo,
                "down_duration": d.DownDuration,
            })
        }

        responseData = append(responseData, gin.H{
            "id":          group.ID,
            "uuid":        group.UUID,
            "name":        group.Name,
            "status":      group.TriggerCnt,
            "create_time": group.CreatedAt.Format(time.RFC3339),
            "rules":       ruleDetails,
            "enabled":     group.Enabled,
        })
    }

    // 返回标准化响应
    c.JSON(http.StatusOK, gin.H{
        "data": responseData,
        "meta": gin.H{
            "total":        total,
            "current_page": page,
            "per_page":     pageSize,
            "total_pages":  int(math.Ceil(float64(total)/float64(pageSize))),
        },
    })
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
	groupUUID := c.Param("uuid")
    if groupUUID == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "empty UUID error."})
        return
    }
	fmt.Printf("wngzhe DeleteCPURule step0")
	if _, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU); err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
                "error": "rules not existed",
                "code":  "RESOURCE_NOT_FOUND",
                "uuid":  groupUUID,
            })
        } else {
            c.JSON(http.StatusInternalServerError, gin.H{
                "error": "server internal failure",
                "code":  "INTERNAL_ERROR",
                "uuid":  groupUUID,
            })
        }
        return
    }
	fmt.Printf("wngzhe DeleteCPURule step1")
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
    var excludeVMs []string
    if err == nil {
        for _, link := range vmLinks {
            excludeVMs = append(excludeVMs, link.VMName)
        }
    }
	fmt.Printf("[DeleteCPURule] vmLinks: %v", vmLinks)
	fmt.Printf("[DeleteCPURule] excludeVMs: %v", excludeVMs)
	fmt.Printf("wngzhe DeleteCPURule step1")
	generalPath, specialPath := rulePaths(RuleTypeCPU, groupUUID)
	fmt.Printf("[DeleteCPURule] generalPath: %v", generalPath)
	if len(excludeVMs) > 0 {
        if content, err := os.ReadFile(generalPath); err == nil {
            newContent := removeExcludedVMs(string(content), excludeVMs)
            _ = atomicWrite(generalPath, newContent)
        }
    }

	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, RuleTypeCPU); err != nil {
		fmt.Printf("[DeleteCPURule] DB operation failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB operation failed: " + err.Error()})
		return
	}
	fmt.Printf("wngzhe DeleteCPURule step2")
	// 更新通用规则文件（移出黑名单）
    enabledPaths := []string{
        filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupUUID)),
        filepath.Join(RulesEnabled, fmt.Sprintf("cpu-general-%s.yml", groupUUID)),
    }

	for _, path := range append([]string{generalPath, specialPath}, enabledPaths...) {
		os.Remove(path)
	}
	fmt.Printf("wngzhe DeleteCPURule step3")
	if err := reloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "配置重载失败"})
		return
	}

	fmt.Printf("wngzhe DeleteCPURule step4")
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": []string{generalPath, specialPath},
		},
	})
}

func removeExcludedVMs(original string, excludeVMs []string) string {
    // 匹配现有的实例排除规则
    re := regexp.MustCompile(`instance!~"([^"]*)"`)
    matches := re.FindStringSubmatch(original)
    
    // 解析现有黑名单
    existingVMs := make(map[string]bool)
    if len(matches) > 1 {
        for _, vm := range strings.Split(matches[1], "|") {
            existingVMs[vm] = true
        }
    }
    
    // 移除需要排除的VM
    for _, vm := range excludeVMs {
        delete(existingVMs, vm)
    }
    
    // 生成新的排除规则
    var newExclusion string
    if len(existingVMs) > 0 {
        var vmList []string
        for vm := range existingVMs {
            vmList = append(vmList, vm)
        }
        newExclusion = fmt.Sprintf(`,instance!~"%s"`, strings.Join(vmList, "|"))
    }
    
    // 替换原始内容
    return re.ReplaceAllString(original, newExclusion)
}

func (a *AlarmAPI) getLinkedVMs(groupUUID string) ([]string, error) {
	vmLinks, err := a.operator.GetLinkedVMs(context.Background(), groupUUID)
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

func genspecialCPURuleContent(rules []common.CPURule, vmList []string, groupID string) (string, error) {
    if len(rules) == 0 {
        return "", fmt.Errorf("empty rule list")
    }

    var sb strings.Builder
    sb.WriteString("groups:\n- name: special_cpu_alerts\n  rules:\n")

    for i, rule := range rules {
        sb.WriteString(fmt.Sprintf(`
  - alert: SpecialCPU_%s_%d
    expr: (sum by (domainname) (rate(libvirt_domain_info_vcpu_time_seconds{domainname=~"%s"}[1m])) 
           / on (domainname) group_left(vcpus) libvirt_domain_info_vcpus) * 100 > %d
    for: "%ds"
    labels:
        rule_group: "%s"
        severity: critical
    annotations:
        description: "Special CPU Alert - VM {{ $labels.domainname }} ({{ $labels.vcpus }} cores) exceeded %d%% for %d seconds"`,
            rule.Name, i,
            strings.Join(vmList, "|"),
            rule.Over,
            rule.Duration,
            groupID,
            rule.Over,
            rule.Duration))
    }
    return sb.String(), nil
}

func addExcludedVMsToRule(original string, excludeVMs []string) string {
    exclusion := ""
    if len(excludeVMs) > 0 {
        exclusion = fmt.Sprintf(`,instance!~"%s"`, strings.Join(excludeVMs, "|"))
    }
    return strings.ReplaceAll(original, "{__vm_exclusion__}", fmt.Sprintf("{%s}", exclusion))
}

func (a *AlarmAPI) EnableRules(c *gin.Context) {
	groupUUID := c.Param("id")

	// 使用operator获取规则组
	group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则组不存在"})
		return
	}

	if group.Enabled {
		c.JSON(http.StatusOK, gin.H{"status": "already enabled"})
		return
	}

	// 获取关联的VM列表
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取VM关联失败"})
		return
	}

	var vmList []string
	for _, l := range vmLinks {
		vmList = append(vmList, l.VMName)
	}

	enabledPath := RulesEnabled
	generalPath, specialPath := rulePaths(group.Type, groupUUID)

	// 检查文件是否存在
	if _, err := os.Stat(generalPath); os.IsNotExist(err) {
		rules := a.getRulesFromDB(groupUUID)
		rawContent, err := generateCPURuleContent(rules, group.Name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "规则生成失败"})
			return
		}
		generalContent := addExcludedVMsToRule(rawContent, vmList)
		os.WriteFile(generalPath, []byte(generalContent), 0640)
	}

	if _, err := os.Stat(specialPath); os.IsNotExist(err) {
		rules := a.getRulesFromDB(groupUUID)
		specialContent, _ := genspecialCPURuleContent(rules, vmList, groupUUID)
		os.WriteFile(specialPath, []byte(specialContent), 0640)
	}

	// 创建启用链接
	if _, err := os.Stat(enabledPath); os.IsNotExist(err) {
		os.Symlink(generalPath, enabledPath)
	}

	// 使用operator更新状态
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupUUID, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "状态更新失败"})
		return
	}

	reloadPrometheus()
}

func (a *AlarmAPI) DisableRules(c *gin.Context) {
	groupUUID := c.Param("id")

	// 使用operator获取规则组
	group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
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
	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupUUID, false); err != nil {
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
			Name:        r.Name,
			Over:   r.Over,
			Duration:    r.Duration,
			DownDuration:    r.DownDuration,
			DownTo:      r.DownTo,
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
