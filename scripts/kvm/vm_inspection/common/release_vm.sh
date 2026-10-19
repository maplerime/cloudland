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
    isolation_status=release_failed isolation_cpus='' operation_error=''
    if release_cpu_affinity 2>"$work/release.stderr"; then
        isolation_status=released
    else operation_error=$(tail -c 512 "$work/release.stderr"); fi
    build_vm_infection_summary || return 1
    flock -u 8
    emit_vm_infection_result "$work/summary.json"
}
main "$@"
