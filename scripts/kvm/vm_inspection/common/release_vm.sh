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
    action=release
    inspection_init && validate_inspection_target "$vm" "$domain" && lock_inspection_vm || return 1
    load_or_create_infection_record || return 1
    local previous
    previous=$(jq -r .isolation_status "$work/previous.json")
    if [ "$previous" != released ]; then
        isolation_status=release_failed isolation_cpus=$(jq -r '.isolation_cpus // ""' "$work/previous.json") operation_error='release not yet verified'
        build_vm_infection_summary && save_vm_infection_summary || return 1
        if [ -s "$state_dir/affinity.json" ]; then
            if restore_cpu_affinity 2>"$work/release.stderr" && verify_cpu_affinity restored 2>>"$work/release.stderr"; then
                isolation_status=released operation_error=''
            else operation_error=$(tail -c 512 "$work/release.stderr");fi
        elif [ -z "$previous" ] || [ "$previous" = disabled ] || [ "$previous" = not_requested ] || { [ "$previous" = failed ] && [ -z "$isolation_cpus" ]; }; then
            isolation_status=released operation_error=''
        else operation_error='original affinity snapshot missing; restoration cannot be verified';fi
        build_vm_infection_summary && save_vm_infection_summary || return 1
        if [ "$isolation_status" = released ]; then rm -f -- "$state_dir/affinity.json";fi
    fi
    flock -u 8
    emit_vm_infection_result "$state_dir/result.json"
}
main "$@"
