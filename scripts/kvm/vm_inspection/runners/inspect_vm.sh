#!/bin/bash
# Cloudland runner: call the original single-purpose PoC scripts for one VM.
# VM selection belongs to the CPU alert; this runner does not scan other VMs.
inspection_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
common_dir=$inspection_dir/common
scripts_dir=$(cd "$inspection_dir/../.." && pwd) || exit 1
work=$1
source "$inspection_dir/store.sh"
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
    JSON=1 timeout --foreground "$((rpc_timeout * 2 + 2))" bash "$common_dir/check_guest_rpc_locked.sh" "$vm_ID" > "$work/rpc.json" 2>"$work/rpc.stderr"
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
    local attempted=false unlock=not_needed unlock_rc
    run_rpc_check
    if jq -e '.status=="ok" and .guest_exec_enabled and (.guest_exec_status_enabled|not)' "$work/rpc.json" >/dev/null; then
        attempted=true
        # Keep the PoC unlock operation in its own script; verify using a fresh check.
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=rpc_unlock start timeout=$((rpc_timeout * 4 + 6))s" >&2
        timeout --foreground "$((rpc_timeout * 4 + 6))" bash "$common_dir/unlock_guest_exec_status.sh" "$vm_ID" > "$work/unlock.stdout" 2>"$work/unlock.stderr"
        unlock_rc=$?
        echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=rpc_unlock end exit=$unlock_rc" >&2
        [ ! -s "$work/unlock.stderr" ] || cat "$work/unlock.stderr" >&2
        run_rpc_check
        unlock=failed
        if jq -e '.status=="ok" and .guest_exec_enabled and .guest_exec_status_enabled' "$work/rpc.json" >/dev/null; then unlock=verified; fi
    fi
    jq -c --argjson attempted "$attempted" --arg unlock "$unlock" '
      .+{unlock_attempted:$attempted,unlock_status:$unlock} |
      if $attempted and $unlock=="failed" then .status="failed" | .error_code="rpc_unlock_failed"
      elif .status=="ok" and (.guest_exec_enabled|not) then .status="failed" | .error_code="rpc_unavailable"
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
    local inner
    run_check cputop_processes "$guest_timeout" "$common_dir/vm_top_cpu_procs.sh" "$vm_ID"
    if [ -s "$work/hive_snapshot.json" ]; then
        local pids hash_files
        pids=$(awk -F, -v vm="$vm_ID" '$2==vm && $5 ~ /^[0-9]+$/ {printf "%s ",$5}' "$work/cputop_processes.out")
        hash_files=$(jq -r 'any(.file[]; .sha256!=null and .sha256!="")' "$work/hive_snapshot.json")
        if [ -n "$pids" ]; then
            inner=$({ printf "PIDS='%s'\nHASH_FILES=%s\n" "$pids" "$hash_files"; cat "$common_dir/inner_process_check.sh"; } | base64 | tr -d '\n')
            run_check process_evidence "$guest_timeout" "$common_dir/outer_check.sh" "$vm_ID" "$inner"
        fi
    fi
    inner=$(base64 < "$common_dir/inner_check.sh" | tr -d '\n')
    run_check cupsd "$guest_timeout" "$common_dir/outer_check.sh" "$vm_ID" "$inner"
    inner=$(base64 < "$common_dir/inner_stat_check.sh" | tr -d '\n')
    run_check timestamps "$guest_timeout" "$common_dir/outer_check.sh" "$vm_ID" "$inner"
}

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
else
    echo "[VM-INSPECTION] $(date -u +%Y-%m-%dT%H:%M:%SZ) domain=$vm_ID step=guest_collection skipped agent=$(jq -c . "$work/agent.json")" >&2
fi
run_check wds "$wds_timeout" "$common_dir/check_uss_core_bind.sh"
