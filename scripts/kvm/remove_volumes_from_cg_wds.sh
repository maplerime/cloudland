#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# cg_ID, wds_cg_id, volume_WDSIDs_JSON
[ $# -lt 3 ] && echo "$0 <cg_ID> <wds_cg_id> <volume_WDSIDs_JSON>" && exit -1

cg_ID=$1
wds_cg_id=$2
volume_WDSIDs_JSON=$3  # JSON array string like: ["wds_id1","wds_id2","wds_id3"]

state='error'

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_cg_id" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'wds_cg_id is not set'"
    exit -1
fi

# Get WDS token
# 获取 WDS 令牌
get_wds_token

# Build volume IDs array from WDS IDs
# 从 WDS ID 构建卷 ID 数组
volume_ids="["
first=true
for vol_wds_id in $(echo $volume_WDSIDs_JSON | tr -d '[]"' | tr ',' ' '); do
    if [ "$first" = true ]; then
        volume_ids="$volume_ids\"$vol_wds_id\""
        first=false
    else
        volume_ids="$volume_ids,\"$vol_wds_id\""
    fi
done
volume_ids="$volume_ids]"

# Remove volumes from consistency group via WDS API
# 通过 WDS API 从一致性组删除卷
result=$(wds_curl "PUT" "api/v2/sync/block/consistency_groups/$wds_cg_id/remove_volumes" "{\"volumes\": $volume_ids}")
ret_code=$(echo $result | jq -r .ret_code)

if [ "$ret_code" != "0" ]; then
    message=$(echo $result | jq -r .message)
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' 'error' 'failed to remove volumes from consistency group: $message'"
    exit -1
fi

state='available'
echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'success'"
