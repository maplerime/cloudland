#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vol_ID> <vol_UUID> <path>"

vol_ID=$1
wds_vol_ID=$2

log_debug $vol_ID "clear_volume_wds_vhost: start, vol_ID=$vol_ID wds_vol_ID=$wds_vol_ID path=$3"

if [ -z "$wds_vol_ID" ]; then
    log_debug $vol_ID "clear_volume_wds_vhost: wds_vol_ID is empty, skipping WDS cleanup"
    exit 0
fi

get_wds_token
log_debug $vol_ID "clear_volume_wds_vhost: wds_token acquired"

# Remove any lingering vhost before deleting the volume (e.g. from a previously failed detach).
vhost_str=$(wds_curl GET "api/v2/block/volumes/$wds_vol_ID/vhost")
vhost_count=$(echo $vhost_str | jq -r '.count // 0')
log_debug $vol_ID "clear_volume_wds_vhost: vhost check result: count=$vhost_count raw=$vhost_str"

if [ -n "$vhost_count" ] && [ "$vhost_count" -gt 0 ]; then
    vhost_id=$(echo $vhost_str | jq -r '.vhosts[0].id')
    log_debug $vol_ID "clear_volume_wds_vhost: found lingering vhost $vhost_id, querying USS binding"
    uss_id=$(wds_curl GET "api/v2/sync/block/vhost/$vhost_id/vhost_binded_uss" | jq -r '.uss[0].id // empty')
    log_debug $vol_ID "clear_volume_wds_vhost: uss_id=$uss_id, calling delete_vhost"
    if [ -n "$uss_id" ]; then
        delete_vhost "$vol_ID" "$vhost_id" "$uss_id"
    else
        delete_vhost "$vol_ID" "$vhost_id"
    fi
    # Verify vhost was actually removed before attempting volume delete
    vhost_check=$(wds_curl GET "api/v2/block/volumes/$wds_vol_ID/vhost")
    remaining=$(echo $vhost_check | jq -r '.count // 0')
    log_debug $vol_ID "clear_volume_wds_vhost: post-delete vhost count=$remaining"
    if [ -n "$remaining" ] && [ "$remaining" -gt 0 ]; then
        log_debug $vol_ID "clear_volume_wds_vhost: ERROR vhost still exists after delete_vhost, volume DELETE may fail"
    fi
fi

log_debug $vol_ID "clear_volume_wds_vhost: deleting WDS volume $wds_vol_ID"
delete_ret=$(wds_curl DELETE "api/v2/sync/block/volumes/$wds_vol_ID?force=false")
ret_code=$(echo $delete_ret | jq -r '.ret_code // empty')
log_debug $vol_ID "clear_volume_wds_vhost: WDS DELETE result: ret_code=$ret_code raw=$delete_ret"
