#!/bin/bash
set -o pipefail
kvm_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
inspection_dir=$kvm_dir/vm_inspection
source "$kvm_dir/../cloudrc"
source "$inspection_dir/store.sh"
source "$inspection_dir/isolation.sh"
source "$inspection_dir/report.sh"
execute_vm_isolation() (
    vm=$(jq -er .vm_uuid "$work/action.json") || return 1
    domain=$(jq -er .domain "$work/action.json") || return 1
    inspection_init && validate_inspection_target "$vm" "$domain" && lock_inspection_vm || return 1
    case "$1" in
        quarantine) quarantine_vm_cpu;;
        release) release_vm_cpu;;
        *) return 1;;
    esac
    build_vm_infection_summary || return 1
    flock -u 8
    emit_vm_infection_result "$work/summary.json"
)
main() {
    [ "$#" -eq 2 ] || [ "$#" -eq 3 ] || return 1
    local action=$1 domain=$2 vm
    case "$action" in quarantine|release) ;; *) return 1;; esac
    [[ "${SCI_CLIENT_ID:-}" =~ ^[0-9]+$ ]] || return 1
    vm=$(get_inspection_vm_uuid "$domain") || return 1
    inspection_init && lock_inspection_work "$vm" || return 1
    work=$(mktemp -d "$inspection_run/action.XXXXXXXX") || return 1
    trap 'rm -rf -- "$work"' EXIT
    jq -cn --arg vm "$vm" --arg domain "$domain" --arg action "$action" \
       --argjson hyper "$SCI_CLIENT_ID" --arg region "${cloud_domain:-}" '
      {schema_version:1,vm_uuid:$vm,domain:$domain,action:$action,action_source:"manual",
       source_hyper:$hyper,region_id:$region}' > "$work/action.json" || return 1
    if [ "$#" -eq 3 ]; then
        [ "$action" = quarantine ] || return 1
        printf '%s' "$3" | base64 -d > "$work/action.json" || return 1
        jq -e --arg vm "$vm" --arg domain "$domain" --argjson hyper "$SCI_CLIENT_ID" '.vm_uuid==$vm and .domain==$domain and .source_hyper==$hyper and .action=="quarantine" and .action_source=="automatic" and .isolation_enabled==true' "$work/action.json" >/dev/null || return 1
        local scan rc
        scan=$(jq -er .scan_id "$work/action.json") || return 1
        python3 "$inspection_dir/metrics.py" check "$vm" "$domain" "$scan" > "$work/cached-receipt.json"
        rc=$?
        if [ "$rc" -eq 3 ]; then
            if [ -s "$work/cached-receipt.json" ]; then
                emit_vm_infection_result "$work/cached-receipt.json" || return 1
            fi
            return 0
        fi
        [ "$rc" -eq 0 ] || return 1
        execute_vm_isolation "$action"
        rc=$?
        if [ "$rc" -ne 0 ]; then
            isolation_status=failed isolation_cpus='' operation_error='Automatic isolation command failed; see compute job log'
            build_vm_infection_summary && emit_vm_infection_result "$work/summary.json" || return 1
        fi
        python3 "$inspection_dir/metrics.py" save-receipt "$vm" "$domain" "$work/summary.json" || return 1
        return "$rc"
    fi
    execute_vm_isolation "$action"
}
main "$@"
