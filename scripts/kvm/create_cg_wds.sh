#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# cg_ID, cg_name, volume_WDSIDs_JSON
[ $# -lt 3 ] && echo "$0 <cg_ID> <cg_name> <volume_WDSIDs_JSON>" && exit -1

cg_ID=$1
cg_name=$2
volume_WDSIDs_JSON=$3  # JSON array string like: ["wds_id1","wds_id2","wds_id3"]

state='error'
wds_cg_id=''

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' '' 'wds_address is not set'"
    exit -1
fi

get_wds_token

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

# Create consistency group
# 创建一致性组
result=$(wds_curl "POST" "api/v2/sync/block/consistency_groups" "{\"name\": \"$cg_name\", \"volumes\": $volume_ids}")
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
