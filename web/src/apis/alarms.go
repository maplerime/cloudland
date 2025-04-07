package apis

import (
    "bytes"
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
	"unsafe"
    "io"
	"web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

type AlarmAPI struct {
	operator *common.AlarmOperator
}

var alarmAPI = &AlarmAPI{
	operator: &common.AlarmOperator{},
}
var (
    alarmPrometheusIP   string
    alarmPrometheusPort int
)

const (
	RuleTypeCPU  = "cpu"
	RuleTypeBW   = "bw"
	RulesEnabled = "/etc/prometheus/rules_enabled"
	RulesGeneral = "/etc/prometheus/general_rules"
	RulesSpecial = "/etc/prometheus/special_rules"
)

type CreateRuleRequest struct {
	Name  string          `json:"name" binding:"required"`
	Rules json.RawMessage `json:"rules" binding:"required"`
}

type RuleDetail struct {
	ID      int    `gorm:"primaryKey;autoIncrement"`
	GroupID string `gorm:"type:varchar(36);index"`
	Name    string `json:"name"`
	Duration          int `json:"duration"`
	Threshold         int `json:"over"`
	Cooldown          int `json:"down_duration"`
	RecoveryThreshold int `json:"down_to"`
}

func init() {
    viper.SetConfigFile("conf/config.toml")
    if err := viper.ReadInConfig(); err == nil {
        alarmPrometheusIP = viper.GetString("monitor.host")
        alarmPrometheusPort = viper.GetInt("monitor.port")
    }
    if alarmPrometheusIP == "" {
        alarmPrometheusIP = "localhost"
    }
    if alarmPrometheusPort == 0 {
        alarmPrometheusPort = 9090
    }
}

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

    // Use operator instead of direct DB operations
    if err := a.operator.BatchLinkVMs(c.Request.Context(), groupUUID, []string{vmName}); err != nil {
        log.Printf("Failed to link VM to rule group: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create VM association"})
        return
    }

    // Retrieve rule group using operator method
    group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
    if errors.Is(err, gorm.ErrRecordNotFound) {
        c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
        return
    } else if err != nil {
        log.Printf("Error retrieving rule group: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rule group"})
        return
    }

    // Get associated VMs using operator
    vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
    if err != nil {
        log.Printf("Error getting linked VMs: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get VM list"})
        return
    }

    // Build exclusion list
    var vmList []string
    for _, l := range vmLinks {
        vmList = append(vmList, l.VMName)
    }
	type ExtendedGroup struct {
		model.RuleGroupV2
		Details []model.CPURuleDetail
	}
	details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
	rules := make([]common.CPURule, 0, len(details))
    for _, d := range details {
        rules = append(rules, common.CPURule{
            Name:         d.Name,
            Duration:     d.Duration,
            Over:         d.Over,
            DownDuration: d.DownDuration,
            DownTo:       d.DownTo,
        })
    }

    // Generate rule content
    generalPath, specialPath := rulePaths(group.Type, groupUUID)
    rawContent, err := generateCPURuleContent(rules, group.Name, groupUUID)
    if err != nil {
        log.Printf("Rule content generation failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule content generation failed"})
        return
    }
    
    generalContent := addExcludedVMsToRule(rawContent, vmList)
    specialContent, err := genspecialCPURuleContent(rules, vmList, groupUUID)
    if err != nil {
        log.Printf("Special rule generation failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate special rules"})
        return
    }

    // Write rule files
    if err := os.WriteFile(generalPath, []byte(generalContent), 0640); err != nil {
        log.Printf("Failed to write general rules: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "General rule file creation failed"})
        return
    }
    
    if err := os.WriteFile(specialPath, []byte(specialContent), 0640); err != nil {
        log.Printf("Failed to write special rules: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Special rule file creation failed"})
        return
    }

    // Activate rules
    enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupUUID))
    if err := os.Symlink(specialPath, enabledPath); err != nil && !os.IsExist(err) {
        log.Printf("Rule activation failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule activation failed"})
        return
    }

    // Reload Prometheus configuration
    if err := reloadPrometheus(); err != nil {
        log.Printf("Prometheus reload failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "status": "success",
        "data": gin.H{
            "group_uuid": groupUUID,
            "linked_vm":  vmName,
        },
    })
}

func (a *AlarmAPI) UnlinkRuleFromVM(c *gin.Context) {
    groupUUID := c.Param("group_uuid")
    vmName := c.Param("vm_name")

    // Get rule group details
    group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
    if errors.Is(err, gorm.ErrRecordNotFound) {
        c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
        return
    } else if err != nil {
        log.Printf("Failed to retrieve rule group: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
        return
    }

    // Delete VM association
    deletedCount, err := a.operator.DeleteVMLink(c.Request.Context(), groupUUID, vmName, group.Type)
    if err != nil || deletedCount == 0 {
        log.Printf("VM unlinking failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unlink VM from rule group"})
        return
    }

    // Get updated VM list
    vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
    if err != nil {
        log.Printf("Failed to get updated VM list: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve VM associations"})
        return
    }

    // Build exclusion list
    vmList := make([]string, 0, len(vmLinks))
    for _, link := range vmLinks {
        vmList = append(vmList, link.VMName)
    }
	type ExtendedGroup struct {
		model.RuleGroupV2
		Details []model.CPURuleDetail
	}
	details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
    // Generate special rule content
    _, specialPath := rulePaths(group.Type, groupUUID)
	convertedRules := make([]common.CPURule, 0, len(details))
    for _, d := range details {
        convertedRules = append(convertedRules, common.CPURule{
            Name:         d.Name,
            Duration:     d.Duration,
            Over:         d.Over,
            DownDuration: d.DownDuration,
            DownTo:       d.DownTo,
        })
    }
    specialContent, err := genspecialCPURuleContent(convertedRules, vmList, groupUUID) 
    if err != nil {
        log.Printf("Rule generation failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule content generation failed"})
        return
    }

    // Atomic file update
    if err := atomicWrite(specialPath, specialContent); err != nil {
        log.Printf("File write failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update rule files"})
        return
    }

    // Reload Prometheus configuration
    if err := reloadPrometheus(); err != nil {
        log.Printf("Config reload failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "status": "success",
        "data": gin.H{
            "unlinked_vm":  vmName,
            "remaining_vms": vmList,
        },
    })
}

func atomicWrite(path string, content string) error {
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(content), 0640); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

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
	group := &model.RuleGroupV2{
		Name:      req.Name,
		Type:      RuleTypeCPU,
		Owner:     req.Owner,
		Enabled:   true,
	}
	if err := a.operator.CreateRuleGroup(c.Request.Context(), group); err != nil {
		c.JSON(
            http.StatusInternalServerError, 
            gin.H{ "error": "operator failed: " + err.Error(),},
        )
        return
	}
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
            c.JSON(http.StatusInternalServerError, gin.H{"error": "create rule detail failed: " + err.Error()})
            return
        }
    }
    groupUUID := group.UUID
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"call get link vm error": err.Error()})
		return
	}
    pu, err := user.Lookup("prometheus")
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "check prometheus user failed: " + err.Error()})
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
	generalRaw, err := generateCPURuleContent(req.Rules, group.Name, groupUUID, excludeVMs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"generate general rules failed": err.Error()})
		return
	}
	generalPath, _ := rulePaths(RuleTypeCPU, groupUUID)
	if err := os.WriteFile(generalPath, []byte(generalRaw), 0640); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"write general rules filed failed": err.Error()})
        return
    }
	if err := os.Chown(generalPath, uid, gid); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "set file owner filed: " + err.Error()})
        return
    }
    enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-general-%s.yml", groupUUID))
    if err := os.Symlink(generalPath, enabledPath); err != nil && !os.IsExist(err) {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "enable general rules filed failed: " + err.Error()})
        return
    }
    if err := os.Lchown(enabledPath, uid, gid); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "set file link failed: " + err.Error()})
        return
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

func generateCPURuleContent(rules []common.CPURule, groupName string, groupUUID string, excludeVMs ...string) (string, error) {
	var sb strings.Builder
	fullGroupName := fmt.Sprintf("cpu_general_%s", groupUUID)
    sb.WriteString(fmt.Sprintf("groups:\n- name: %s\n  rules:", fullGroupName))
	filter := ""
    if len(excludeVMs) > 0 && len(excludeVMs[0]) > 0 {
        filter = fmt.Sprintf(`{instance!~"%s"}`, strings.Join(excludeVMs, "|"))
    }
	for i, rule := range rules {
		if rule.Over <= 0 || rule.DownTo <= 0 {
			return "", fmt.Errorf("rule #%d verify failed：must be greater than 0", i)
		}
		if rule.Over <= rule.DownTo {
			return "", fmt.Errorf("rule #%d verify failed：trigger (%d%%) must be greater than(%d%%)", 
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
      rule_group: "%s" 
      alert_type: cpu
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
      rule_group: "%s"
      alert_type: cpu
    annotations:
      summary: "VM CPU Usage Recovered ({{ $value }})"
      description: "VM {{ $labels.domain }} CPU usage has recovered below threshold for %d seconds"`,
      rule.Name, i, filter,
      rule.Over,
      rule.Duration,
      groupUUID,
      rule.Duration,
      rule.Name, i, filter,
      rule.DownTo,
      rule.DownDuration,
      groupUUID,
      rule.DownDuration))
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
        pageSize = 20
    }

	queryParams := common.ListRuleGroupsParams{
        RuleType: RuleTypeCPU,
        Page:     page,
        PageSize: pageSize,
    }
    
    if groupUUID != "" {
        queryParams.GroupUUID = groupUUID
        queryParams.PageSize = 1
    }

	groups, total, err := a.operator.ListRuleGroups(c.Request.Context(), queryParams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query rules group failed: " + err.Error()})
		return
	}
    responseData := make([]gin.H, 0, len(groups))
    for _, group := range groups {
        details, err := a.operator.GetCPURuleDetails(c.Request.Context(), group.UUID)
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
	vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
    var excludeVMs []string
    if err == nil {
        for _, link := range vmLinks {
            excludeVMs = append(excludeVMs, link.VMName)
        }
    }
	generalPath, specialPath := rulePaths(RuleTypeCPU, groupUUID)
	if len(excludeVMs) > 0 {
        if content, err := os.ReadFile(generalPath); err == nil {
            newContent := removeExcludedVMs(string(content), excludeVMs)
            _ = atomicWrite(generalPath, newContent)
        }
    }

	if err := a.operator.DeleteRuleGroupWithDependencies(c.Request.Context(), groupUUID, RuleTypeCPU); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB operation failed: " + err.Error()})
		return
	}
    patterns := []string{
        fmt.Sprintf("%s/cpu-*_%s*", RulesGeneral, groupUUID),
        fmt.Sprintf("%s/cpu-*_%s*", RulesSpecial, groupUUID),
        fmt.Sprintf("%s/cpu-*_%s*", RulesEnabled, groupUUID),
        fmt.Sprintf("%s/*_%s.yml", RulesEnabled, groupUUID),
    }
    
    for _, pattern := range patterns {
        matches, _ := filepath.Glob(pattern)
        log.Printf("[Cleanup] Pattern: %s, Matches: %v", pattern, matches)
        for _, path := range matches {
            if err := os.Remove(path); err != nil {
                log.Printf("[Cleanup] Failed to remove %s: %v", path, err)
            }
        }
    }
	if err := reloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reload Prometheus failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"deleted_group": groupUUID,
			"deleted_files": []string{generalPath, specialPath},
		},
	})
}

func removeExcludedVMs(original string, excludeVMs []string) string {
    re := regexp.MustCompile(`instance!~"([^"]*)"`)
    matches := re.FindStringSubmatch(original)
    
    existingVMs := make(map[string]bool)
    if len(matches) > 1 {
        for _, vm := range strings.Split(matches[1], "|") {
            existingVMs[vm] = true
        }
    }
    
    for _, vm := range excludeVMs {
        delete(existingVMs, vm)
    }
    
    var newExclusion string
    if len(existingVMs) > 0 {
        var vmList []string
        for vm := range existingVMs {
            vmList = append(vmList, vm)
        }
        newExclusion = fmt.Sprintf(`,instance!~"%s"`, strings.Join(vmList, "|"))
    }
    
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update status failed"})
		return
	}

	if err := reloadPrometheus(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prometheus reload failed"})
		return
	}
}

const (
	bwTemplatePath = "/etc/prometheus/rules.d/bw.yml"
)

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

func genspecialCPURuleContent(rules []common.CPURule, vmList []string, groupID string) (string, error) {
    return "", nil
}

func genspecialCPURuleContent1(rules []common.CPURule, vmList []string, groupUUID string) (string, error) {
    if len(rules) == 0 {
        return "", fmt.Errorf("empty rule list")
    }

    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("groups:\n- name: cpu_special_%s_%s\n  rules:\n", groupUUID, RuleTypeCPU))

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
            groupUUID,
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

    // Retrieve rule group with details using operator
    group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
    if errors.Is(err, gorm.ErrRecordNotFound) {
        c.JSON(http.StatusNotFound, gin.H{"error": "Rule group not found"})
        return
    } else if err != nil {
        log.Printf("Failed to retrieve rule group: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
        return
    }

    if group.Enabled {
        c.JSON(http.StatusOK, gin.H{"status": "already enabled"})
        return
    }
	type ExtendedGroup struct {
		model.RuleGroupV2
		Details []model.CPURuleDetail
	}
	details := (*ExtendedGroup)(unsafe.Pointer(group)).Details
    // Build rules from group details
    rules := make([]common.CPURule, 0, len(details))
    for _, d := range details {
        rules = append(rules, common.CPURule{
            Name:         d.Name,
            Duration:     d.Duration,
            Over:         d.Over,
            DownDuration: d.DownDuration,
            DownTo:       d.DownTo,
        })
    }

    // Get associated VMs
    vmLinks, err := a.operator.GetLinkedVMs(c.Request.Context(), groupUUID)
    if err != nil {
        log.Printf("Failed to get VM associations: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve VM links"})
        return
    }

    // Build exclusion list
    var vmList []string
    for _, link := range vmLinks {
        vmList = append(vmList, link.VMName)
    }

    // Generate rule paths
    generalPath, specialPath := rulePaths(RuleTypeCPU, groupUUID)

    // Create prometheus user reference
    pu, err := user.Lookup("prometheus")
    if err != nil {
        log.Printf("Prometheus user lookup failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "System configuration error"})
        return
    }
    uid, _ := strconv.Atoi(pu.Uid)
    gid, _ := strconv.Atoi(pu.Gid)

    // Generate and write rule files
    generalContent, err := generateCPURuleContent(rules, group.Name, groupUUID, vmList...)
    if err != nil {
        log.Printf("Rule content generation failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Rule generation failed"})
        return
    }

    // Write general rules
    if err := os.WriteFile(generalPath, []byte(generalContent), 0640); err != nil {
        log.Printf("File write failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create rule file"})
        return
    }
    os.Chown(generalPath, uid, gid)

    // Handle special rules for excluded VMs
    if len(vmList) > 0 {
        specialContent, err := genspecialCPURuleContent(rules, vmList, groupUUID)
        if err != nil {
            log.Printf("Special rule generation failed: %v", err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Special rules creation failed"})
            return
        }
        
        // Write special rules
        if err := os.WriteFile(specialPath, []byte(specialContent), 0640); err != nil {
            log.Printf("Special rule write failed: %v", err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create special rules"})
            return
        }
        os.Chown(specialPath, uid, gid)

        // Activate special rules
        enabledPath := filepath.Join(RulesEnabled, fmt.Sprintf("cpu-special-%s.yml", groupUUID))
        if err := os.Symlink(specialPath, enabledPath); err != nil && !os.IsExist(err) {
            log.Printf("Rule activation failed: %v", err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate rules"})
            return
        }
    }

    // Update rule group status
    if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), group.UUID, true); err != nil {
        log.Printf("Status update failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enable rule group"})
        return
    }

    // Reload Prometheus configuration
    if err := reloadPrometheus(); err != nil {
        log.Printf("Config reload failed: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Configuration reload failed"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "status": "success",
        "data": gin.H{
            "group_uuid": groupUUID,
            "enabled":    true,
        },
    })
}

func (a *AlarmAPI) DisableRules(c *gin.Context) {
	groupUUID := c.Param("id")

	// 使用operator获取规则组
	group, err := a.operator.GetCPURulesByGroupUUID(c.Request.Context(), groupUUID, RuleTypeCPU)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}

	if !group.Enabled {
		c.JSON(http.StatusOK, gin.H{"status": "already disabled"})
		return
	}

	enabledPath := RulesEnabled
	if _, err := os.Stat(enabledPath); err == nil {
		os.Remove(enabledPath)
	}

	if err := a.operator.UpdateRuleGroupStatus(c.Request.Context(), groupUUID, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update status failed"})
		return
	}

	reloadPrometheus()
}

func (a *AlarmAPI) GetCurrentAlarms(c *gin.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	targetURL := fmt.Sprintf("http://%s:%d/api/v1/alerts", alarmPrometheusIP, alarmPrometheusPort)
	resp, err := client.Get(targetURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
    bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}
    // Process active alerts
    if data, exists := result["data"]; exists {
        if alertData, ok := data.(map[string]interface{}); ok {
            if alerts, ok := alertData["alerts"].([]interface{}); ok {
                filtered := filterActiveAlerts(alerts)
                if filtered == nil {
                    filtered = make([]interface{}, 0)
                }
                for _, alert := range filtered {
                    if alertMap, ok := alert.(map[string]interface{}); ok {
                        labels := alertMap["labels"].(map[string]interface{})
                        if domain, ok := labels["domain"].(string); ok {
                            uuid, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), domain)
                            if err != nil {
                                log.Printf("Domain conversion failed domain=%s error=%v", domain, err)
                                labels["instance_uuid"] = "" // 确保空值
                            } else {
                                labels["instance_uuid"] = uuid
                            }
                        }
                    }
                }
                result["data"] = map[string]interface{}{"alerts": filtered}
            }
        } else {
            log.Printf("Unexpected data format: %T", data)
            result["data"] = map[string]interface{}{"alerts": []interface{}{}}
        }
    }

	c.JSON(http.StatusOK, gin.H{
        "status": "success",
        "data":   result["data"],
    })
}

func (a *AlarmAPI) GetHistoryAlarm(c *gin.Context) {
	startStr := c.Query("start")
    endStr := c.Query("end")
	stepStr := c.DefaultQuery("step", "300s")
    if startStr == "" || endStr == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "start and end parameters are required"})
        return
    }
    // Convert timestamps to integers
    start, err := strconv.ParseInt(startStr, 10, 64)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start timestamp"})
        return
    }
    end, err := strconv.ParseInt(endStr, 10, 64)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end timestamp"})
        return
    }

    client := &http.Client{Timeout: 15 * time.Second}
    baseURL := fmt.Sprintf("http://%s:%d/api/v1/query_range", alarmPrometheusIP, alarmPrometheusPort)

	req, err := http.NewRequest("GET", baseURL, nil)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
        return
    }

    // Set query parameters
    q := req.URL.Query()
    q.Add("query", "ALERTS")
    q.Add("start", strconv.FormatInt(start, 10))
    q.Add("end", strconv.FormatInt(end, 10))
    q.Add("step", stepStr)
    req.URL.RawQuery = q.Encode()

    // Execute request
    resp, err := client.Do(req)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query Prometheus"})
        return
    }
    defer resp.Body.Close()
    
    // Decode response structure
    var promResp struct {
        Data struct {
            Result []struct {
                Metric struct {
                    Alertname  string `json:"alertname"`
                    Domain     string `json:"domain"`
                    Instance   string `json:"instance"`
                    Alertstate string `json:"alertstate"`
                } `json:"metric"`
                Values [][]interface{} `json:"values"`
            } `json:"result"`
        } `json:"data"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse response"})
        return
    }
    fmt.Printf("[GetHistoryAlarm] Prometheus响应状态: %s (状态码: %d)\n", resp.Status, resp.StatusCode)
    // Process results
    processed := make([]gin.H, 0)
    for _, result := range promResp.Data.Result {
         fmt.Printf("[GetHistoryAlarm] result.Metric.Domain: %v", result.Metric.Domain)
		 instanceUUID, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), result.Metric.Domain)
         fmt.Printf("[GetHistoryAlarm] instanceUUID %v", instanceUUID)
		 if err != nil {
			 log.Printf("Domain to UUID convert failed : domain=%s error=%v", result.Metric.Domain, err)
			 instanceUUID = "" 
		 }
        events := make([]gin.H, 0)
        now := time.Now().Unix()
        
        for _, value := range result.Values {
            if len(value) < 2 {
                continue
            }
            
            // Extract timestamp (Prometheus returns seconds as float)
            timestamp, _ := strconv.ParseInt(fmt.Sprintf("%.0f", value[0]), 10, 64)
            duration := now - timestamp

            events = append(events, gin.H{
                "timestamp": timestamp,
                "duration":  duration,
            })
        }

        processed = append(processed, gin.H{
            "alert":    result.Metric.Alertname,
            "domain":   result.Metric.Domain,
            "instance": instanceUUID,
            "state":    result.Metric.Alertstate,
            "count":    len(events),
            "events":   events,
        })
    }
    fmt.Printf("[GetHistoryAlarm] processed %v", processed)

    c.JSON(http.StatusOK, gin.H{
        "status": "success",
        "data":   processed,
    })
}

func filterActiveAlerts(alerts []interface{}) []interface{} {
    filtered := make([]interface{}, 0)
    for _, a := range alerts {
        alert, ok := a.(map[string]interface{})
        if !ok {
            continue
        }

        if status, ok := alert["state"].(string); ok && status == "firing" {
            filtered = append(filtered, alert)
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


func (a *AlarmAPI) ProcessAlertWebhook(c *gin.Context) {
    var notification struct {
        Alerts []struct {
            Status      string            `json:"status"`
            Labels      map[string]string `json:"labels"`
            Annotations map[string]string `json:"annotations"`
            StartsAt    time.Time         `json:"startsAt"`
            EndsAt      time.Time         `json:"endsAt"`
        } `json:"alerts"`
    }
    
    body, _ := io.ReadAll(c.Request.Body)
    c.Request.Body = io.NopCloser(bytes.NewReader(body))

    if err := c.ShouldBindJSON(&notification); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid warnning msg format"})
        log.Printf("ProcessAlertWebhook invalid warnning msg format")
        return
    }

    for _, alert := range notification.Alerts {
        alertName := alert.Labels["alertname"]
        domain := alert.Labels["domain"]
        severity := alert.Labels["severity"]
        fmt.Printf("ProcessAlertWebhook Processing alert: name=%s domain=%s severity=%s", alertName, domain, severity)
        log.Printf("ProcessAlertWebhook Processing alert: name=%s domain=%s severity=%s", alertName, domain, severity)
        var instanceUUID string
        if domain != "" {
            uuid, err := routes.GetInstanceUUIDByDomain(c.Request.Context(), domain)
            if err != nil {
                log.Printf("convert domain to uuid domain=%s error=%v", domain, err)
                continue
            }
            instanceUUID = uuid
        }

        alertRecord := &common.Alert{
            Name:         alertName,
            Status:       alert.Status,
            InstanceUUID: instanceUUID,
            Severity:     severity,
            Summary:      alert.Annotations["summary"],
            Description:  alert.Annotations["description"],
            StartsAt:     alert.StartsAt,
            EndsAt:       alert.EndsAt,
        }

        if alert.Status == "firing" {
            go a.notifyRealtimeAlert(alertRecord) 
        }
    }

    c.JSON(http.StatusOK, gin.H{
        "status":  "processed",
        "alerts":  len(notification.Alerts),
        "message": "alarm process completed",
    })
}

func (a *AlarmAPI) notifyRealtimeAlert(alert *common.Alert) {
    // notify message to ui 
}