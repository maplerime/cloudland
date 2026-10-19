package routes

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/google/uuid"
	"github.com/jinzhu/gorm"
	macaron "gopkg.in/macaron.v1"

	"web/src/callback"
	"web/src/common"
	"web/src/model"
)

var inspectionDomain = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type VMInspectionOperator struct{}
type VMInspectionStartInput struct {
	InstanceUUID, Domain, RegionID, RuleGroupUUID, GlobalRuleID, RequestID string
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
	occurrence, err := time.Parse(time.RFC3339Nano, input.RequestID)
	if err != nil || occurrence.UnixNano() <= 0 {
		return fmt.Errorf("invalid CPU alert occurrence")
	}
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
	config, health, _, err := readHiveLibrary(true)
	if err != nil {
		return err
	}
	if hiveDetectionState(config.URL, health) != "ready" {
		logger.Infof("[VM-INSPECTION] VM=%s scan skipped: Hive not available", inst.UUID)
		return nil
	}
	key := sha256.Sum256([]byte(input.RequestID + "/" + group.UUID + "/" + inst.UUID))
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/inspect_vm.sh %s %s %s", shellQuote(inst.Domain), shellQuote(fmt.Sprintf("%d_%s", occurrence.UnixNano(), hex.EncodeToString(key[:]))), shellQuote(inst.UUID))
	return common.HyperExecute(ctx, fmt.Sprintf("inter=%d", inst.Hyper), command)
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
	Hive      HiveNodeStatus             `json:"hive"`
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
	if err := fillHiveSettings(&out); err != nil {
		return out, err
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
		if value == "" {
			empty := ""
			p.Global.FeatureLibraryToken = &empty
		}
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
	var hiveURL, hiveToken *string
	var effective hiveConfig
	hiveLocked := false
	if hiveChanged {
		hiveConfigUpdateMu.Lock()
		hiveLocked = true
		defer func() {
			if hiveLocked {
				hiveConfigUpdateMu.Unlock()
			}
		}()
		current, err := readHiveConfig()
		if err != nil {
			return VMInspectionSettingsResponse{}, err
		}
		effective = applyHiveConfigPatch(current, p.Global)
		effective.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		hiveURL, hiveToken = &effective.URL, &effective.Token
	}
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
	save := func(id int32, enabled *bool, days *int, libraryURL, libraryToken *string) error {
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
				if libraryToken != nil {
					row.FeatureLibraryToken = *libraryToken
				}
				if p.Global.MatchPolicy != nil {
					data, _ := json.Marshal(*p.Global.MatchPolicy)
					row.MatchPolicy = string(data)
				}
				if row.FeatureLibraryURL == "" {
					row.FeatureLibraryToken = ""
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
			if libraryToken != nil {
				updates["feature_library_token"] = *libraryToken
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
		if err := save(-1, p.Global.IsolationEnabled, p.Global.RecordRetentionDays, hiveURL, hiveToken); err != nil {
			return VMInspectionSettingsResponse{}, err
		}
	}
	for _, group := range p.Nodes {
		for _, id := range group.HyperIDs {
			if err := save(id, group.IsolationEnabled, nil, nil, nil); err != nil {
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
	if hiveChanged {
		if err := writeHiveConfigValue(effective); err != nil {
			return VMInspectionSettingsResponse{}, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		logger.Errorf("[VM-INSPECTION] DB commit failed after monitor config write; DB and monitor may remain divergent until the next successful settings save: %v", err)
		return VMInspectionSettingsResponse{}, err
	}
	if hiveLocked {
		hiveConfigUpdateMu.Unlock()
		hiveLocked = false
	}
	if err := fillHiveSettings(&out); err != nil {
		return out, err
	}
	logger.Infof("[VM-INSPECTION] settings updated operator=%d nodes=%d global=%t", member.UserID, len(ids), globalChanged)
	return out, nil
}

func CreateVMInspectionPinWhitelist(ctx context.Context, instanceUUID, reason string) (*model.VMInspectionPinWhitelist, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(instanceUUID)
	if err != nil {
		return nil, common.NewCLError(common.ErrInvalidParameter, "invalid instance UUID", err)
	}
	instanceUUID = id.String()
	reason = strings.TrimSpace(reason)
	if len(reason) > 1024 || len([]rune(reason)) > 255 {
		return nil, common.NewCLError(common.ErrInvalidParameter, "reason exceeds 255 characters", nil)
	}
	inst, err := instanceAdmin.GetInstanceByUUID(ctx, instanceUUID)
	if err != nil {
		if clErr, ok := err.(*common.CLError); ok && clErr.Code == common.ErrInstanceNotFound {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	_, db := common.GetContextDB(ctx)
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer tx.Rollback()
	// Serialize additions with each other and with deletion of this VM.
	if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", inst.ID).Take(inst).Error; err != nil {
		return nil, err
	}
	if inst.Status == model.InstanceStatusDeleting || inst.Status == model.InstanceStatusDeleted {
		return nil, common.NewCLError(common.ErrInvalidParameter, "instance is being deleted", nil)
	}
	var count int64
	if err := tx.Model(&model.VMInspectionPinWhitelist{}).Where("instance_uuid = ?", instanceUUID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("conflict: instance already has an automatic pin exemption")
	}
	entry := &model.VMInspectionPinWhitelist{
		Model: model.Model{Creater: common.GetMemberShip(ctx).UserID}, InstanceUUID: instanceUUID, Reason: reason,
	}
	if err := tx.Create(entry).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return entry, nil
}

func ListVMInspectionPinWhitelists(ctx context.Context, instanceUUID string, offset, limit int) ([]model.VMInspectionPinWhitelist, int, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return nil, 0, err
	}
	if offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, common.NewCLError(common.ErrInvalidParameter, "invalid pagination", nil)
	}
	_, db := common.GetContextDB(ctx)
	query := db.Model(&model.VMInspectionPinWhitelist{})
	if instanceUUID != "" {
		id, err := uuid.Parse(instanceUUID)
		if err != nil {
			return nil, 0, common.NewCLError(common.ErrInvalidParameter, "invalid instance UUID", err)
		}
		query = query.Where("instance_uuid = ?", id.String())
	}
	var total int
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.VMInspectionPinWhitelist{}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func GetVMInspectionPinWhitelist(ctx context.Context, recordUUID string) (*model.VMInspectionPinWhitelist, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(recordUUID)
	if err != nil {
		return nil, common.NewCLError(common.ErrInvalidParameter, "invalid whitelist record UUID", err)
	}
	_, db := common.GetContextDB(ctx)
	entry := &model.VMInspectionPinWhitelist{}
	if err := db.Where("uuid = ?", id.String()).Take(entry).Error; err != nil {
		return nil, err
	}
	return entry, nil
}

func DeleteVMInspectionPinWhitelist(ctx context.Context, recordUUID string) error {
	entry, err := GetVMInspectionPinWhitelist(ctx, recordUUID)
	if err != nil {
		return err
	}
	_, db := common.GetContextDB(ctx)
	return db.Delete(entry).Error
}

func isVMInspectionPinWhitelisted(ctx context.Context, instanceUUID string) (bool, error) {
	_, db := common.GetContextDB(ctx)
	var entry model.VMInspectionPinWhitelist
	err := db.Select("id").Where("instance_uuid = ?", instanceUUID).Take(&entry).Error
	if gorm.IsRecordNotFoundError(err) {
		return false, nil
	}
	return err == nil, err
}

// This DTO carries node observations and optional judgment/disposition results.
type VMInfectionSummary struct {
	SchemaVersion       int             `json:"schema_version"`
	InstanceUUID        string          `json:"instance_uuid"`
	Domain              string          `json:"domain"`
	SourceHyper         int32           `json:"source_hyper"`
	InspectionResult    json.RawMessage `json:"inspection_result"`
	CollectedAt         *time.Time      `json:"collected_at"`
	RulesetVersion      string          `json:"ruleset_version"`
	MatchSummary        string          `json:"match_summary"`
	Verdict             string          `json:"verdict"`
	MatchRuleIDs        []string        `json:"match_rule_ids"`
	IsolationStatus     string          `json:"isolation_status"`
	IsolationSkipReason string          `json:"isolation_skip_reason"`
	IsolationCPUs       string          `json:"isolation_cpus"`
	IsolationSource     string          `json:"isolation_source"`
	OperationAt         time.Time       `json:"operation_at"`
	Action              string          `json:"action"`
	ActionSource        string          `json:"action_source"`
	ScanID              string          `json:"scan_id"`
	Remark              string          `json:"remark"`
	ErrorMessage        string          `json:"error_message"`
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
	switch e.IsolationSkipReason {
	case "", "whitelist", "configuration_disabled", "settings_unavailable", "whitelist_unavailable", "match_policy", "no_match", "not_evaluated", "affinity_query_failed", "library_unavailable":
	default:
		return fmt.Errorf("invalid isolation skip reason")
	}
	if e.IsolationSkipReason != "" && (e.Action != "record" || (e.IsolationStatus != "not_requested" && e.IsolationStatus != "disabled" && e.IsolationStatus != "")) {
		return fmt.Errorf("isolation skip reason is only valid for an unisolated observation")
	}
	if e.IsolationSource != "" && e.IsolationSource != "automatic" && e.IsolationSource != "manual" && e.IsolationSource != "existing" {
		return fmt.Errorf("invalid isolation source")
	}
	if e.IsolationStatus == "pending" && (e.Action != "record" || e.ActionSource != "automatic") {
		return fmt.Errorf("invalid pending inspection")
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
	case "pending", "disabled", "not_requested", "isolated", "failed", "released", "release_failed":
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
	// SCI callbacks use the reporting node and current VM assignment, not a user login.
	_, db := common.GetContextDB(ctx)
	var inst model.Instance
	if err := db.Where("uuid = ?", e.InstanceUUID).Take(&inst).Error; err != nil {
		return err
	}
	expected := inst.Domain
	if expected == "" {
		expected = fmt.Sprintf("inst-%d", inst.ID)
	}
	if e.Domain != expected {
		return fmt.Errorf("VM domain mismatch")
	}
	if inst.Hyper != source {
		return fmt.Errorf("VM moved or result source mismatched")
	}
	return nil
}
func SaveVMInfectionResult(ctx context.Context, source int32, e *VMInfectionSummary) error {
	// New automatic receipts carry only the operation. Match facts already live in this record.
	if e.ActionSource == "automatic" && e.Action == "quarantine" && (len(e.InspectionResult) == 0 || string(e.InspectionResult) == "null") {
		_, db := common.GetContextDB(ctx)
		var old model.VMInfectionRecord
		if err := db.Where("instance_uuid = ? AND scan_id = ? AND hyper_id = ?", e.InstanceUUID, e.ScanID, source).Order("id DESC").Take(&old).Error; err != nil {
			return err
		}
		if old.IsolationStatus != "pending" {
			return nil
		}
		matched := summaryFromRecord(&old)
		e.InspectionResult, e.CollectedAt, e.Verdict, e.RulesetVersion, e.MatchSummary, e.MatchRuleIDs = matched.InspectionResult, matched.CollectedAt, matched.Verdict, matched.RulesetVersion, matched.MatchSummary, matched.MatchRuleIDs
	}
	if err := validateVMInfectionResult(ctx, source, e); err != nil {
		return err
	}
	logger.Infof("[VM-INFECTION] result received: VM=%s source=%d scan=%q action=%s action_source=%s verdict=%s status=%s cpus=%q operation_at=%s error=%q",
		e.InstanceUUID, source, e.ScanID, e.Action, e.ActionSource, e.Verdict, e.IsolationStatus, e.IsolationCPUs, e.OperationAt.UTC().Format(time.RFC3339Nano), e.ErrorMessage)
	if err := saveVMInfectionResult(ctx, source, e); err != nil {
		return err
	}
	if e.ActionSource == "automatic" && e.Action == "quarantine" {
		inst := &model.Instance{Hyper: source, Domain: e.Domain}
		if err := FinishVMInspection(ctx, inst, e.ScanID); err != nil {
			logger.Errorf("[VM-INSPECTION] cleanup dispatch failed: %v", err)
		}
	}
	return nil
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
	if !fresh && old.HyperID == source && e.ActionSource == "automatic" && e.ScanID != "" && e.ScanID < old.ScanID {
		return nil
	}
	hasInspection := len(e.InspectionResult) > 0 && string(e.InspectionResult) != "null"
	newInspection := hasInspection && e.CollectedAt != nil && (fresh || old.HyperID != source || old.CollectedAt == nil || e.CollectedAt.After(*old.CollectedAt) || (e.CollectedAt.Equal(*old.CollectedAt) && e.ScanID > old.ScanID))
	if !fresh {
		if e.Action == "record" && !newInspection && !(old.ScanID == e.ScanID && old.IsolationStatus == "pending" && e.IsolationStatus != "pending") {
			return nil
		}
		if e.Action != "record" && !e.OperationAt.After(old.OperationAt) && !newInspection && !(old.ScanID == e.ScanID && old.IsolationStatus == "pending") {
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
		if old.HyperID == source && ((e.Action == "record" && e.IsolationStatus == "") || (e.IsolationStatus == "disabled" && old.IsolationStatus != "") || (e.OperationAt.Before(old.OperationAt) && !(old.ScanID == e.ScanID && old.IsolationStatus == "pending"))) {
			e.IsolationStatus, e.IsolationCPUs, e.IsolationSource = old.IsolationStatus, old.IsolationCPUs, old.IsolationSource
			e.IsolationSkipReason = old.IsolationSkipReason
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

	completing := !fresh && old.ScanID == e.ScanID && old.IsolationStatus == "pending" && e.IsolationStatus != "pending"
	ids, _ := json.Marshal(e.MatchRuleIDs)
	updates := map[string]interface{}{"instance_uuid": e.InstanceUUID, "hyper_id": source, "verdict": e.Verdict, "match_rule_ids": string(ids), "isolation_status": e.IsolationStatus, "isolation_cpus": e.IsolationCPUs, "isolation_source": e.IsolationSource, "operation_at": e.OperationAt, "scan_id": e.ScanID, "remark": e.Remark, "error_message": e.ErrorMessage}
	updates["inspection_result"] = string(e.InspectionResult)
	updates["collected_at"] = e.CollectedAt
	updates["ruleset_version"] = e.RulesetVersion
	updates["match_summary"] = e.MatchSummary
	updates["isolation_skip_reason"] = e.IsolationSkipReason
	if fresh {
		row := model.VMInfectionRecord{Model: model.Model{Creater: 1}, InstanceUUID: e.InstanceUUID, HyperID: source, Verdict: e.Verdict, MatchRuleIDs: string(ids), IsolationStatus: e.IsolationStatus, IsolationCPUs: e.IsolationCPUs, IsolationSource: e.IsolationSource, OperationAt: e.OperationAt, ScanID: e.ScanID, Remark: e.Remark, ErrorMessage: e.ErrorMessage}
		row.InspectionResult = string(e.InspectionResult)
		row.CollectedAt = e.CollectedAt
		row.RulesetVersion = e.RulesetVersion
		row.MatchSummary = e.MatchSummary
		row.IsolationSkipReason = e.IsolationSkipReason
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
	if e.ActionSource == "automatic" && e.IsolationStatus != "pending" && (newInspection || completing) {
		pushVMInspectionThreat(db, &inst, e)
	}
	return nil
}

// Called only after a new scan has been committed; retained .done files use the
// same stale-result checks as normal collection and cannot enqueue another event.
func pushVMInspectionThreat(db *gorm.DB, inst *model.Instance, e *VMInfectionSummary) {
	if !callback.IsEnabled() || (e.Verdict != "confirmed" && e.Verdict != "suspected") {
		return
	}
	var org model.Organization
	if err := db.Select("uuid").Where("id = ?", inst.Owner).Take(&org).Error; err != nil || org.UUID == "" {
		logger.Errorf("[VM-INFECTION] VM=%s scan=%q notification skipped: tenant lookup failed: %v", inst.UUID, e.ScanID, err)
		return
	}
	result := map[string]string{
		"isolated": "已隔离", "failed": "隔离失败", "disabled": "未隔离", "not_requested": "未隔离",
		"released": "已解除隔离", "release_failed": "解除隔离失败", "": "隔离状态查询失败",
	}[e.IsolationStatus]
	reason := map[string]string{
		"whitelist": "实例在自动隔离白名单中", "configuration_disabled": "配置禁止自动隔离",
		"settings_unavailable": "隔离配置读取失败", "whitelist_unavailable": "白名单查询失败",
		"match_policy": "匹配策略不要求隔离", "affinity_query_failed": "CPU 亲和性查询失败", "library_unavailable": "特征库已删除或不可用",
	}[e.IsolationSkipReason]
	description := fmt.Sprintf("实例：%s（%s）\n威胁判定：%s\n匹配详情：%s\n隔离结果：%s", inst.Hostname, inst.UUID, e.Verdict, e.MatchSummary, result)
	if reason != "" {
		description += "\n未隔离原因：" + reason
	}
	if e.IsolationCPUs != "" {
		description += "\n隔离 CPU：" + e.IsolationCPUs
	}
	if e.ErrorMessage != "" {
		description += "\n错误信息：" + e.ErrorMessage
	}
	event := &callback.Event{
		EventType: "instance_inspection_threat", Source: "Cloudland", OccurredAt: *e.CollectedAt,
		TenantID: org.UUID,
		Resource: callback.Resource{Type: callback.ResourceTypeInstance.String(), ID: inst.UUID, Name: inst.Hostname, Region: callback.GetRegion()},
		Data: map[string]interface{}{
			"summary": "虚拟机检测发现威胁", "description": description,
			"instance_uuid": inst.UUID, "domain": e.Domain, "hyper_id": e.SourceHyper, "scan_id": e.ScanID,
			"verdict": e.Verdict, "ruleset_version": e.RulesetVersion, "match_rule_ids": append([]string(nil), e.MatchRuleIDs...), "match_summary": e.MatchSummary,
			"status": e.IsolationStatus, "isolation_status": e.IsolationStatus, "isolation_skip_reason": e.IsolationSkipReason,
			"isolation_source": e.IsolationSource, "isolation_cpus": e.IsolationCPUs,
			"action": e.Action, "action_source": e.ActionSource, "operation_at": e.OperationAt, "error_message": e.ErrorMessage,
		},
	}
	// clapi has no callback queue/worker; send the event directly.
	go func() {
		if err := common.SendVMInspectionNotification(context.Background(), event); err != nil {
			logger.Errorf("[VM-INFECTION] VM=%s scan=%q notification send failed: %v", inst.UUID, e.ScanID, err)
			return
		}
		logger.Infof("[VM-INFECTION] VM=%s scan=%q notification sent", inst.UUID, e.ScanID)
	}()
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
	ctx        context.Context
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
	process                        func(context.Context, int32, *VMInfectionSummary) error
	cleanupTimer                   *time.Timer
}

func newVMInfectionRuntime(maxBytes, maxChunks, maxActive int, timeout, ttl time.Duration, process func(context.Context, int32, *VMInfectionSummary) error) *vmInfectionRuntime {
	return &vmInfectionRuntime{active: make(map[vmInfectionKey]*vmInfectionBuffer), completed: make(map[vmInfectionKey]time.Time), maxBytes: maxBytes, maxChunks: maxChunks, maxActive: maxActive, timeout: timeout, ttl: ttl, process: process}
}

var infectionRuntimeOnce sync.Once
var infectionRuntime *vmInfectionRuntime

func ReceiveVMInfectionChunk(ctx context.Context, source int32, chunk VMInfectionChunkInput) error {
	infectionRuntimeOnce.Do(func() {
		infectionRuntime = newVMInfectionRuntime(262144, 512, 128, 60*time.Second, 300*time.Second, func(ctx context.Context, source int32, e *VMInfectionSummary) error {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			return SaveVMInfectionResult(ctx, source, e)
		})
	})
	return infectionRuntime.receive(ctx, source, chunk)
}
func (r *vmInfectionRuntime) receive(ctx context.Context, source int32, c VMInfectionChunkInput) error {
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
		b = &vmInfectionBuffer{ctx: context.WithoutCancel(ctx), key: key, total: c.Total, hash: strings.ToLower(c.SHA256), chunks: make([]string, c.Total)}
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
		err = r.process(b.ctx, b.key.source, e)
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

// Each table has its own query parameters, so paging one preserves the other.
type vmInspectionPage struct {
	Query                string
	Limit, Offset, Total int
	Info                 *PageInfo
	PreviousURL, NextURL string
}

func vmInspectionPageFor(c *macaron.Context, prefix string, total int) vmInspectionPage {
	limit := c.QueryInt(prefix + "_limit")
	if limit != 20 && limit != 50 && limit != 100 {
		limit = 20
	}
	info := GetSmartPaginationInfo(int64(total), int64(limit), 0)
	page := c.QueryInt(prefix + "_page")
	if page < 1 {
		page = 1
	}
	if page > info.TotalPages {
		page = info.TotalPages
	}
	offset := (page - 1) * limit
	info = GetSmartPaginationInfo(int64(total), int64(limit), int64(offset))
	link := func(page int) string {
		q := vmInspectionPageQuery(c)
		q.Set(prefix+"_page", strconv.Itoa(page))
		q.Set(prefix+"_limit", strconv.Itoa(limit))
		return "/vm-inspection/settings?" + q.Encode() + "#" + prefix
	}
	return vmInspectionPage{c.QueryTrim(prefix + "_q"), limit, offset, total, info, link(info.PreviousPage), link(info.NextPage)}
}

func vmInspectionPageQuery(c *macaron.Context) url.Values {
	q := url.Values{}
	for _, key := range []string{"nodes_q", "nodes_page", "nodes_limit", "whitelist_q", "whitelist_page", "whitelist_limit"} {
		if value := c.Query(key); value != "" {
			q.Set(key, value)
		}
	}
	return q
}

func vmInspectionSearch(value string) string {
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(value)) + "%"
}

type vmInspectionWhitelistView struct {
	UUID, InstanceUUID, InstanceName, Organization, Reason string
	CreatedAt                                              time.Time
}

func vmInspectionWhitelistQuery(db *gorm.DB, search string) *gorm.DB {
	q := db.Table("vm_inspection_pin_whitelists AS w").
		Joins("LEFT JOIN instances AS i ON i.uuid = w.instance_uuid AND i.deleted_at IS NULL").
		Joins("LEFT JOIN organizations AS o ON o.id = i.owner AND o.deleted_at IS NULL").
		Where("w.deleted_at IS NULL")
	if search != "" {
		like := vmInspectionSearch(search)
		q = q.Where("(LOWER(w.instance_uuid) LIKE ? ESCAPE '!' OR LOWER(i.hostname) LIKE ? ESCAPE '!' OR LOWER(o.name) LIKE ? ESCAPE '!' OR LOWER(w.reason) LIKE ? ESCAPE '!')", like, like, like, like)
	}
	return q
}

func (*VMInspectionView) Settings(c *macaron.Context) {
	ctx := c.Req.Context()
	if err := requireVMInspectionAdmin(ctx); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	nodeSearch, whitelistSearch := c.QueryTrim("nodes_q"), c.QueryTrim("whitelist_q")
	if len(nodeSearch) > 255 || len(whitelistSearch) > 255 {
		vmInspectionViewError(c, common.NewCLError(common.ErrInvalidParameter, "search is too long", nil))
		return
	}
	_, db := common.GetContextDB(ctx)
	query := db.Model(&model.Hyper{}).Where("hostid >= 0")
	if nodeSearch != "" {
		like := vmInspectionSearch(nodeSearch)
		id, err := strconv.ParseInt(nodeSearch, 10, 32)
		if err != nil {
			id = -1
		}
		query = query.Where("(hostid = ? OR LOWER(hostname) LIKE ? ESCAPE '!' OR LOWER(host_ip) LIKE ? ESCAPE '!')", id, like, like)
	}
	var total int
	if err := query.Count(&total).Error; err != nil {
		vmInspectionViewError(c, err)
		return
	}
	nodePage := vmInspectionPageFor(c, "nodes", total)
	// Only fetch the identities and settings for the displayed page.
	var hypers []model.Hyper
	if err := query.Select("hostid, hostname, host_ip").Order("hostid").Offset(nodePage.Offset).Limit(nodePage.Limit).Find(&hypers).Error; err != nil {
		vmInspectionViewError(c, err)
		return
	}
	ids := []int32{-1}
	for _, hyper := range hypers {
		ids = append(ids, hyper.Hostid)
	}
	var rows []model.VMInspectionSetting
	if err := db.Where("hyper_id IN (?)", ids).Order("hyper_id").Find(&rows).Error; err != nil {
		vmInspectionViewError(c, err)
		return
	}
	settings := inspectionSettingsFromRows(rows)
	if err := fillHiveSettings(&settings); err != nil {
		vmInspectionViewError(c, err)
		return
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
		nodes = append(nodes, vmInspectionNodeView{hyper.Hostid, hyper.Hostname, hyper.HostIP, state, settings.Hive})
	}
	whitelistQuery := vmInspectionWhitelistQuery(db, whitelistSearch)
	if err := whitelistQuery.Count(&total).Error; err != nil {
		vmInspectionViewError(c, err)
		return
	}
	whitelistPage := vmInspectionPageFor(c, "whitelist", total)
	whitelist := []vmInspectionWhitelistView{}
	if err := whitelistQuery.Select("w.uuid, w.instance_uuid, w.reason, w.created_at, COALESCE(i.hostname, '') AS instance_name, COALESCE(o.name, '') AS organization").Order("w.id DESC").Offset(whitelistPage.Offset).Limit(whitelistPage.Limit).Scan(&whitelist).Error; err != nil {
		vmInspectionViewError(c, err)
		return
	}
	setVMInspectionDisplay(c)
	c.Data["Settings"] = settings.Global
	c.Data["Hive"] = settings.Hive
	c.Data["HiveDetectionState"] = func(health HiveNodeStatus) string {
		return hiveDetectionState(settings.Global.FeatureLibraryURL, health)
	}
	c.Data["Nodes"] = nodes
	c.Data["NodePage"] = nodePage
	c.Data["Whitelist"] = whitelist
	c.Data["WhitelistPage"] = whitelistPage
	c.Data["InspectionPageSizes"] = []int{20, 50, 100}
	c.Data["PageQuery"] = template.URL(vmInspectionPageQuery(c).Encode())
	c.Data["Saved"] = c.Query("saved") == "1"
	c.HTML(http.StatusOK, "vm_inspection_settings")
}

func (*VMInspectionView) DeleteFeatureLibrary(c *macaron.Context) {
	_, err := DeleteVMInspectionLibrary(c.Req.Context())
	if err != nil {
		vmInspectionViewError(c, err)
		return
	}
	query := vmInspectionPageQuery(c)
	query.Set("saved", "1")
	c.Redirect("/vm-inspection/settings?" + query.Encode())
}

func (*VMInspectionView) CreatePinWhitelist(c *macaron.Context) {
	if err := requireVMInspectionAdmin(c.Req.Context()); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Req.Request.Body = http.MaxBytesReader(c.Resp, c.Req.Request.Body, 8192)
	if err := c.Req.ParseForm(); err != nil {
		vmInspectionViewError(c, common.NewCLError(common.ErrInvalidParameter, "invalid whitelist form", err))
		return
	}
	if _, err := CreateVMInspectionPinWhitelist(c.Req.Context(), c.Req.PostForm.Get("instance_uuid"), c.Req.PostForm.Get("reason")); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-inspection/settings?" + vmInspectionPageQuery(c).Encode() + "#whitelist")
}

func (*VMInspectionView) DeletePinWhitelist(c *macaron.Context) {
	if err := DeleteVMInspectionPinWhitelist(c.Req.Context(), c.Params(":uuid")); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-inspection/settings?" + vmInspectionPageQuery(c).Encode() + "#whitelist")
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
	query := vmInspectionPageQuery(c)
	query.Set("saved", "1")
	c.Redirect("/vm-inspection/settings?" + query.Encode())
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

func hiveDetectionState(libraryURL string, health HiveNodeStatus) string {
	if strings.TrimSpace(libraryURL) == "" {
		return "not_configured"
	}
	if health.SyncStatus == "failed" {
		return "sync_failed"
	}
	if health.SyncStatus != "ok" || health.ConfigUpdatedAt == "" {
		return "sync_pending"
	}
	if health.FetchStatus == "failed" && health.SnapshotVersion == nil {
		return "fetch_failed"
	}
	if (health.FetchStatus != "ok" && !health.UsingCache) || health.SnapshotVersion == nil {
		return "fetch_pending"
	}
	if _, err := time.Parse(time.RFC3339Nano, health.LastSuccessAt); err != nil {
		return "fetch_pending"
	}
	return "ready"
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

func (*VMInspectionView) RefreshFeatureLibrary(c *macaron.Context) {
	if _, err := RefreshVMInspectionLibrary(c.Req.Context()); err != nil {
		vmInspectionViewError(c, err)
		return
	}
	c.Redirect("/vm-inspection/settings?saved=1")
}
