#!/bin/bash
set -o pipefail
inspection_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
source "$inspection_dir/store.sh"
source "$inspection_dir/isolation.sh"
source "$inspection_dir/report.sh"
main() {
    work=$1
    vm=$(jq -er .vm_uuid "$work/action.json") || return 1
    domain=$(jq -er .domain "$work/action.json") || return 1
    action=$(jq -er .action "$work/action.json") || return 1
    inspection_init && validate_inspection_target "$vm" "$domain" && lock_inspection_vm || return 1
    load_or_create_infection_record || return 1
    local previous
    previous=$(jq -r '.isolation_status // ""' "$work/previous.json")
    # Retries reuse the original snapshot and current business record.
    if [ "$previous" = isolated ]; then
        if jq -e '.evaluation.verdict=="confirmed"' "$work/action.json" >/dev/null; then
            # Record this scan without treating the earlier isolation as a new operation.
            jq '.action="record"' "$work/action.json" > "$work/action.tmp" &&
                mv -- "$work/action.tmp" "$work/action.json" || return 1
            isolation_status=isolated isolation_cpus=$(jq -r .isolation_cpus "$work/previous.json") operation_error=''
            build_vm_infection_summary && save_vm_infection_summary || return 1
        fi
    else
        isolation_status=disabled isolation_cpus='' operation_error=''
        if [ "$(jq -r .action_source "$work/action.json")" = manual ] || is_isolation_enabled "$work/action.json"; then
            isolation_status=failed
            if isolation_cpus=$(bash "$inspection_dir/common/select_quarantine_cpus.sh" "$domain" 2>"$work/isolation.stderr") && save_cpu_affinity 2>>"$work/isolation.stderr"; then
                # Save an unfinished state before modifying CPU affinity, so restore remains possible.
                operation_error='affinity operation not yet verified'
                build_vm_infection_summary && save_vm_infection_summary || return 1
                if apply_cpu_affinity 2>>"$work/isolation.stderr" && verify_cpu_affinity 2>>"$work/isolation.stderr"; then
                    isolation_status=isolated operation_error=''
                else
                    operation_error='CPU isolation failed; '
                    if restore_cpu_affinity 2>>"$work/isolation.stderr" && verify_cpu_affinity restored 2>>"$work/isolation.stderr"; then
                        operation_error+='original affinity restored'
                    else operation_error+='restore incomplete; original snapshot retained';fi
                fi
            else
                operation_error=$(tail -c 512 "$work/isolation.stderr")
                if [ -e "$state_dir/affinity.json" ];then isolation_cpus=$(jq -r '.isolation_cpus // ""' "$work/previous.json");else isolation_cpus='';fi
            fi
        fi
        if [ "$isolation_status" = disabled ] && [ -e "$state_dir/affinity.json" ];then
            isolation_status=$previous
            isolation_cpus=$(jq -r '.isolation_cpus // ""' "$work/previous.json")
            operation_error=$(jq -r '.error_message // ""' "$work/previous.json")
        fi
        build_vm_infection_summary && save_vm_infection_summary || return 1
    fi
    flock -u 8
    emit_vm_infection_result "$state_dir/result.json"
}
main "$@"
