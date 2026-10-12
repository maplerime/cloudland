#!/bin/bash
set -Eeuo pipefail
cd "$(dirname "$0")"
source ./floating6_common.sh
if [[ ${1:-} == --router ]]; then
    [[ $# == 2 && $2 =~ ^[1-9][0-9]*$ ]] || exit 1
    router_id=$2; router=router-$router_id
    f6_lock
    namespaces=$(ip netns list) || exit 1
    while read -r name rest; do
        [[ $name == "fip6-$router_id-"* ]] || continue
        f6_ids "$router_id" "${name##*-}"
        f6_clear_one || { echo "NAT64 cleanup incomplete; namespace retained for retry" >&2; exit 1; }
    done <<<"$namespaces"
else
    [[ $# == 5 ]] || { echo "$0 <router> <ext_cidr> <int_cidr> <int_vlan> <fip_id>" >&2; exit 1; }
    f6_ids "$1" "$5"
    f6_lock
    f6_clear_one || { echo "NAT64 cleanup incomplete; namespace retained for retry" >&2; exit 1; }
fi
