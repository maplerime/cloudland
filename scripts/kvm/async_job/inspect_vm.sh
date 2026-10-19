#!/bin/bash
set -o pipefail
kvm_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
inspection_dir=$kvm_dir/vm_inspection
source "$kvm_dir/../cloudrc"
source "$inspection_dir/store.sh"
source "$inspection_dir/report.sh"
export VM_INSPECTION_KEEP_RESULTS="$inspection_debug_keep_results"
collect_vm_evidence() (
    common_dir=$inspection_dir/common
    scripts_dir=$(cd "$inspection_dir/../.." && pwd) || exit 1
    work=$1
    vm_ID=$(jq -r .domain "$work/target.json")
    vm_uuid=$(jq -r .vm_uuid "$work/target.json")
    rpc_timeout=$inspection_rpc_timeout
    guest_timeout=$inspection_guest_timeout
    wds_timeout=$inspection_wds_timeout
    export CLOUDRC="$scripts_dir/cloudrc" RPC_TIMEOUT="$rpc_timeout"
    export GUEST_EXEC_TIMEOUT="$guest_timeout" TOPN="$inspection_top_n" CSV=1 LC_ALL=C

    resolve_target_vm() { validate_inspection_target "$vm_uuid" "$vm_ID"; }

    run_rpc_check() {
        local rc
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=rpc_check start timeout=$((rpc_timeout * 2 + 2))s" >&2
        JSON=1 timeout --foreground "$((rpc_timeout * 2 + 2))" bash "$common_dir/inspection.sh" rpc "$vm_ID" > "$work/rpc.json" 2>"$work/rpc.stderr"
        rc=$?
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=rpc_check end exit=$rc" >&2
        if [ "$rc" -ne 0 ] ||
           ! jq -e -s 'length==1 and (.[0]|type=="object" and has("reachable"))' "$work/rpc.json" >/dev/null 2>&1; then
            echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=rpc_check failed exit=$rc; recording agent_unreachable" >&2
            [ ! -s "$work/rpc.stderr" ] || cat "$work/rpc.stderr" >&2
            printf '%s\n' '{"status":"failed","error_code":"agent_unreachable","reachable":false,"guest_exec_enabled":false,"guest_exec_status_enabled":false}' > "$work/rpc.json"
        fi
    }

    ensure_guest_rpc() {
        local attempted=false unlock=not_needed
        run_rpc_check
        if jq -e '.rpc_original_enabled==false' "${cache_dir:-/opt/cloudland/cache}/vm_inspection/scans/$vm_uuid.json" >/dev/null 2>&1; then
            attempted=true unlock=verified
        fi
        jq -c --argjson attempted "$attempted" --arg unlock "$unlock" '
          .+{unlock_attempted:$attempted,unlock_status:$unlock} |
          if .status=="ok" and (.guest_exec_enabled|not) then .status="failed" | .error_code="rpc_unavailable"
          else . end' "$work/rpc.json" > "$work/agent.tmp" && mv "$work/agent.tmp" "$work/agent.json"
        jq -e '.status=="ok" and .guest_exec_enabled and .guest_exec_status_enabled' "$work/agent.json" >/dev/null
    }

    run_check() {
        local name=$1 budget=$2 rc
        shift 2
        date +%s > "$work/$name.collected_at"
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=$name start timeout=${budget}s" >&2
        timeout --foreground -k 2 "$budget" bash "$@" > "$work/$name.out" 2>"$work/$name.stderr"
        rc=$?
        printf '%s\n' "$rc" > "$work/$name.rc"
        date +%s > "$work/$name.finished_at"
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=$name end exit=$rc" >&2
        [ "$rc" -eq 0 ] || { [ ! -s "$work/$name.stderr" ] || cat "$work/$name.stderr" >&2; }
        return 0
    }

    collect_guest_evidence() {
        run_check cputop_processes "$guest_timeout" "$common_dir/inspection.sh" processes "$vm_ID"
        local pids
        pids=$(awk -F, -v vm="$vm_ID" -v limit="$inspection_top_n" '$2==vm && $5 ~ /^[0-9]+$/ {if (++n<=limit) printf "%s ",$5}' "$work/cputop_processes.out")
        if [ -n "$pids" ]; then
            run_check process_evidence "$guest_timeout" "$common_dir/inspection.sh" process "$vm_ID" "$pids"
        fi
    }

    collect_additional_checks() {
        run_check cupsd "$guest_timeout" "$common_dir/inspection.sh" cupsd "$vm_ID"
        run_check timestamps "$guest_timeout" "$common_dir/inspection.sh" timestamps "$vm_ID"
        run_check wds "$wds_timeout" "$common_dir/inspection.sh" wds
    }

    if [ "${2:-}" = details ]; then
        collect_additional_checks
        exit 0
    fi

    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=identity start" >&2
    if ! resolve_target_vm; then
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=identity failed; skipping collection" >&2
        printf '%s\n' vm_identity_mismatch > "$work/failure"
        exit 1
    fi
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=identity end exit=0" >&2
    if ensure_guest_rpc; then
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=guest_collection start" >&2
        collect_guest_evidence
        collect_additional_checks
    else
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=guest_collection skipped agent=$(jq -c . "$work/agent.json")" >&2
    fi
)

main() {
    if [ "${1:-}" = --collect ]; then
        collect_vm_evidence "$2"
        return
    fi
    inspection_init || return 1
    [ "$#" -eq 3 ] || return 1
    local domain=$1 request=$2 expected=$3 vm scan rc error_code
    if [ "$1" = --finish ]; then domain=$2; fi
    vm=$(get_inspection_vm_uuid "$domain") || return 1
    lock_inspection_work "$vm" || return 1
    if [ "$1" = --finish ]; then
        python3 "$inspection_dir/metrics.py" finish "$vm" "$domain" "$3"
        return
    fi
    [ "$vm" = "$expected" ] && [[ "$request" =~ ^[0-9]+_[a-f0-9]{64}$ && "${SCI_CLIENT_ID:-}" =~ ^[0-9]+$ ]] || return 1
    acquire_inspection_slot || return 1
    scan="${vm}_$(date -u +%Y%m%dT%H%M%S.%NZ)"
    python3 "$inspection_dir/metrics.py" begin "$vm" "$domain" "$request" "$SCI_CLIENT_ID" "$scan"
    rc=$?
    [ "$rc" -ne 3 ] || return 0
    if [ "$rc" -ne 0 ]; then
        python3 "$inspection_dir/metrics.py" fail "$vm" "$domain" rpc_prepare_failed
        return 1
    fi
    work=$(mktemp -d "$inspection_run/collect.XXXXXXXX") || return 1
    trap 'rm -rf -- "$work"' EXIT
    jq -cn --arg vm "$vm" --arg domain "$domain" --argjson hyper "$SCI_CLIENT_ID" '{vm_uuid:$vm,domain:$domain,hyper_id:$hyper}' > "$work/target.json" || return 1
    started=$(date +%s) failure_reason=''
    timeout --foreground -k 2 "$inspection_scan_timeout" bash "$0" --collect "$work"
    rc=$?
    if [ "$rc" -ne 0 ]; then
        error_code=scan_failed
        [ "$rc" -ne 124 ] && [ "$rc" -ne 137 ] || error_code=scan_timeout
        python3 "$inspection_dir/metrics.py" fail "$vm" "$domain" "$error_code"
        return 1
    fi
    build_inspection_result && cp "$work/result.json" "$work/report.json" && build_vm_inspection_data && python3 "$inspection_dir/metrics.py" publish "$vm" "$domain" "$work"
    rc=$?
    if [ "$rc" -ne 0 ]; then
        error_code=$(jq -r 'if .agent.status!="ok" then .agent.error_message elif (.cputop_processes.items|length)==0 then "empty_process_list" else "evidence_publish_failed" end' "$work/result.json" 2>/dev/null)
        python3 "$inspection_dir/metrics.py" fail "$vm" "$domain" "${error_code:-evidence_publish_failed}"
        return 1
    fi
    echo "[VM-INSPECTION] VM=$vm scan=$scan collection ready; awaiting controller match" >&2
}
main "$@"
