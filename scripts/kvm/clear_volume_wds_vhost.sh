#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vol_ID> <vol_UUID> <path>"

wds_vol_ID=$2

get_wds_token
# Remove any lingering vhost before deleting the volume (e.g. from a previously failed detach).
vhost_str=$(wds_curl GET "api/v2/block/volumes/$wds_vol_ID/vhost")
vhost_count=$(echo $vhost_str | jq -r '.count // 0')
if [ -n "$vhost_count" ] && [ "$vhost_count" -gt 0 ]; then
    vhost_id=$(echo $vhost_str | jq -r '.vhosts[0].id')
    uss_id=$(wds_curl GET "api/v2/sync/block/vhost/$vhost_id/vhost_binded_uss" | jq -r '.uss[0].id // empty')
    if [ -n "$uss_id" ]; then
        delete_vhost "$1" "$vhost_id" "$uss_id"
    else
        delete_vhost "$1" "$vhost_id"
    fi
fi
wds_curl DELETE "api/v2/sync/block/volumes/$wds_vol_ID?force=false"
