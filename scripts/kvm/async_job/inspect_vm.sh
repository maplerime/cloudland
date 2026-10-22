#!/bin/bash
set -o pipefail
kvm_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
inspection_dir=$kvm_dir/vm_inspection
source "$kvm_dir/../cloudrc"
source "$inspection_dir/store.sh"
source "$inspection_dir/result.sh"
source "$inspection_dir/evaluation.sh"
source "$inspection_dir/report.sh"
source "$inspection_dir/metrics.sh"
main() {
    local vm domain enabled library_url library_configured=false rc
    if [ "${1:-}" = --collect ]; then
        bash "$inspection_dir/runners/inspect_vm.sh" "$2"
        return
    fi
    inspection_init || return 1
    [ "$#" -eq 3 ] || return 1
    domain=$1 enabled=$2 library_url=$3
    vm=$(get_inspection_vm_uuid "$domain") || return 1
    if [ "$library_url" = NONE ]; then library_url=''; fi
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=input instance=$vm source_hyper=${SCI_CLIENT_ID:-unset} isolation=$enabled" >&2
    [[ "$vm" =~ ^[0-9a-fA-F-]{36}$ && "$domain" =~ ^[A-Za-z0-9_.-]{1,128}$ && "${SCI_CLIENT_ID:-}" =~ ^[0-9]+$ ]] || return 1
    case "$enabled" in true|false) ;; *) return 1;; esac
    work=$(mktemp -d "$inspection_run/collect.XXXXXXXX") || return 1
    trap 'rm -rf -- "$work"' EXIT
    jq -cn --arg vm "$vm" --arg domain "$domain" --argjson hyper "$SCI_CLIENT_ID" \
        '{vm_uuid:$vm,domain:$domain,hyper_id:$hyper}' > "$work/target.json" || return 1
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=lock_and_slot wait" >&2
    lock_inspection_work "$vm" && acquire_inspection_slot || return 1
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=lock_and_slot acquired" >&2
    started=$(date +%s) failure_reason=''
    scan_stamp=$(date -u +%Y%m%dT%H%M%S.%NZ)
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=scan start timeout=${inspection_scan_timeout}s work=$work" >&2
    timeout --foreground -k 2 "$inspection_scan_timeout" bash "$0" --collect "$work"
    rc=$?
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=scan end exit=$rc elapsed=$(($(date +%s)-started))s" >&2
    if [ "$rc" -eq 124 ] || [ "$rc" -eq 137 ]; then failure_reason=scan_timeout; fi
    [ ! -f "$work/failure" ] || failure_reason=$(cat "$work/failure")
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=build_result start" >&2
    build_inspection_result || { rc=$?; echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=build_result failed exit=$rc" >&2; return 1; }
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=evaluation start" >&2
    evaluate_saved_inspection "$work/result.json" "$library_url" || { rc=$?; echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=evaluation failed exit=$rc" >&2; return 1; }
    # A report must be safely stored before any automatic isolation is attempted.
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=save_report start" >&2
    save_inspection_report || { rc=$?; echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=save_report failed exit=$rc" >&2; return 1; }
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=build_structured_result start" >&2
    build_vm_inspection_data || { rc=$?; echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=build_structured_result failed exit=$rc" >&2; return 1; }
    jq -cn --slurpfile o "$work/target.json" --slurpfile e "$work/evaluation.json" \
      --slurpfile data "$work/inspection.json" --arg scan "$scan_id" --arg region "${cloud_domain:-}" --argjson enabled "$enabled" '
      {schema_version:1,vm_uuid:$o[0].vm_uuid,domain:$o[0].domain,
       source_hyper:$o[0].hyper_id,region_id:$region,
       action:(if $e[0].verdict=="confirmed" then "quarantine" else "record" end),
       action_source:"automatic",
       isolation_enabled:$enabled,scan_id:$scan,evaluation:$e[0],
       collected_at:$data[0].finished_at,
       inspection_result:$data[0]}' > "$work/action.json" || return 1
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=disposition verdict=$(jq -r .verdict "$work/evaluation.json") isolation_enabled=$enabled" >&2
    if jq -e '.verdict=="confirmed"' "$work/evaluation.json" >/dev/null;then
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=quarantine start" >&2
        bash "$inspection_dir/common/quarantine_vm.sh" "$work" || return 1
    else
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=record start" >&2
        record_vm_inspection || return 1
    fi
    [ -z "${library_url//[[:space:]]/}" ] || library_configured=true
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=metrics start" >&2
    export_infection_metrics "$work/summary.json" "$work/action.json" "$library_configured" || echo 'failed to publish VM inspection metrics' >&2
    # Only a finished scan AND disposition may be marked for collection/cleanup.
    emit_inspection_report_reference "$vm" "$scan_id"
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$domain step=complete collection_status=$(jq -r .inspection_result.collection_status "$work/summary.json") scan=$scan_id" >&2
    return 0
}
main "$@"
