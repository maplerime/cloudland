#!/bin/bash
set -o pipefail
kvm_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
inspection_dir=$kvm_dir/vm_inspection
source "$kvm_dir/../cloudrc"
source "$inspection_dir/store.sh"
main() {
    [ "$#" -eq 2 ] || return 1
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
    if [ "$action" = quarantine ]; then
        bash "$inspection_dir/common/quarantine_vm.sh" "$work"
    else bash "$inspection_dir/common/release_vm.sh" "$work"; fi
}
main "$@"
