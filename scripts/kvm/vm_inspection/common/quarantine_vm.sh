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
    inspection_init && validate_inspection_target "$vm" "$domain" && lock_inspection_vm || return 1
    isolation_status=disabled isolation_cpus='' operation_error=''
    if [ "$(jq -r .action_source "$work/action.json")" = manual ] || is_isolation_enabled "$work/action.json"; then
        isolation_status=failed
        if isolation_cpus=$(bash "$inspection_dir/common/select_quarantine_cpus.sh" "$domain" 2>"$work/isolation.stderr"); then
            if verify_cpu_affinity 2>/dev/null && [ "$(jq -r .action_source "$work/action.json")" = automatic ]; then
                # The current pins already match; this scan does not change their operation time.
                jq '.action="record"' "$work/action.json" > "$work/action.tmp" &&
                    mv -- "$work/action.tmp" "$work/action.json" || return 1
                isolation_status=isolated
            elif apply_cpu_affinity 2>>"$work/isolation.stderr" && verify_cpu_affinity 2>>"$work/isolation.stderr"; then
                isolation_status=isolated
            else
                operation_error="CPU isolation failed: $(tail -c 256 "$work/isolation.stderr")"
                if ! release_cpu_affinity 2>>"$work/isolation.stderr"; then
                    operation_error+="; unpin failed: $(tail -c 128 "$work/isolation.stderr")"
                fi
            fi
        else
            isolation_cpus=''
            operation_error=$(tail -c 512 "$work/isolation.stderr")
        fi
    fi
    build_vm_infection_summary || return 1
    flock -u 8
    emit_vm_infection_result "$work/summary.json"
}
main "$@"
