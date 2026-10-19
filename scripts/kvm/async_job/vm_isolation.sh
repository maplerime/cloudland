#!/bin/bash
set -o pipefail
kvm_dir=$(cd "$(dirname "$0")/.." && pwd) || exit 1
inspection_dir=$kvm_dir/vm_inspection
source "$kvm_dir/../cloudrc"
source "$inspection_dir/store.sh"
main() {
    [ "$#" -eq 4 ] || return 1
    local action=$1 vm=$2 domain=$3 encoded=$4
    [[ "$vm" =~ ^[0-9a-fA-F-]{36}$ && "$domain" =~ ^[A-Za-z0-9_.-]{1,128}$ ]] || return 1
    [[ "${SCI_CLIENT_ID:-}" =~ ^[0-9]+$ ]] || return 1
    case "$action" in quarantine|release) ;; *) return 1;; esac
    inspection_init || return 1
    lock_inspection_work "$vm" || return 1
    work=$(mktemp -d "$inspection_run/action.XXXXXXXX") || return 1
    trap 'rm -rf -- "$work"' EXIT
    printf '%s' "$encoded" | base64 -d > "$work/request.json" || return 1
    jq -e '
      (.operator_id|type=="number" and .>0 and .==floor) and
      (.reason|type=="string" and length>0 and length<=255) and
      (.record_uuid|type=="string" and (.=="" or test("^[0-9a-fA-F-]{36}$")))
      ' "$work/request.json" >/dev/null || return 1
    jq --arg vm "$vm" --arg domain "$domain" --arg action "$action" \
       --argjson hyper "$SCI_CLIENT_ID" --arg region "${cloud_domain:-}" '
      .+{schema_version:1,vm_uuid:$vm,domain:$domain,action:$action,action_source:"manual",
         source_hyper:$hyper,region_id:$region}' "$work/request.json" > "$work/action.json" || return 1
    if [ "$action" = quarantine ];then
        bash "$inspection_dir/common/quarantine_vm.sh" "$work"
    else bash "$inspection_dir/common/release_vm.sh" "$work";fi
}
main "$@"
