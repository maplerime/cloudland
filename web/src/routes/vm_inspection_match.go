package routes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jinzhu/gorm"
	"web/src/common"
	"web/src/model"
)

const hiveDirectory = "/etc/prometheus/vm_inspection/"

var inspectionMatcherPath = "/opt/cloudland/scripts/kvm/vm_inspection/common/match_inspection.py"
var inspectionMatchLocks sync.Map
var inspectionMatchSlots = make(chan struct{}, 6)

type hiveConfig struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	UpdatedAt string `json:"updated_at"`
}

// hiveConfigUpdateMu serializes monitor config read/merge/write in this process.
var hiveConfigUpdateMu sync.Mutex

// The monitor is the authority for credentials, snapshot and fetch state.
func readHiveConfig() (hiveConfig, error) {
	var config hiveConfig
	exists, err := CheckFileExists(hiveDirectory + "config.json")
	if err != nil || !exists {
		return config, err
	}
	data, err := ReadFile(hiveDirectory + "config.json")
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(data, &config)
	return config, err
}

// applyHiveConfigPatch returns the monitor configuration after applying p.
// Omitted fields keep the monitor's current value.
func applyHiveConfigPatch(config hiveConfig, p *VMInspectionGlobalPatch) hiveConfig {
	if p != nil {
		if p.FeatureLibraryURL != nil {
			config.URL = *p.FeatureLibraryURL
		}
		if p.FeatureLibraryToken != nil {
			config.Token = *p.FeatureLibraryToken
		}
	}
	if config.URL == "" {
		config.Token = ""
	}
	return config
}

func writeHiveConfigValue(config hiveConfig) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	// Empty configuration is a tombstone: the timer clears the cache. Readers reject it immediately.
	return WriteFile(hiveDirectory+"config.json", data, 0600)
}

func writeHiveConfig(p *VMInspectionGlobalPatch) error {
	hiveConfigUpdateMu.Lock()
	defer hiveConfigUpdateMu.Unlock()
	config, err := readHiveConfig()
	if err != nil {
		return err
	}
	config = applyHiveConfigPatch(config, p)
	config.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeHiveConfigValue(config)
}

func readHiveLibrary(snapshot bool) (hiveConfig, HiveNodeStatus, json.RawMessage, error) {
	config, err := readHiveConfig()
	health := HiveNodeStatus{SyncStatus: "ok", FetchStatus: "not_configured", ConfigUpdatedAt: config.UpdatedAt}
	if err != nil || config.URL == "" {
		return config, health, nil, err
	}
	health.SyncStatus, health.FetchStatus = "pending", "pending"
	exists, err := CheckFileExists(hiveDirectory + "hive_status.json")
	if err != nil || !exists {
		return config, health, nil, err
	}
	data, err := ReadFile(hiveDirectory + "hive_status.json")
	if err != nil {
		return config, health, nil, err
	}
	var status struct {
		HiveNodeStatus
		SourceURL string `json:"source_url"`
		Available bool   `json:"available"`
	}
	if err = json.Unmarshal(data, &status); err != nil {
		return config, health, nil, err
	}
	if status.ConfigUpdatedAt != config.UpdatedAt || status.SourceURL != config.URL {
		return config, health, nil, nil
	}
	health = status.HiveNodeStatus
	if !status.Available {
		health.SnapshotVersion = nil
	}
	if !snapshot || hiveDetectionState(config.URL, health) != "ready" {
		return config, health, nil, nil
	}
	data, err = ReadFile(hiveDirectory + "hive_snapshot.json")
	if err != nil {
		return config, health, nil, err
	}
	var version struct {
		Version *int64 `json:"version"`
	}
	if len(data) > 1<<20 || json.Unmarshal(data, &version) != nil || version.Version == nil || *version.Version != *health.SnapshotVersion {
		return config, health, nil, fmt.Errorf("Hive snapshot changed or invalid; retry")
	}
	// A delete/source change during the read must not let an old alert isolate a VM.
	current, err := readHiveConfig()
	if err != nil || current != config {
		return config, health, nil, fmt.Errorf("Hive configuration changed; retry")
	}
	return config, health, data, nil
}

func fillHiveSettings(out *VMInspectionSettingsResponse) error {
	config, health, _, err := readHiveLibrary(false)
	if err != nil {
		return err
	}
	out.Global.FeatureLibraryURL = config.URL
	out.Global.FeatureLibraryTokenConfigured = config.Token != ""
	out.Hive = health
	out.HiveNodes = []VMInspectionHiveNode{} // Compatibility field; health is now regional.
	return nil
}

func DeleteVMInspectionLibrary(ctx context.Context) (VMInspectionSettingsResponse, error) {
	empty := ""
	return UpdateVMInspectionSettings(ctx, VMInspectionSettingsPatch{Global: &VMInspectionGlobalPatch{FeatureLibraryURL: &empty, FeatureLibraryToken: &empty}})
}

func RefreshVMInspectionLibrary(ctx context.Context) (VMInspectionSettingsResponse, error) {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	if err := writeHiveConfig(&VMInspectionGlobalPatch{}); err != nil {
		return VMInspectionSettingsResponse{}, err
	}
	return ListVMInspectionSettings(ctx)
}

type scanSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

func queryScanSamples(ctx context.Context, selector string) ([]scanSample, error) {
	endpoint := fmt.Sprintf("http://%s:%d/api/v1/query", GetPrometheusIP(), GetPrometheusPort())
	// A stale target cannot authorize an action through Prometheus's lookback.
	query := selector + " and on(instance,job) (up == 1) and on(instance,job) (time() - timestamp(up) < 120)"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+url.Values{"query": {query}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string       `json:"resultType"`
			Result     []scanSample `json:"result"`
		} `json:"data"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil || len(body) > 2<<20 || response.StatusCode != 200 {
		return nil, fmt.Errorf("scan query failed or too large")
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Status != "success" || result.Data.ResultType != "vector" {
		return nil, fmt.Errorf("invalid scan query response")
	}
	return result.Data.Result, nil
}

func readScanReport(ctx context.Context, inst *model.Instance, scan string, hyper int32) (map[string]interface{}, error) {
	selector := fmt.Sprintf(`{__name__=~"instance_scan_(flag|context_info|process_info|process_count|collected_timestamp_seconds)",instance_uuid=%q,domain=%q,scan_id=%q,hyper_id=%q,job="prometheus_node_exporter"}`, inst.UUID, inst.Domain, scan, strconv.Itoa(int(hyper)))
	samples, err := queryScanSamples(ctx, selector)
	if err != nil {
		return nil, err
	}
	var report map[string]interface{}
	processes := []interface{}{}
	pids := map[string]bool{}
	flag, count, stamp, contexts := false, -1, 0.0, 0
	target := ""
	for _, sample := range samples {
		if len(sample.Value) != 2 {
			return nil, fmt.Errorf("invalid scan sample")
		}
		var sampled float64
		var raw string
		if json.Unmarshal(sample.Value[0], &sampled) != nil || json.Unmarshal(sample.Value[1], &raw) != nil || time.Since(time.Unix(int64(sampled), 0)) > 2*time.Minute {
			return nil, fmt.Errorf("stale scan sample")
		}
		if target != "" && target != sample.Metric["instance"] {
			return nil, fmt.Errorf("ambiguous scan target")
		}
		target = sample.Metric["instance"]
		switch sample.Metric["__name__"] {
		case "instance_scan_flag":
			flag = raw == "1"
		case "instance_scan_process_count":
			count, err = strconv.Atoi(raw)
		case "instance_scan_collected_timestamp_seconds":
			stamp, err = strconv.ParseFloat(raw, 64)
		case "instance_scan_context_info", "instance_scan_process_info":
			encoded := sample.Metric["data"]
			if len(encoded) > 32768 {
				return nil, fmt.Errorf("scan fact too large")
			}
			data, decodeErr := base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil {
				return nil, decodeErr
			}
			if sample.Metric["__name__"] == "instance_scan_context_info" {
				contexts++
				err = json.Unmarshal(data, &report)
			} else {
				var process map[string]interface{}
				err = json.Unmarshal(data, &process)
				pid, ok := process["pid"].(float64)
				if !ok || pid <= 0 || strconv.FormatFloat(pid, 'f', 0, 64) != sample.Metric["pid"] || pids[sample.Metric["pid"]] {
					return nil, fmt.Errorf("invalid or duplicate process")
				}
				pids[sample.Metric["pid"]] = true
				processes = append(processes, process)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if !flag || contexts != 1 || report == nil || count < 1 || count > 2048 || count != len(processes) || stamp <= 0 {
		return nil, fmt.Errorf("incomplete scan facts")
	}
	finished, err := time.Parse(time.RFC3339Nano, fmt.Sprint(report["finished_at"]))
	if err != nil || float64(finished.Unix()) != stamp || finished.After(time.Now().Add(time.Minute)) || report["scan_id"] != scan {
		return nil, fmt.Errorf("scan timestamp/round mismatch")
	}
	top, ok := report["cputop_processes"].(map[string]interface{})
	if !ok || top["status"] != "ok" {
		return nil, fmt.Errorf("process collection failed")
	}
	sort.SliceStable(processes, func(i, j int) bool {
		a, _ := processes[i].(map[string]interface{})["cpu_percent"].(float64)
		b, _ := processes[j].(map[string]interface{})["cpu_percent"].(float64)
		return a > b
	})
	top["items"] = processes
	return report, nil
}

func matchScan(ctx context.Context, report map[string]interface{}, snapshot json.RawMessage, policy VMInspectionMatchPolicy) (json.RawMessage, VMInfectionSummary, bool, error) {
	select {
	case inspectionMatchSlots <- struct{}{}:
		defer func() { <-inspectionMatchSlots }()
	case <-ctx.Done():
		return nil, VMInfectionSummary{}, false, ctx.Err()
	}

	report["match_policy"] = policy
	data, err := json.Marshal(map[string]interface{}{"report": report, "snapshot": snapshot})
	if err != nil {
		return nil, VMInfectionSummary{}, false, err
	}
	task, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(task, "/usr/bin/python3", inspectionMatcherPath, "stdin")
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.Output()
	if err != nil || len(output) > 1<<20 {
		return nil, VMInfectionSummary{}, false, fmt.Errorf("VM inspection matcher failed")
	}
	var result struct {
		Report     json.RawMessage `json:"report"`
		Evaluation struct {
			VMInfectionSummary
			PinRequested bool `json:"pin_requested"`
		} `json:"evaluation"`
	}
	if err = json.Unmarshal(output, &result); err != nil {
		return nil, VMInfectionSummary{}, false, err
	}
	return result.Report, result.Evaluation.VMInfectionSummary, result.Evaluation.PinRequested, nil
}

func FinishVMInspection(ctx context.Context, inst *model.Instance, scan string) error {
	return common.HyperExecute(ctx, fmt.Sprintf("inter=%d", inst.Hyper), fmt.Sprintf("/opt/cloudland/scripts/kvm/inspect_vm.sh --finish %s %s", shellQuote(inst.Domain), shellQuote(scan)))
}

// MatchVMInspection handles the collection-ready alert, never a client-supplied threat verdict.
func MatchVMInspection(ctx context.Context, labels map[string]string) error {
	if err := requireVMInspectionAdmin(ctx); err != nil {
		return err
	}
	if labels["alertname"] != "VMInspectionReady" || labels["alert_type"] != "vm_inspection_ready" {
		return fmt.Errorf("invalid inspection alert")
	}
	inst, err := resolveInspectionInstance(ctx, labels["instance_uuid"], labels["domain"])
	if err != nil {
		return err
	}
	hyper, err := strconv.ParseInt(labels["hyper_id"], 10, 32)
	scan := labels["scan_id"]
	if err != nil || int32(hyper) != inst.Hyper || !strings.HasPrefix(scan, inst.UUID+"_") || !scanReference.MatchString(scan) {
		return fmt.Errorf("invalid scan target/round")
	}
	if inst.Status == model.InstanceStatusMigrating {
		return fmt.Errorf("VM is migrating")
	}
	lock, _ := inspectionMatchLocks.LoadOrStore(inst.UUID, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	_, db := common.GetContextDB(ctx)
	var old model.VMInfectionRecord
	err = db.Where("instance_uuid = ?", inst.UUID).Order("id DESC").Take(&old).Error
	if err != nil && !gorm.IsRecordNotFoundError(err) {
		return err
	}
	if err == nil && old.ScanID >= scan && old.HyperID == inst.Hyper {
		if old.ScanID == scan && old.IsolationStatus == "pending" {
			return dispatchInspectionPin(ctx, inst, scan)
		}
		return FinishVMInspection(ctx, inst, scan)
	}
	config, health, snapshot, err := readHiveLibrary(true)
	if err != nil {
		return err
	}
	if len(snapshot) == 0 {
		return FinishVMInspection(ctx, inst, scan)
	}
	report, err := readScanReport(ctx, inst, scan, inst.Hyper)
	if err != nil {
		return err
	}
	report["hive"] = health
	settings, err := GetVMInspectionSettings(ctx, inst.Hyper)
	if err != nil {
		return err
	}
	observations, e, pin, err := matchScan(ctx, report, snapshot, settings.MatchPolicy)
	if err != nil {
		return err
	}
	current, err := readHiveConfig()
	if err != nil || current != config {
		return fmt.Errorf("Hive configuration changed before disposition")
	}
	collected, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(report["finished_at"]))
	e.SchemaVersion, e.InstanceUUID, e.Domain, e.SourceHyper = 1, inst.UUID, inst.Domain, inst.Hyper
	e.ScanID, e.InspectionResult, e.CollectedAt = scan, observations, &collected
	e.Action, e.ActionSource, e.OperationAt = "record", "automatic", time.Now().UTC()
	e.IsolationStatus = "not_requested"
	if !pin {
		e.IsolationSkipReason = "match_policy"
	}
	if e.Verdict == "no_match" || e.Verdict == "not_evaluated" {
		e.IsolationSkipReason = e.Verdict
	}
	if !settings.IsolationEnabled {
		pin = false
		e.IsolationSkipReason = "configuration_disabled"
	}
	whitelisted, whiteErr := isVMInspectionPinWhitelisted(ctx, inst.UUID)
	if whiteErr != nil {
		pin = false
		e.IsolationSkipReason = "whitelist_unavailable"
	} else if whitelisted {
		pin = false
		e.IsolationSkipReason = "whitelist"
	}
	if old.HyperID == inst.Hyper && infectionNeedsRestore(old.IsolationStatus) && old.IsolationStatus != "pending" {
		pin = false
		e.IsolationStatus, e.IsolationSource, e.IsolationCPUs, e.OperationAt = old.IsolationStatus, old.IsolationSource, old.IsolationCPUs, old.OperationAt
		e.IsolationSkipReason = ""
	}
	if pin {
		e.IsolationStatus = "pending"
		e.IsolationSource = "automatic"
		e.IsolationSkipReason = ""
	}
	if err = SaveVMInfectionResult(ctx, inst.Hyper, &e); err != nil {
		return err
	}
	if pin {
		return dispatchInspectionPin(ctx, inst, scan)
	}
	return FinishVMInspection(ctx, inst, scan)
}

var scanReference = regexp.MustCompile(`^[0-9a-f-]{36}_[0-9]{8}T[0-9]{6}\.[0-9]{9}Z$`)

func dispatchInspectionPin(ctx context.Context, inst *model.Instance, scan string) error {
	current, err := resolveInspectionInstance(ctx, inst.UUID, inst.Domain)
	if err != nil {
		return err
	}
	if current.Hyper != inst.Hyper || current.Status == model.InstanceStatusMigrating {
		return fmt.Errorf("VM moved or migrating")
	}
	_, db := common.GetContextDB(ctx)
	var old model.VMInfectionRecord
	if err = db.Where("instance_uuid = ? AND scan_id = ? AND hyper_id = ?", inst.UUID, scan, inst.Hyper).Order("id DESC").Take(&old).Error; err != nil {
		return err
	}
	if old.IsolationStatus != "pending" {
		return FinishVMInspection(ctx, inst, scan)
	}
	config, health, _, err := readHiveLibrary(false)
	if err != nil {
		return err
	}
	settings, err := GetVMInspectionSettings(ctx, inst.Hyper)
	if err != nil {
		return err
	}
	whitelist, err := isVMInspectionPinWhitelisted(ctx, inst.UUID)
	if err != nil {
		return err
	}
	check := summaryFromRecord(&old)
	check.Action = "quarantine"
	var observations map[string]interface{}
	if err = json.Unmarshal(check.InspectionResult, &observations); err != nil {
		return err
	}
	observations["match_policy"] = settings.MatchPolicy
	check.InspectionResult, _ = json.Marshal(observations)
	policyAllows := validAutomaticHiveAction(&check)
	if whitelist || !settings.IsolationEnabled || !policyAllows || hiveDetectionState(config.URL, health) != "ready" {
		reason := "configuration_disabled"
		if !policyAllows {
			reason = "match_policy"
		}
		if hiveDetectionState(config.URL, health) != "ready" {
			reason = "library_unavailable"
		}
		if whitelist {
			reason = "whitelist"
		}
		// Complete a previously matched round without an operation. No second scan or match.
		e := summaryFromRecord(&old)
		e.Domain = inst.Domain
		e.Action, e.IsolationStatus, e.IsolationSource, e.OperationAt, e.IsolationSkipReason = "record", "not_requested", "", time.Now().UTC(), reason
		if err = saveVMInfectionResult(ctx, inst.Hyper, &e); err != nil {
			return err
		}
		return FinishVMInspection(ctx, inst, scan)
	}
	payload, _ := json.Marshal(map[string]interface{}{"schema_version": 1, "vm_uuid": inst.UUID, "domain": inst.Domain, "source_hyper": inst.Hyper, "scan_id": scan, "action": "quarantine", "action_source": "automatic", "isolation_enabled": true})
	command := fmt.Sprintf("/opt/cloudland/scripts/kvm/quarantine_vm.sh %s %s", shellQuote(inst.Domain), shellQuote(base64.StdEncoding.EncodeToString(payload)))
	return common.HyperExecute(ctx, fmt.Sprintf("inter=%d", inst.Hyper), command)
}

func summaryFromRecord(row *model.VMInfectionRecord) VMInfectionSummary {
	e := VMInfectionSummary{SchemaVersion: 1, InstanceUUID: row.InstanceUUID, SourceHyper: row.HyperID, InspectionResult: json.RawMessage(row.InspectionResult), CollectedAt: row.CollectedAt, RulesetVersion: row.RulesetVersion, MatchSummary: row.MatchSummary, Verdict: row.Verdict, ScanID: row.ScanID, ActionSource: "automatic"}
	_ = json.Unmarshal([]byte(row.MatchRuleIDs), &e.MatchRuleIDs)
	return e
}
