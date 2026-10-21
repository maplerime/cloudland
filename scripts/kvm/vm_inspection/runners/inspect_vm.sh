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
    if ! JSON=1 timeout "$((rpc_timeout * 2 + 2))" bash "$common_dir/check_guest_rpc_locked.sh" "$vm_ID" > "$work/rpc.json" 2>"$work/rpc.stderr" ||
       ! jq -e -s 'length==1 and (.[0]|type=="object" and has("reachable"))' "$work/rpc.json" >/dev/null 2>&1; then
        printf '%s\n' '{"status":"failed","error_code":"agent_unreachable","reachable":false,"guest_exec_enabled":false,"guest_exec_status_enabled":false}' > "$work/rpc.json"
    fi
}

ensure_guest_rpc() {
    local attempted=false unlock=not_needed
    run_rpc_check
    if jq -e '.status=="ok" and .guest_exec_enabled and (.guest_exec_status_enabled|not)' "$work/rpc.json" >/dev/null; then
        attempted=true
        # Keep the PoC unlock operation in its own script; verify using a fresh check.
        timeout "$((rpc_timeout * 4 + 6))" bash "$common_dir/unlock_guest_exec_status.sh" "$vm_ID" > "$work/unlock.stdout" 2>"$work/unlock.stderr"
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
    local name=$1 budget=$2
    shift 2
    date +%s > "$work/$name.collected_at"
    timeout -k 2 "$budget" bash "$@" > "$work/$name.out" 2>"$work/$name.stderr"
    printf '%s\n' "$?" > "$work/$name.rc"
    date +%s > "$work/$name.finished_at"
}

collect_guest_evidence() {
    local inner
    run_check cputop_processes "$guest_timeout" "$common_dir/vm_top_cpu_procs.sh" "$vm_ID"
    inner=$(base64 < "$common_dir/inner_check.sh" | tr -d '\n')
    run_check cupsd "$guest_timeout" "$common_dir/outer_check.sh" "$vm_ID" "$inner"
    inner=$(base64 < "$common_dir/inner_stat_check.sh" | tr -d '\n')
    run_check timestamps "$guest_timeout" "$common_dir/outer_check.sh" "$vm_ID" "$inner"
}

if ! resolve_target_vm; then
    printf '%s\n' vm_identity_mismatch > "$work/failure"
    exit 1
fi
if ensure_guest_rpc; then collect_guest_evidence; fi
run_check wds "$wds_timeout" "$common_dir/check_uss_core_bind.sh"
