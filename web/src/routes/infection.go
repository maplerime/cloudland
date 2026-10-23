package routes

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/google/uuid"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
	macaron "gopkg.in/macaron.v1"

	"web/src/common"
	"web/src/model"
)

var inspectionDomain = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type VMInspectionOperator struct{}
type VMInspectionStartInput struct {
	InstanceUUID, Domain, RegionID, RuleGroupUUID, GlobalRuleID string
}

func resolveInspectionInstance(ctx context.Context, instanceUUID, domain string) (*model.Instance, error) {
	if instanceUUID == "" {
		var err error
		instanceUUID, err = GetInstanceUUIDByDomain(ctx, domain)
		if err != nil {
			return nil, err
		}
	}
	if _, err := uuid.Parse(instanceUUID); err != nil {
		return nil, fmt.Errorf("invalid VM UUID")
	}
	inst, err := GetInstanceByUUIDWithAuth(ctx, instanceUUID)
	if err != nil {
		return nil, err
	}
	expected := inst.Domain
	if expected == "" {
		expected = fmt.Sprintf("inst-%d", inst.ID)
	}
	if domain != "" && domain != expected {
		return nil, fmt.Errorf("VM domain mismatch")
	}
	inst.Domain = expected
	if !inspectionDomain.MatchString(expected) || inst.Hyper < 0 {
		return nil, fmt.Errorf("invalid VM target")
	}
	return inst, nil
}
func (*VMInspectionOperator) StartVMInspection(ctx context.Context, input VMInspectionStartInput) error {
	inst, err := resolveInspectionInstance(ctx, input.InstanceUUID, input.Domain)
	if err != nil {
		return err
	}
	op := &AdjustOperator{}
	var group *model.AdjustRuleGroup
	if input.RuleGroupUUID != "" {
		group, err = op.GetAdjustRulesByGroupUUID(ctx, input.RuleGroupUUID)
	} else if input.GlobalRuleID != "" {
		group, err = op.GetAdjustRulesByIdentifier(ctx, input.GlobalRuleID)
	} else {
		return fmt.Errorf("missing inspection rule group")
	}
	if err != nil {
		return err
	}
	if group.Type != model.RuleTypeAdjustCPU || group.RegionID != input.RegionID {
		return fmt.Errorf("inspection rule type/region mismatch")
	}
	settings, err := GetVMInspectionSettings(ctx, inst.Hyper)
	if err != nil {
		logger.Errorf("[VM-INSPECTION] VM=%s settings unavailable, automatic isolation disabled: %v", inst.UUID, err)
	}
	return dispatchVMInspection(ctx, inst, settings.IsolationEnabled, settings.MatchPolicy)
}

func dispatchVMInspection(ctx context.Context, inst *model.Instance, enabled bool, policy VMInspectionMatchPolicy) error {
	if policy == nil {
		policy = defaultVMInspectionMatchPolicy()
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/inspect_vm.sh '%s' '%t' '%s'", inst.Domain, enabled, string(data))
	if err := common.HyperExecute(ctx, fmt.Sprintf("inter=%d", inst.Hyper), command); err != nil {
		return err
	}
	logger.Infof("[VM-INSPECTION] VM=%s domain=%s hyper=%d isolation_enabled=%t command sent", inst.UUID, inst.Domain, inst.Hyper, enabled)
	return nil
}

type VMInspectionMatchAction struct {
	Action string `json:"action"`
}
type VMInspectionMatchPolicy map[string]VMInspectionMatchAction

func defaultVMInspectionMatchPolicy() VMInspectionMatchPolicy {
	return VMInspectionMatchPolicy{"strong": {Action: "pin"}, "weak": {Action: "pin"}}
}
func validVMInspectionMatchPolicy(policy VMInspectionMatchPolicy) bool {
	return len(policy) == 2 && (policy["strong"].Action == "pin" || policy["strong"].Action == "none") &&
		(policy["weak"].Action == "pin" || policy["weak"].Action == "none")
}

type VMInspectionGlobalSettings struct {
	IsolationEnabled              bool                    `json:"isolation_enabled"`
	RecordRetentionDays           int                     `json:"record_retention_days"`
	FeatureLibraryURL             string                  `json:"feature_library_url"`
	FeatureLibraryTokenConfigured bool                    `json:"feature_library_token_configured"`
	MatchPolicy                   VMInspectionMatchPolicy `json:"match_policy"`
}
type VMInspectionNodeSetting struct {
	HyperID          int32 `json:"hyper_id"`
	IsolationEnabled bool  `json:"isolation_enabled"`
}
type VMInspectionSettingsResponse struct {
	Global    VMInspectionGlobalSettings `json:"global"`
	Nodes     []VMInspectionNodeSetting  `json:"nodes"`
	HiveNodes []VMInspectionHiveNode     `json:"hive_nodes"`
}
type VMInspectionGlobalPatch struct {
	IsolationEnabled    *bool                    `json:"isolation_enabled"`
	RecordRetentionDays *int                     `json:"record_retention_days"`
	FeatureLibraryURL   *string                  `json:"feature_library_url"`
	FeatureLibraryToken *string                  `json:"feature_library_token"`
	MatchPolicy         *VMInspectionMatchPolicy `json:"match_policy"`
}
type VMInspectionNodePatch struct {
	HyperIDs         []int32 `json:"hyper_ids"`
	IsolationEnabled *bool   `json:"isolation_enabled"`
}
type VMInspectionSettingsPatch struct {
	Global        *VMInspectionGlobalPatch `json:"global"`
	Nodes         []VMInspectionNodePatch  `json:"nodes"`
	ResetHyperIDs []int32                  `json:"reset_hyper_ids"`
}

func requireVMInspectionAdmin(ctx context.Context) error {
	member := common.GetMemberShip(ctx)
	if member.OrgName != "admin" || !member.CheckPermission(model.Admin) {
		return common.NewCLError(common.ErrPermissionDenied, "administrator permission required", nil)
	}
	return nil
}
func inspectionSettingsFromRows(rows []model.VMInspectionSetting) VMInspectionSettingsResponse {
	out := VMInspectionSettingsResponse{Global: VMInspectionGlobalSettings{RecordRetentionDays: 7, MatchPolicy: defaultVMInspectionMatchPolicy()}, Nodes: []VMInspectionNodeSetting{}}
	for _, row := range rows {
		if row.HyperID == -1 {
			out.Global.IsolationEnabled = row.IsolationEnabled
			out.Global.FeatureLibraryURL = row.FeatureLibraryURL
			out.Global.FeatureLibraryTokenConfigured = row.FeatureLibraryToken != ""
			if row.MatchPolicy != "" {
				_ = json.Unmarshal([]byte(row.MatchPolicy), &out.Global.MatchPolicy)
			}
			if row.RecordRetentionDays != nil && *row.RecordRetentionDays > 0 {
				out.Global.RecordRetentionDays = *row.RecordRetentionDays
			}
		} else {
			out.Nodes = append(out.Nodes, VMInspectionNodeSetting{row.HyperID, row.IsolationEnabled})
		}
	}
	return out
}
func ListVMInspectionSettings(ctx context.Context) (VMInspectionSettingsResponse, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	_, db := common.GetContextDB(ctx)
	var rows []model.VMInspectionSetting
	if err := db.Order("hyper_id").Find(&rows).Error; err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	out := inspectionSettingsFromRows(rows)
	healthByNode := map[int32]HiveNodeStatus{}
	for _, row := range rows {
		if row.HyperID == -1 && row.HiveStatus != "" {
			if err := json.Unmarshal([]byte(row.HiveStatus), &healthByNode); err != nil {
				return out, err
			}
		}
	}
	var hypers []model.Hyper
	if err := db.Select("hostid, hostname").Where("hostid >= 0").Order("hostid").Find(&hypers).Error; err != nil {
		return out, err
	}
	out.HiveNodes = []VMInspectionHiveNode{}
	for _, h := range hypers {
		out.HiveNodes = append(out.HiveNodes, VMInspectionHiveNode{h.Hostid, h.Hostname, healthByNode[h.Hostid]})
	}
	return out, nil
}

// Internal reader: a missing row is a default; a database failure is an error.
func GetVMInspectionSettings(ctx context.Context, hyperID int32) (VMInspectionGlobalSettings, error) {
	if hyperID < -1 {
		return VMInspectionGlobalSettings{}, fmt.Errorf("invalid compute node")
	}
	_, db := common.GetContextDB(ctx)
	var rows []model.VMInspectionSetting
	if err := db.Where("hyper_id IN (?)", []int32{-1, hyperID}).Order("hyper_id").Find(&rows).Error; err != nil {
		return VMInspectionGlobalSettings{}, err
	}
	out := inspectionSettingsFromRows(rows).Global
	for _, row := range rows {
		if hyperID >= 0 && row.HyperID == hyperID {
			out.IsolationEnabled = row.IsolationEnabled
		}
	}
	return out, nil
}
func UpdateVMInspectionSettings(ctx context.Context, p VMInspectionSettingsPatch) (VMInspectionSettingsResponse, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	invalid := func(message string) error { return common.NewCLError(common.ErrInvalidParameter, message, nil) }
	ids := map[int32]bool{}
	for _, group := range p.Nodes {
		if len(group.HyperIDs) == 0 || group.IsolationEnabled == nil {
			return VMInspectionSettingsResponse{}, invalid("each nodes entry needs hyper_ids and isolation_enabled")
		}
		for _, id := range group.HyperIDs {
			if id < 0 || ids[id] {
				return VMInspectionSettingsResponse{}, invalid("invalid or duplicate compute node")
			}
			ids[id] = true
		}
	}
	for _, id := range p.ResetHyperIDs {
		if id < 0 || ids[id] {
			return VMInspectionSettingsResponse{}, invalid("duplicate, conflicting or invalid reset node")
		}
		ids[id] = true
	}
	if p.Global != nil && p.Global.RecordRetentionDays != nil && *p.Global.RecordRetentionDays <= 0 {
		return VMInspectionSettingsResponse{}, invalid("record_retention_days must be positive")
	}
	if p.Global != nil && p.Global.FeatureLibraryURL != nil {
		value := strings.TrimSpace(*p.Global.FeatureLibraryURL)
		if value != "" {
			u, err := url.ParseRequestURI(value)
			if err != nil || len(value) > 512 || u.Hostname() == "" || (u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "") {
				return VMInspectionSettingsResponse{}, invalid("feature_library_url must be an absolute HTTPS URL of at most 512 bytes, or empty")
			}
		}
		p.Global.FeatureLibraryURL = &value
	}
	hiveChanged := p.Global != nil && (p.Global.FeatureLibraryURL != nil || p.Global.FeatureLibraryToken != nil)
	if p.Global != nil {
		if p.Global.FeatureLibraryToken != nil && (len(*p.Global.FeatureLibraryToken) > 4096 || strings.ContainsAny(*p.Global.FeatureLibraryToken, "\x00 \t\r\n\v\f")) {
			return VMInspectionSettingsResponse{}, invalid("invalid Hive read token")
		}

		if p.Global.MatchPolicy != nil && !validVMInspectionMatchPolicy(*p.Global.MatchPolicy) {
			return VMInspectionSettingsResponse{}, invalid("match_policy needs strong/weak actions pin or none")
		}
	}
	globalChanged := p.Global != nil && (p.Global.IsolationEnabled != nil || p.Global.RecordRetentionDays != nil || p.Global.MatchPolicy != nil || hiveChanged)
	if !globalChanged && len(ids) == 0 {
		return VMInspectionSettingsResponse{}, invalid("no settings supplied")
	}
	_, db := common.GetContextDB(ctx)
	tx := db.New().LogMode(false).Begin()
	if tx.Error != nil {
		return VMInspectionSettingsResponse{}, tx.Error
	}
	defer tx.Rollback()
	list := make([]int32, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	if len(list) > 0 {
		var hypers []model.Hyper
		if err := tx.Select("hostid").Where("hostid IN (?)", list).Find(&hypers).Error; err != nil {
			return VMInspectionSettingsResponse{}, err
		}
		found := map[int32]bool{}
		for _, h := range hypers {
			found[h.Hostid] = true
		}
		missing := []int32{}
		for _, id := range list {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return VMInspectionSettingsResponse{}, invalid(fmt.Sprintf("unknown compute nodes: %v", missing))
		}
	}
	member := common.GetMemberShip(ctx)
	save := func(id int32, enabled *bool, days *int, libraryURL *string) error {
		row := model.VMInspectionSetting{}
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where("hyper_id = ?", id).Take(&row).Error
		if gorm.IsRecordNotFoundError(err) {
			row.HyperID = id
			row.Creater = member.UserID
			if enabled != nil {
				row.IsolationEnabled = *enabled
			}
			if id == -1 {
				n := 7
				if days != nil {
					n = *days
				}
				row.RecordRetentionDays = &n
				if libraryURL != nil {
					row.FeatureLibraryURL = *libraryURL
				}
			}
			if id == -1 {
				if p.Global.FeatureLibraryToken != nil {
					row.FeatureLibraryToken = *p.Global.FeatureLibraryToken
				}
				if p.Global.MatchPolicy != nil {
					data, _ := json.Marshal(*p.Global.MatchPolicy)
					row.MatchPolicy = string(data)
				}
			}
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]interface{}{}
		if enabled != nil {
			updates["isolation_enabled"] = *enabled
		}
		if days != nil && id == -1 {
			updates["record_retention_days"] = *days
		}
		if libraryURL != nil && id == -1 {
			updates["feature_library_url"] = *libraryURL
		}
		if id == -1 {
			if p.Global.FeatureLibraryToken != nil {
				updates["feature_library_token"] = *p.Global.FeatureLibraryToken
			}
			if p.Global.MatchPolicy != nil {
				data, _ := json.Marshal(*p.Global.MatchPolicy)
				updates["match_policy"] = string(data)
			}
		}
		if id >= 0 {
			updates["record_retention_days"] = nil
		}
		return tx.Model(&row).Updates(updates).Error
	}
	if globalChanged {
		if err := save(-1, p.Global.IsolationEnabled, p.Global.RecordRetentionDays, p.Global.FeatureLibraryURL); err != nil {
			return VMInspectionSettingsResponse{}, err
		}
	}
	for _, group := range p.Nodes {
		for _, id := range group.HyperIDs {
			if err := save(id, group.IsolationEnabled, nil, nil); err != nil {
				return VMInspectionSettingsResponse{}, err
			}
		}
	}
	if len(p.ResetHyperIDs) > 0 {
		if err := tx.Unscoped().Where("hyper_id IN (?)", p.ResetHyperIDs).Delete(&model.VMInspectionSetting{}).Error; err != nil {
			return VMInspectionSettingsResponse{}, err
		}
	}
	var rows []model.VMInspectionSetting
	if err := tx.Order("hyper_id").Find(&rows).Error; err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	out := inspectionSettingsFromRows(rows)
	if err := tx.Commit().Error; err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	if hiveChanged {
		go syncHiveNodes(common.SetContextDB(context.Background(), db))
	}
	logger.Infof("[VM-INSPECTION] settings updated operator=%d nodes=%d global=%t", member.UserID, len(ids), globalChanged)
	return out, nil
}

// This DTO carries node observations and optional judgment/disposition results.
type VMInfectionSummary struct {
	SchemaVersion    int             `json:"schema_version"`
	InstanceUUID     string          `json:"instance_uuid"`
	Domain           string          `json:"domain"`
	SourceHyper      int32           `json:"source_hyper"`
	InspectionResult json.RawMessage `json:"inspection_result"`
	CollectedAt      *time.Time      `json:"collected_at"`
	RulesetVersion   string          `json:"ruleset_version"`
	MatchSummary     string          `json:"match_summary"`
	Verdict          string          `json:"verdict"`
	MatchRuleIDs     []string        `json:"match_rule_ids"`
	IsolationStatus  string          `json:"isolation_status"`
	IsolationCPUs    string          `json:"isolation_cpus"`
	IsolationSource  string          `json:"isolation_source"`
	OperationAt      time.Time       `json:"operation_at"`
	Action           string          `json:"action"`
	ActionSource     string          `json:"action_source"`
	ScanID           string          `json:"scan_id"`
	Remark           string          `json:"remark"`
	ErrorMessage     string          `json:"error_message"`
}

func validateVMInfectionResult(ctx context.Context, source int32, e *VMInfectionSummary) error {
	if e.SchemaVersion != 1 || source < 0 || e.SourceHyper != source {
		return fmt.Errorf("invalid infection result source or schema")
	}
	if id, err := uuid.Parse(e.InstanceUUID); err != nil || id.String() != e.InstanceUUID {
		return fmt.Errorf("invalid instance UUID")
	}
	if !inspectionDomain.MatchString(e.Domain) {
		return fmt.Errorf("invalid VM domain")
	}
	if len(e.ScanID) > 128 || (e.ScanID != "" && !strings.HasPrefix(e.ScanID, e.InstanceUUID+"_")) {
		return fmt.Errorf("invalid scan reference")
	}
	if e.Verdict != "" && e.Verdict != "confirmed" && e.Verdict != "suspected" && e.Verdict != "not_evaluated" && e.Verdict != "no_match" {
		return fmt.Errorf("invalid verdict")
	}
	if e.Action != "record" && e.Action != "quarantine" && e.Action != "release" {
		return fmt.Errorf("invalid action")
	}
	if e.ActionSource != "automatic" && e.ActionSource != "manual" {
		return fmt.Errorf("invalid action source")
	}
	if e.IsolationSource != "" && e.IsolationSource != "automatic" && e.IsolationSource != "manual" && e.IsolationSource != "existing" {
		return fmt.Errorf("invalid isolation source")
	}
	if e.IsolationSource == "existing" && (e.Action != "record" || e.IsolationStatus != "isolated") {
		return fmt.Errorf("invalid existing isolation observation")
	}
	if e.ActionSource == "automatic" && e.Action != "record" && !validAutomaticHiveAction(e) {
		return fmt.Errorf("unconfirmed automatic result")
	}
	switch e.IsolationStatus {
	case "":
		if e.Action != "record" || e.IsolationSource != "" || e.IsolationCPUs != "" {
			return fmt.Errorf("empty isolation state is only valid for an observation")
		}
	case "disabled", "not_requested", "isolated", "failed", "released", "release_failed":
	default:
		return fmt.Errorf("invalid isolation status")
	}
	if e.IsolationStatus != "" && e.IsolationSource == "" && e.Action != "record" {
		return fmt.Errorf("disposition has no action source")
	}
	if len([]rune(e.RulesetVersion)) > 128 || len([]rune(e.MatchSummary)) > 256 {
		return fmt.Errorf("feature judgment exceeds field bounds")
	}
	hasInspection := len(e.InspectionResult) > 0 && string(e.InspectionResult) != "null"
	if e.Action == "record" && !hasInspection {
		return fmt.Errorf("observation result is missing")
	}
	if hasInspection {
		var data struct {
			SchemaVersion    int       `json:"schema_version"`
			CollectionStatus string    `json:"collection_status"`
			FinishedAt       time.Time `json:"finished_at"`
		}
		if len(e.InspectionResult) > 262144 || json.Unmarshal(e.InspectionResult, &data) != nil || data.SchemaVersion != 1 {
			return fmt.Errorf("invalid inspection observations")
		}
		switch data.CollectionStatus {
		case "complete", "partial", "failed":
		default:
			return fmt.Errorf("invalid collection status")
		}
		if e.CollectedAt == nil || !e.CollectedAt.Equal(data.FinishedAt) {
			return fmt.Errorf("invalid collection timestamp")
		}
	}
	if e.IsolationStatus == "isolated" && e.IsolationCPUs == "" {
		return fmt.Errorf("isolated result has no CPUs")
	}
	if len(e.Remark) > 1024 || len([]rune(e.Remark)) > 255 || len(e.ErrorMessage) > 2048 || len([]rune(e.ErrorMessage)) > 512 || len(e.IsolationCPUs) > 255 || len(e.MatchRuleIDs) > 4096 {
		return fmt.Errorf("infection summary exceeds field bounds")
	}
	for _, id := range e.MatchRuleIDs {
		if len(id) == 0 || len(id) > 128 {
			return fmt.Errorf("invalid feature identifier")
		}
	}
	encoded, _ := json.Marshal(e.MatchRuleIDs)
	if len(encoded) > 65536 {
		return fmt.Errorf("feature identifiers exceed limit")
	}
	settings, err := GetVMInspectionSettings(ctx, -1)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	latest := e.OperationAt
	if e.CollectedAt != nil {
		if e.CollectedAt.IsZero() || e.CollectedAt.After(now.Add(5*time.Minute)) {
			return fmt.Errorf("invalid collection time")
		}
		if e.CollectedAt.After(latest) {
			latest = *e.CollectedAt
		}
	}
	if e.OperationAt.IsZero() || e.OperationAt.After(now.Add(5*time.Minute)) || latest.Before(now.AddDate(0, 0, -settings.RecordRetentionDays)) {
		return fmt.Errorf("expired or invalid result time")
	}
	adminCtx, err := CreateAdminContext(ctx)
	if err != nil {
		return err
	}
	inst, err := resolveInspectionInstance(adminCtx, e.InstanceUUID, e.Domain)
	if err != nil {
		return err
	}
	if inst.Hyper != source {
		return fmt.Errorf("VM moved or result source mismatched")
	}
	return nil
}
func SaveVMInfectionResult(ctx context.Context, source int32, e *VMInfectionSummary) error {
	if err := validateVMInfectionResult(ctx, source, e); err != nil {
		return err
	}
	logger.Infof("[VM-INFECTION] result received: VM=%s source=%d scan=%q action=%s action_source=%s verdict=%s status=%s cpus=%q operation_at=%s error=%q",
		e.InstanceUUID, source, e.ScanID, e.Action, e.ActionSource, e.Verdict, e.IsolationStatus, e.IsolationCPUs, e.OperationAt.UTC().Format(time.RFC3339Nano), e.ErrorMessage)
	return saveVMInfectionResult(ctx, source, e)
}

func saveVMInfectionResult(ctx context.Context, source int32, e *VMInfectionSummary) error {
	_, db := common.GetContextDB(ctx)
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()
	// Serialize updates for one VM using its existing row; no task table or node-side record ID.
	var inst model.Instance
	if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("uuid = ?", e.InstanceUUID).Take(&inst).Error; err != nil {
		return err
	}
	if inst.Hyper != source {
		return fmt.Errorf("VM moved or result source mismatched")
	}
	old := model.VMInfectionRecord{}
	err := tx.Set("gorm:query_option", "FOR UPDATE").Where("instance_uuid = ?", e.InstanceUUID).Order("id DESC").Take(&old).Error
	fresh := gorm.IsRecordNotFoundError(err)
	if err != nil && !fresh {
		return err
	}
	if !fresh {
		hasInspection := len(e.InspectionResult) > 0 && string(e.InspectionResult) != "null"
		newInspection := hasInspection && e.CollectedAt != nil && (old.CollectedAt == nil || e.CollectedAt.After(*old.CollectedAt) || (e.CollectedAt.Equal(*old.CollectedAt) && e.ScanID > old.ScanID))
		if e.Action == "record" && !newInspection {
			return nil
		}
		if e.Action != "record" && !e.OperationAt.After(old.OperationAt) && !newInspection {
			return nil
		}
		if !newInspection {
			e.InspectionResult = json.RawMessage(old.InspectionResult)
			e.CollectedAt = old.CollectedAt
			e.ScanID = old.ScanID
		}
		// Manual disposition results do not carry the earlier scan or feature judgment.
		if !newInspection {
			e.Verdict, e.RulesetVersion, e.MatchSummary = old.Verdict, old.RulesetVersion, old.MatchSummary
			if err := json.Unmarshal([]byte(old.MatchRuleIDs), &e.MatchRuleIDs); err != nil {
				e.MatchRuleIDs = nil
			}
		}
		if old.HyperID == source && ((e.Action == "record" && e.IsolationStatus == "") || (e.IsolationStatus == "disabled" && old.IsolationStatus != "") || e.OperationAt.Before(old.OperationAt)) {
			e.IsolationStatus, e.IsolationCPUs, e.IsolationSource = old.IsolationStatus, old.IsolationCPUs, old.IsolationSource
			e.OperationAt = old.OperationAt
			if e.ErrorMessage == "" || e.Action != "record" {
				e.ErrorMessage = old.ErrorMessage
			}
		}
		if e.Remark == "" {
			e.Remark = old.Remark
		}
	}

	if e.Action == "record" && e.IsolationSource == "existing" {
		// Observation is not a new pin operation; preserve its known original time.
		e.OperationAt = time.Time{}
		if !fresh && old.HyperID == source && old.IsolationStatus == "isolated" && old.IsolationCPUs == e.IsolationCPUs {
			e.OperationAt = old.OperationAt
		}
	}

	ids, _ := json.Marshal(e.MatchRuleIDs)
	updates := map[string]interface{}{"instance_uuid": e.InstanceUUID, "hyper_id": source, "verdict": e.Verdict, "match_rule_ids": string(ids), "isolation_status": e.IsolationStatus, "isolation_cpus": e.IsolationCPUs, "isolation_source": e.IsolationSource, "operation_at": e.OperationAt, "scan_id": e.ScanID, "remark": e.Remark, "error_message": e.ErrorMessage}
	updates["inspection_result"] = string(e.InspectionResult)
	updates["collected_at"] = e.CollectedAt
	updates["ruleset_version"] = e.RulesetVersion
	updates["match_summary"] = e.MatchSummary
	if fresh {
		row := model.VMInfectionRecord{Model: model.Model{Creater: 1}, InstanceUUID: e.InstanceUUID, HyperID: source, Verdict: e.Verdict, MatchRuleIDs: string(ids), IsolationStatus: e.IsolationStatus, IsolationCPUs: e.IsolationCPUs, IsolationSource: e.IsolationSource, OperationAt: e.OperationAt, ScanID: e.ScanID, Remark: e.Remark, ErrorMessage: e.ErrorMessage}
		row.InspectionResult = string(e.InspectionResult)
		row.CollectedAt = e.CollectedAt
		row.RulesetVersion = e.RulesetVersion
		row.MatchSummary = e.MatchSummary
		err = tx.Create(&row).Error
		old.UUID = row.UUID
	} else {
		err = tx.Model(&old).Updates(updates).Error
	}
	if err != nil {
		return err
	}
	if err = tx.Commit().Error; err != nil {
		return err
	}
	logger.Infof("[VM-INFECTION] record=%s VM=%s source=%d scan=%q collected_at=%v verdict=%q status=%q saved", old.UUID, e.InstanceUUID, source, e.ScanID, e.CollectedAt, e.Verdict, e.IsolationStatus)
	return nil
}

type VMInfectionListInput struct {
	InstanceUUID    string
	IsolationStatus string
	Verdict         string
	Start           time.Time
	End             time.Time
	Offset          int
	Limit           int
}

func ListVMInfections(ctx context.Context, p VMInfectionListInput) ([]model.VMInfectionRecord, int, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return nil, 0, err
	}
	_, db := common.GetContextDB(ctx)
	q := db.Model(&model.VMInfectionRecord{})
	if p.InstanceUUID != "" {
		q = q.Where("instance_uuid = ?", p.InstanceUUID)
	}
	if p.IsolationStatus != "" {
		if p.IsolationStatus == "not_requested" {
			q = q.Where("isolation_status IN (?)", []string{"", "not_requested"})
		} else {
			q = q.Where("isolation_status = ?", p.IsolationStatus)
		}
	}
	if p.Verdict != "" {
		if p.Verdict == "not_evaluated" {
			q = q.Where("verdict IN (?)", []string{"", "not_evaluated"})
		} else {
			q = q.Where("verdict = ?", p.Verdict)
		}
	}
	if !p.Start.IsZero() {
		q = q.Where("operation_at >= ?", p.Start)
	}
	if !p.End.IsZero() {
		q = q.Where("operation_at <= ?", p.End)
	}
	var total int
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.VMInfectionRecord{}
	err := q.Order("CASE WHEN collected_at > operation_at THEN collected_at ELSE operation_at END DESC").Offset(p.Offset).Limit(p.Limit).Find(&rows).Error
	if err == nil {
		err = fillVMInfectionHostnames(ctx, rows)
	}
	return rows, total, err
}

func fillVMInfectionHostnames(ctx context.Context, rows []model.VMInfectionRecord) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int32, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.HyperID)
	}
	_, db := common.GetContextDB(ctx)
	var hypers []model.Hyper
	if err := db.Select("hostid, hostname").Where("hostid IN (?)", ids).Find(&hypers).Error; err != nil {
		return err
	}
	names := make(map[int32]string, len(hypers))
	for _, hyper := range hypers {
		names[hyper.Hostid] = hyper.Hostname
	}
	for i := range rows {
		rows[i].HyperHostname = names[rows[i].HyperID]
	}
	return nil
}
func GetVMInfection(ctx context.Context, id string) (*model.VMInfectionRecord, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, common.NewCLError(common.ErrInvalidParameter, "invalid record UUID", err)
	}
	_, db := common.GetContextDB(ctx)
	row := &model.VMInfectionRecord{}
	if err := db.Where("uuid = ?", id).Take(row).Error; err != nil {
		return nil, err
	}
	rows := []model.VMInfectionRecord{*row}
	if err := fillVMInfectionHostnames(ctx, rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}
func DeleteVMInfection(ctx context.Context, id string) error {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return common.NewCLError(common.ErrInvalidParameter, "invalid record UUID", err)
	}
	_, db := common.GetContextDB(ctx)
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()
	row := model.VMInfectionRecord{}
	if err := tx.Unscoped().Set("gorm:query_option", "FOR UPDATE").Where("uuid = ?", id).Take(&row).Error; err != nil {
		return err
	}
	if infectionNeedsRestore(row.IsolationStatus) {
		return fmt.Errorf("conflict: release quarantine before deleting this record")
	}
	if err := tx.Unscoped().Delete(&row).Error; err != nil {
		return err
	}
	return tx.Commit().Error
}

type VMInfectionChunkInput struct {
	InstanceUUID string
	Seq          int
	Total        int
	SHA256       string
	Payload      string
}

type vmInfectionKey struct {
	vm     string
	hash   string
	source int32
}
type vmInfectionBuffer struct {
	key        vmInfectionKey
	total      int
	hash       string
	chunks     []string
	received   int
	bytes      int
	processing bool
	receivedAt time.Time
	timer      *time.Timer
}
type vmInfectionRuntime struct {
	mu                             sync.Mutex
	active                         map[vmInfectionKey]*vmInfectionBuffer
	completed                      map[vmInfectionKey]time.Time
	maxBytes, maxChunks, maxActive int
	timeout, ttl                   time.Duration
	process                        func(int32, *VMInfectionSummary) error
	cleanupTimer                   *time.Timer
}

func newVMInfectionRuntime(maxBytes, maxChunks, maxActive int, timeout, ttl time.Duration, process func(int32, *VMInfectionSummary) error) *vmInfectionRuntime {
	return &vmInfectionRuntime{active: make(map[vmInfectionKey]*vmInfectionBuffer), completed: make(map[vmInfectionKey]time.Time), maxBytes: maxBytes, maxChunks: maxChunks, maxActive: maxActive, timeout: timeout, ttl: ttl, process: process}
}

var infectionRuntimeOnce sync.Once
var infectionRuntime *vmInfectionRuntime

func ReceiveVMInfectionChunk(_ context.Context, source int32, chunk VMInfectionChunkInput) error {
	infectionRuntimeOnce.Do(func() {
		infectionRuntime = newVMInfectionRuntime(262144, 512, 128, 60*time.Second, 300*time.Second, func(source int32, e *VMInfectionSummary) error {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			return SaveVMInfectionResult(ctx, source, e)
		})
	})
	return infectionRuntime.receive(source, chunk)
}
func (r *vmInfectionRuntime) receive(source int32, c VMInfectionChunkInput) error {
	if _, err := uuid.Parse(c.InstanceUUID); err != nil || len(c.InstanceUUID) != 36 {
		return fmt.Errorf("invalid infection instance UUID")
	}
	if source < 0 || c.Total < 1 || c.Total > r.maxChunks || c.Seq < 1 || c.Seq > c.Total || len(c.Payload) < 1 || len(c.Payload) > 768 {
		return fmt.Errorf("invalid inspection chunk bounds")
	}
	if c.Total > (base64.StdEncoding.EncodedLen(r.maxBytes)+767)/768 {
		return fmt.Errorf("inspection result too large")
	}
	hash, err := hex.DecodeString(c.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return fmt.Errorf("invalid inspection hash")
	}
	for _, v := range []byte(c.Payload) {
		if !(v >= 'A' && v <= 'Z' || v >= 'a' && v <= 'z' || v >= '0' && v <= '9' || v == '+' || v == '/' || v == '=') {
			return fmt.Errorf("invalid Base64 chunk")
		}
	}
	key := vmInfectionKey{vm: c.InstanceUUID, hash: strings.ToLower(c.SHA256), source: source}
	r.mu.Lock()
	defer r.mu.Unlock()
	if expiry, ok := r.completed[key]; ok {
		if time.Now().Before(expiry) {
			return nil
		}
		delete(r.completed, key)
	}
	b := r.active[key]
	if b == nil {
		if len(r.active) >= r.maxActive {
			return fmt.Errorf("inspection result capacity reached")
		}
		b = &vmInfectionBuffer{key: key, total: c.Total, hash: strings.ToLower(c.SHA256), chunks: make([]string, c.Total)}
		r.active[key] = b
		b.timer = time.AfterFunc(r.timeout, func() { r.expireVMInfectionBuffer(key) })
	}
	if b.processing {
		return nil
	}
	if b.total != c.Total || b.hash != strings.ToLower(c.SHA256) || b.chunks[c.Seq-1] != "" && b.chunks[c.Seq-1] != c.Payload {
		r.releaseLocked(key)
		return fmt.Errorf("inspection chunk conflict")
	}
	if b.chunks[c.Seq-1] != "" {
		return nil
	}
	b.bytes += len(c.Payload)
	if b.bytes > base64.StdEncoding.EncodedLen(r.maxBytes) {
		r.releaseLocked(key)
		return fmt.Errorf("inspection encoded result too large")
	}
	b.chunks[c.Seq-1] = c.Payload
	b.received++
	if b.received == b.total {
		b.receivedAt = time.Now().UTC()
		b.processing = true
		b.timer.Stop()
		go r.processVMInfectionResult(b)
	}
	return nil
}
func assembleVMInfection(b *vmInfectionBuffer, maxBytes int) (*VMInfectionSummary, error) {
	encoded := strings.Join(b.chunks, "")
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("inspection JSON exceeds limit")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != b.hash {
		return nil, fmt.Errorf("inspection checksum mismatch")
	}
	var e VMInfectionSummary
	if err = json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.SchemaVersion != 1 || e.InstanceUUID != b.key.vm {
		return nil, fmt.Errorf("inspection schema/VM mismatch")
	}
	if e.SourceHyper != b.key.source {
		return nil, fmt.Errorf("inspection source mismatch")
	}
	return &e, nil
}
func (r *vmInfectionRuntime) processVMInfectionResult(b *vmInfectionBuffer) {
	defer func() { r.mu.Lock(); defer r.mu.Unlock(); r.releaseLocked(b.key) }()
	e, err := assembleVMInfection(b, r.maxBytes)
	if err == nil {
		err = r.process(b.key.source, e)
	}
	if err != nil {
		logger.Errorf("[VM-INFECTION] VM=%s source=%d save failed: %v", b.key.vm, b.key.source, err)
		return
	}
	r.mu.Lock()
	r.rememberLocked(b.key)
	r.mu.Unlock()
}
func (r *vmInfectionRuntime) expireVMInfectionBuffer(key vmInfectionKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.active[key]
	if b == nil || b.processing {
		return
	}
	missing := []int{}
	for i, p := range b.chunks {
		if p == "" {
			missing = append(missing, i+1)
		}
	}
	logger.Warningf("[VM-INSPECTION] VM=%s source=%d result_incomplete missing=%v", key.vm, key.source, missing)
	r.releaseLocked(key)
}
func (r *vmInfectionRuntime) releaseLocked(key vmInfectionKey) {
	if b := r.active[key]; b != nil {
		b.timer.Stop()
		delete(r.active, key)
	}
}
func (r *vmInfectionRuntime) rememberLocked(key vmInfectionKey) {
	// Tombstones are bounded as well as expiring; only small keys survive processing.
	if len(r.completed) >= r.maxActive {
		var oldest vmInfectionKey
		var at time.Time
		for k, t := range r.completed {
			if at.IsZero() || t.Before(at) {
				oldest = k
				at = t
			}
		}
		delete(r.completed, oldest)
	}
	expiry := time.Now().Add(r.ttl)
	r.completed[key] = expiry
	if r.cleanupTimer == nil {
		r.cleanupTimer = time.AfterFunc(r.ttl, r.cleanupCompleted)
	}
}

func (r *vmInfectionRuntime) cleanupCompleted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, expiry := range r.completed {
		if !time.Now().Before(expiry) {
			delete(r.completed, key)
		}
	}
	r.cleanupTimer = nil
	if len(r.completed) > 0 {
		r.cleanupTimer = time.AfterFunc(r.ttl, r.cleanupCompleted)
	}
}

func RegisterVMInfectionCleanup(s *gocron.Scheduler) {
	if _, err := s.Every(1).Hour().Do(CleanupExpiredVMInfections); err != nil {
		logger.Errorf("[VM-INFECTION] register cleanup: %v", err)
	}
}
func CleanupExpiredVMInfections() {
	cleanupExpiredVMInfections(context.Background(), time.Now().UTC())
}

func cleanupExpiredVMInfections(ctx context.Context, now time.Time) {
	settings, err := GetVMInspectionSettings(ctx, -1)
	if err != nil {
		logger.Errorf("[VM-INFECTION] cleanup settings: %v", err)
		return
	}
	cutoff := now.UTC().AddDate(0, 0, -settings.RecordRetentionDays)
	_, db := common.GetContextDB(ctx)
	// Retention follows the latest observation or operation time.
	safe := []string{"", "disabled", "not_requested", "released"}
	for {
		var rows []model.VMInfectionRecord
		err = db.Unscoped().Select("id").Where("operation_at < ? AND (collected_at IS NULL OR collected_at < ?) AND isolation_status IN (?)", cutoff, cutoff, safe).Order("operation_at").Limit(500).Find(&rows).Error
		if err != nil {
			logger.Errorf("[VM-INFECTION] cleanup query: %v", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		result := db.Unscoped().Where("id IN (?) AND operation_at < ? AND (collected_at IS NULL OR collected_at < ?) AND isolation_status IN (?)", ids, cutoff, cutoff, safe).Delete(&model.VMInfectionRecord{})
		if result.Error != nil {
			logger.Errorf("[VM-INFECTION] cleanup delete: %v", result.Error)
			return
		}
		if len(rows) < 500 {
			return
		}
	}
}

const RuleTypeVMMalware = "vm_malware"

func validateVMMalwareRuleConfig(raw json.RawMessage) (map[string]interface{}, error) {
	config := struct {
		ForDuration string `json:"for_duration"`
	}{ForDuration: "0s"}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("invalid VM malware rule config: %w", err)
	}
	duration, err := time.ParseDuration(config.ForDuration)
	if err != nil || duration < 0 {
		return nil, fmt.Errorf("invalid for_duration")
	}
	base := ""
	if host := viper.GetString("console.host"); host != "" {
		port := viper.GetInt("console.port")
		if port <= 0 {
			port = 443
		}
		base = "https://" + net.JoinHostPort(host, strconv.Itoa(port))
	}
	// Keep the already-scraped snapshot available while a configured `for` elapses,
	// even after report_rc removes the node's five-minute textfile.
	windowSeconds := int64(duration/time.Second) + 300
	return map[string]interface{}{
		"for_duration":                config.ForDuration,
		"notification_window":         fmt.Sprintf("%ds", windowSeconds),
		"notification_window_seconds": windowSeconds,
		"query_url":                   strconv.Quote(base + "/vm-infections?instance_uuid={{ $labels.instance_id }}"),
	}, nil
}

var vmInspectionView = &VMInspectionView{}

type VMInspectionView struct{}

type vmInfectionRowView struct {
	model.VMInfectionRecord
	CollectionStatus string
}

type vmInspectionNodeView struct {
	HyperID          int32
	Hostname, HostIP string
	State            string
	Hive             HiveNodeStatus
}

func inspectionDisplay(value interface{}) string {
	if value == nil {
		return "NA"
	}
	switch v := value.(type) {
	case *int64:
		if v == nil {
			return "NA"
		}
		return strconv.FormatInt(*v, 10)
	case []interface{}:
		if len(v) == 0 {
			return "NA"
		}
	case []string:
		if len(v) == 0 {
			return "NA"
		}
	}
	text := fmt.Sprint(value)
	if text == "" {
		return "NA"
	}
	return text
}
func inspectionTime(value interface{}) string {
	var stamp time.Time
	switch v := value.(type) {
	case time.Time:
		stamp = v
	case *time.Time:
		if v != nil {
			stamp = *v
		}
	case string:
		stamp, _ = time.Parse(time.RFC3339Nano, v)
	}
	if stamp.IsZero() {
		return "NA"
	}
	return stamp.UTC().Format("2006-01-02 15:04:05 UTC")
}
func inspectionError(status interface{}, message interface{}) string {
	if text := inspectionDisplay(message); text != "NA" {
		return text
	}
	switch status {
	case "ok", "complete", "isolated", "released":
		return "NONE"
	}
	return "NA"
}
func setVMInspectionDisplay(c *macaron.Context) {
	c.Data["DisplayJSON"] = func(value interface{}) string { data, _ := json.MarshalIndent(value, "", "  "); return string(data) }
	c.Data["Display"] = inspectionDisplay
	c.Data["DisplayTime"] = inspectionTime
	c.Data["DisplayError"] = inspectionError
}

func vmInspectionViewError(c *macaron.Context, err error) {
	status := http.StatusInternalServerError
	var cl *common.CLError
	if errors.As(err, &cl) {
		switch cl.Code {
		case common.ErrPermissionDenied:
			status = http.StatusForbidden
		case common.ErrInvalidParameter:
			status = http.StatusBadRequest
		}
	}
	if gorm.IsRecordNotFoundError(err) {
		status = http.StatusNotFound
	}
	if strings.HasPrefix(err.Error(), "conflict:") {
		status = http.StatusConflict
	}
	logger.Errorf("[VM-INSPECTION] UI path=%s failed: %v", c.Req.URL.Path, err)
	c.Data["ErrorMsg"] = err.Error()
	c.HTML(status, "error")
}

func (*VMInspectionView) Settings(c *macaron.Context) {
	ctx := c.Req.Context()
	if err := requireVMInspectionAdmin(ctx); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	// Only identity fields are needed; do not load per-node resource statistics.
	_, db := common.GetContextDB(ctx)
	var hypers []model.Hyper
	if err := db.Select("hostid, hostname, host_ip").Where("hostid >= 0").Order("hostid").Find(&hypers).Error; err != nil {
		vmInspectionViewError(c, err)
		return
	}
	settings, err := ListVMInspectionSettings(ctx)
	if err != nil {
		vmInspectionViewError(c, err)
		return
	}
	healthByNode := map[int32]HiveNodeStatus{}
	for _, node := range settings.HiveNodes {
		healthByNode[node.HyperID] = node.Hive
	}
	overrides := map[int32]bool{}
	for _, node := range settings.Nodes {
		overrides[node.HyperID] = node.IsolationEnabled
	}
	nodes := make([]vmInspectionNodeView, 0, len(hypers))
	for _, hyper := range hypers {
		state := "default"
		if enabled, ok := overrides[hyper.Hostid]; ok {
			state = strconv.FormatBool(enabled)
		}
		nodes = append(nodes, vmInspectionNodeView{hyper.Hostid, hyper.Hostname, hyper.HostIP, state, healthByNode[hyper.Hostid]})
	}
	setVMInspectionDisplay(c)
	c.Data["Settings"] = settings.Global
	c.Data["Nodes"] = nodes
	c.Data["Saved"] = c.Query("saved") == "1"
	c.HTML(http.StatusOK, "vm_inspection_settings")
}

func (*VMInspectionView) SaveSettings(c *macaron.Context) {
	ctx := c.Req.Context()
	if err := requireVMInspectionAdmin(ctx); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Req.Request.Body = http.MaxBytesReader(c.Resp, c.Req.Request.Body, 1<<20)
	if err := c.Req.ParseForm(); err != nil {
		vmInspectionViewError(c, common.NewCLError(common.ErrInvalidParameter, "invalid settings form", err))
		return
	}
	form := c.Req.PostForm
	enabled, err := strconv.ParseBool(form.Get("isolation_enabled"))
	days, daysErr := strconv.Atoi(form.Get("record_retention_days"))
	if err != nil || daysErr != nil || days <= 0 {
		vmInspectionViewError(c, common.NewCLError(common.ErrInvalidParameter, "invalid global settings", nil))
		return
	}
	patch := VMInspectionSettingsPatch{Global: &VMInspectionGlobalPatch{IsolationEnabled: &enabled, RecordRetentionDays: &days}}
	if values, ok := form["feature_library_url"]; ok && len(values) == 1 {
		patch.Global.FeatureLibraryURL = &values[0]
	}
	if token := form.Get("feature_library_token"); token != "" {
		patch.Global.FeatureLibraryToken = &token
	}
	if form.Get("clear_feature_library_token") == "true" {
		empty := ""
		patch.Global.FeatureLibraryToken = &empty
	}
	if _, ok := form["strong_action"]; ok {
		policy := VMInspectionMatchPolicy{"strong": {Action: form.Get("strong_action")}, "weak": {Action: form.Get("weak_action")}}
		patch.Global.MatchPolicy = &policy
	}
	for key, values := range form {
		if !strings.HasPrefix(key, "node_") {
			continue
		}
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(key, "node_"), 10, 32)
		if parseErr != nil || id < 0 || len(values) != 1 {
			vmInspectionViewError(c, common.NewCLError(common.ErrInvalidParameter, "invalid node setting", nil))
			return
		}
		if values[0] == "default" {
			patch.ResetHyperIDs = append(patch.ResetHyperIDs, int32(id))
			continue
		}
		value, parseErr := strconv.ParseBool(values[0])
		if parseErr != nil {
			vmInspectionViewError(c, common.NewCLError(common.ErrInvalidParameter, "invalid node state", parseErr))
			return
		}
		patch.Nodes = append(patch.Nodes, VMInspectionNodePatch{HyperIDs: []int32{int32(id)}, IsolationEnabled: &value})
	}
	if _, err := UpdateVMInspectionSettings(ctx, patch); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-inspection/settings?saved=1")
}

func (*VMInspectionView) Infections(c *macaron.Context) {
	setVMInspectionDisplay(c)
	config, offset, limit := GetPaginationParams(c, "vm_infections")
	if offset < 0 {
		offset = 0
	}
	p := VMInfectionListInput{InstanceUUID: c.QueryTrim("instance_uuid"), IsolationStatus: c.QueryTrim("isolation_status"), Verdict: c.QueryTrim("verdict"), Offset: int(offset), Limit: int(limit)}
	rows, total, err := ListVMInfections(c.Req.Context(), p)
	if err != nil {
		vmInspectionViewError(c, err)
		return
	}
	records := make([]vmInfectionRowView, 0, len(rows))
	for _, row := range rows {
		var inspection struct {
			CollectionStatus string `json:"collection_status"`
		}
		_ = json.Unmarshal([]byte(row.InspectionResult), &inspection)
		records = append(records, vmInfectionRowView{row, inspection.CollectionStatus})
	}
	c.Data["Records"] = records
	c.Data["InstanceUUID"] = p.InstanceUUID
	c.Data["IsolationStatus"] = p.IsolationStatus
	c.Data["Verdict"] = p.Verdict
	c.Data["Accepted"] = c.Query("accepted") == "1"
	c.Data["Deleted"] = c.Query("deleted") == "1"
	query := url.Values{"instance_uuid": {p.InstanceUUID}, "isolation_status": {p.IsolationStatus}, "verdict": {p.Verdict}}
	c.Data["ExtraQuery"] = template.URL("&" + query.Encode())
	SetPaginationData(c, "vm_infections", int64(total), limit, offset, config, "[]", nil)
	c.HTML(http.StatusOK, "vm_infections")
}

func (*VMInspectionView) Infection(c *macaron.Context) {
	setVMInspectionDisplay(c)
	row, err := GetVMInfection(c.Req.Context(), c.Params(":uuid"))
	if err != nil {
		vmInspectionViewError(c, err)
		return
	}
	rules := []string{}
	if err := json.Unmarshal([]byte(row.MatchRuleIDs), &rules); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	if row.InspectionResult != "" && row.InspectionResult != "null" {
		var inspection map[string]interface{}
		if err := json.Unmarshal([]byte(row.InspectionResult), &inspection); err != nil {
			vmInspectionViewError(c, err)
			return
		}
		c.Data["Inspection"] = inspection
	}
	c.Data["Record"] = row
	c.Data["MatchRules"] = rules
	c.Data["NeedsRestore"] = infectionNeedsRestore(row.IsolationStatus)
	c.HTML(http.StatusOK, "vm_infection")
}

func (*VMInspectionView) Delete(c *macaron.Context) {
	if err := DeleteVMInfection(c.Req.Context(), c.Params(":uuid")); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-infections?deleted=1")
}

// Hive health is the latest node state, never a VM record or a credential store.
type HiveNodeStatus struct {
	SyncStatus           string `json:"sync_status"`
	FetchStatus          string `json:"fetch_status"`
	ConfigUpdatedAt      string `json:"config_updated_at"`
	LastAttemptAt        string `json:"last_attempt_at,omitempty"`
	LastSuccessAt        string `json:"last_success_at,omitempty"`
	SnapshotVersion      *int64 `json:"snapshot_version,omitempty"`
	UsingCache           bool   `json:"using_cache"`
	ErrorCode            string `json:"error_code,omitempty"`
	ErrorMessage         string `json:"error_message,omitempty"`
	CertificateExpiresAt string `json:"certificate_expires_at,omitempty"`
}
type VMInspectionHiveNode struct {
	HyperID  int32          `json:"hyper_id"`
	Hostname string         `json:"hostname"`
	Hive     HiveNodeStatus `json:"hive"`
}

func validAutomaticHiveAction(e *VMInfectionSummary) bool {
	if e.Action != "quarantine" || (e.Verdict != "confirmed" && e.Verdict != "suspected") {
		return false
	}
	var data struct {
		MatchPolicy VMInspectionMatchPolicy `json:"match_policy"`
		Processes   struct {
			Items []struct {
				Matches []struct {
					Confidence string `json:"confidence"`
				} `json:"matches"`
			} `json:"items"`
		} `json:"cputop_processes"`
	}
	if json.Unmarshal(e.InspectionResult, &data) != nil || !validVMInspectionMatchPolicy(data.MatchPolicy) {
		return false
	}
	strong, weak, pin := false, false, false
	for _, p := range data.Processes.Items {
		for _, hit := range p.Matches {
			if hit.Confidence == "strong" {
				strong = true
			} else if hit.Confidence == "weak" {
				weak = true
			} else {
				return false
			}
			pin = pin || data.MatchPolicy[hit.Confidence].Action == "pin"
		}
	}
	return pin && ((e.Verdict == "confirmed" && strong) || (e.Verdict == "suspected" && weak && !strong))
}

var hiveSyncMu sync.Mutex

func syncHiveNodes(ctx context.Context) {
	hiveSyncMu.Lock()
	defer hiveSyncMu.Unlock()
	_, db := common.GetContextDB(ctx)
	var row model.VMInspectionSetting
	if err := db.Where("hyper_id = ?", -1).Take(&row).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			logger.Errorf("[HIVE] read configuration failed: %v", err)
		}
		return
	}
	var hypers []model.Hyper
	if err := db.Where("hostid >= 0 AND (virt_type = ? OR virt_type = ?)", "kvm-x86_64", "").Find(&hypers).Error; err != nil {
		logger.Errorf("[HIVE] list nodes failed: %v", err)
		return
	}
	stamp := row.UpdatedAt.UTC().Format(time.RFC3339Nano)
	payload, _ := json.Marshal(map[string]string{"url": row.FeatureLibraryURL, "token": row.FeatureLibraryToken, "updated_at": stamp})
	encoded := base64.StdEncoding.EncodeToString(payload)
	healthByNode := map[int32]HiveNodeStatus{}
	if row.HiveStatus != "" {
		if err := json.Unmarshal([]byte(row.HiveStatus), &healthByNode); err != nil {
			logger.Errorf("[HIVE] read node status: %v", err)
			return
		}
	}
	saveStatus := func(id int32, health HiveNodeStatus) error {
		tx := db.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		defer tx.Rollback()
		if err := updateHiveNodeStatus(tx, id, health); err != nil {
			return err
		}
		return tx.Commit().Error
	}
	for _, h := range hypers {
		health := healthByNode[h.Hostid]
		health.ConfigUpdatedAt, health.SyncStatus, health.FetchStatus = stamp, "pending", "pending"
		health.ErrorCode, health.ErrorMessage = "", ""
		if err := saveStatus(h.Hostid, health); err != nil {
			logger.Errorf("[HIVE] node=%d pending state: %v", h.Hostid, err)
			continue
		}
		var err error
		for attempt := 0; attempt < 4; attempt++ {
			// Reuse the existing SSH action helper; credentials travel only on stdin.
			script := `source /opt/cloudland/scripts/cloudrc || exit 1; set -e; action_target "$1" 'sudo -E /opt/cloudland/scripts/kvm/hive.sh sync'`
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", script, "hive-sync", strconv.FormatInt(int64(h.Hostid), 10))
			cmd.Stdin = strings.NewReader(encoded + "\n")
			err = cmd.Run()
			if err == nil {
				break
			}
			if attempt < 3 {
				time.Sleep(time.Second)
			}
		}
		if err != nil {
			health.SyncStatus, health.ErrorCode, health.ErrorMessage = "failed", "dispatch_failed", "Hive command could not be sent after retries"
			_ = saveStatus(h.Hostid, health)
			logger.Errorf("[HIVE] node=%d dispatch failed", h.Hostid)
		} else {
			logger.Infof("[HIVE] node=%d configuration sync sent", h.Hostid)
		}
	}
}

// Called only inside report_rc. Config timestamps prevent stale callbacks replacing a new save.
func SaveHiveNodeStatus(ctx context.Context, hyperID int32, encoded string) error {
	if hyperID < 0 || len(encoded) > 1800 {
		return fmt.Errorf("invalid Hive health source/size")
	}
	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(data) > 4096 {
		return fmt.Errorf("invalid Hive health payload")
	}
	var health HiveNodeStatus
	if json.Unmarshal(data, &health) != nil {
		return fmt.Errorf("invalid Hive health JSON")
	}
	if len(health.ErrorMessage) > 512 || len(health.ErrorCode) > 64 {
		return fmt.Errorf("invalid Hive health error")
	}
	_, db := common.GetContextDB(ctx)
	return updateHiveNodeStatus(db, hyperID, health)
}

// The caller's transaction serializes updates to one node entry in the global settings row.
func updateHiveNodeStatus(db *gorm.DB, hyperID int32, health HiveNodeStatus) error {
	stamp, err := time.Parse(time.RFC3339Nano, health.ConfigUpdatedAt)
	if err != nil {
		return fmt.Errorf("invalid Hive config timestamp")
	}
	var settings model.VMInspectionSetting
	if err := db.Set("gorm:query_option", "FOR UPDATE").Where("hyper_id = ?", -1).Take(&settings).Error; err != nil {
		return err
	}
	if stamp.After(settings.UpdatedAt) {
		return nil
	}
	states := map[int32]HiveNodeStatus{}
	if settings.HiveStatus != "" {
		if err := json.Unmarshal([]byte(settings.HiveStatus), &states); err != nil {
			return err
		}
	}
	if previous := states[hyperID].ConfigUpdatedAt; previous != "" {
		applied, err := time.Parse(time.RFC3339Nano, previous)
		if err != nil {
			return err
		}
		if stamp.Before(applied) {
			return nil
		}
	}
	before, _ := json.Marshal(states[hyperID])
	after, _ := json.Marshal(health)
	if bytes.Equal(before, after) {
		return nil
	}
	states[hyperID] = health
	data, _ := json.Marshal(states)
	// A health report must not change the configuration revision or isolation overrides.
	return db.Model(&settings).UpdateColumn("hive_status", string(data)).Error
}
