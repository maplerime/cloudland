#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 4 ] && die "$0 <volume_ID> <wds_volume_id> <iops_limit> <bps_limit>"

vol_ID=$1
wds_vol_ID=$2
iops_limit=$3
bps_limit=$4

get_wds_token

update_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$wds_vol_ID/qos" "{\"iops_limit\": $iops_limit, \"bps_limit\": $bps_limit}")
log_debug $vol_ID "update volume qos: $update_ret"


read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$update_ret)
if [ "$ret_code" != "0" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$vol_ID' 'error' 'failed to update volume qos: $update_ret'"
    exit -1
fi
log_debug $vol_ID "update volume qos success"


echo "|:-COMMAND-:| $(basename $0) '$vol_ID' 'success' 'success'"
