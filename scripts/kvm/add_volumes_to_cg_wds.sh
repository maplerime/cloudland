#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# cg_ID, wds_cg_id, volume_UUIDs_JSON
[ $# -lt 3 ] && echo "$0 <cg_ID> <wds_cg_id> <volume_UUIDs_JSON>" && exit -1

cg_ID=$1
wds_cg_id=$2
volume_UUIDs_JSON=$3

state='error'

# Get WDS token
# 获取 WDS 令牌
get_wds_token

# Get WDS volume IDs from UUIDs
# 从 UUID 获取 WDS 卷 ID
volume_ids="["
first=true
for vol_uuid in $(echo $volume_UUIDs_JSON | tr -d '[]' | tr ',' ' '); do
    wds_vol_id=$(wds_curl GET "api/v2/sync/block/volumes?name=vol-$vol_uuid" | jq -r '.volumes[0].id')
    if [ -z "$wds_vol_id" ] || [ "$wds_vol_id" = "null" ]; then
        echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'failed to get volume ID for $vol_uuid'"
        exit -1
    fi
    if [ "$first" = true ]; then
        volume_ids="${volume_ids}\"${wds_vol_id}\""
        first=false
    else
        volume_ids="${volume_ids},\"${wds_vol_id}\""
    fi
done
volume_ids="$volume_ids]"

# Add volumes to consistency group via WDS API
# 通过 WDS API 向一致性组添加卷
result=$(wds_curl "PUT" "api/v2/sync/block/consistency-groups/$wds_cg_id/add-volumes" "{\"volume_ids\": $volume_ids}")
ret_code=$(echo $result | jq -r .ret_code)

if [ "$ret_code" != "0" ]; then
    message=$(echo $result | jq -r .message)
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' 'error' 'failed to add volumes to consistency group: $message'"
    exit -1
fi

state='available'
echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'success'"
