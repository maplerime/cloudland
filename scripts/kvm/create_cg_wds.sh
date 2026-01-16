#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# cg_ID, cg_name, pool_ID, volume_UUIDs_JSON
[ $# -lt 4 ] && echo "$0 <cg_ID> <cg_name> <pool_ID> <volume_UUIDs_JSON>" && exit -1

cg_ID=$1
cg_name=$2
pool_ID=$3
volume_UUIDs_JSON=$4  # JSON array string like: [uuid1,uuid2,uuid3]

state='error'
wds_cg_id=''

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' '' 'wds_address is not set'"
    exit -1
fi

if [ -z "$pool_ID" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' '' 'pool_ID is not set'"
    exit -1
fi

get_wds_token

# Get WDS volume IDs from volume UUIDs
# 从卷 UUID 获取 WDS 卷 ID
volume_ids="["
first=true
for vol_uuid in $(echo $volume_UUIDs_JSON | tr -d '[]' | tr ',' ' '); do
    # Query volume by name pattern "vol-<id>-<uuid>"
    wds_vol_id=$(wds_curl GET "api/v2/sync/block/volumes?name=vol-$vol_uuid" | jq -r '.volumes[0].id')
    if [ -z "$wds_vol_id" ] || [ "$wds_vol_id" == "null" ]; then
        # Try searching with just the UUID
        wds_vol_id=$(wds_curl GET "api/v2/sync/block/volumes" | jq -r ".volumes[] | select(.name | contains(\"$vol_uuid\")) | .id" | head -1)
    fi

    if [ -z "$wds_vol_id" ] || [ "$wds_vol_id" == "null" ]; then
        echo "|:-COMMAND-:| $(basename $0) '$cg_ID' 'error' '' 'failed to find WDS volume ID for UUID $vol_uuid'"
        exit -1
    fi

    if [ "$first" = true ]; then
        volume_ids="$volume_ids\"$wds_vol_id\""
        first=false
    else
        volume_ids="$volume_ids,\"$wds_vol_id\""
    fi
done
volume_ids="$volume_ids]"

# Create consistency group
# 创建一致性组
result=$(wds_curl "POST" "api/v2/sync/block/consistency-groups" "{\"name\": \"$cg_name\", \"volume_ids\": $volume_ids}")
ret_code=$(echo $result | jq -r .ret_code)
message=$(echo $result | jq -r .message)

if [ "$ret_code" != "0" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' 'error' '' 'failed to create consistency group: $message'"
    exit -1
fi

wds_cg_id=$(echo $result | jq -r .id)
if [ -z "$wds_cg_id" ] || [ "$wds_cg_id" == "null" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' 'error' '' 'failed to get WDS consistency group ID'"
    exit -1
fi

state='available'
echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' '$wds_cg_id' 'success'"
