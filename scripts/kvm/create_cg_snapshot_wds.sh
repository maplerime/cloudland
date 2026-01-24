#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# Parameters: cg_ID, snapshot_ID, snapshot_name, wds_cg_id
[ $# -lt 4 ] && echo "$0 <cg_ID> <snapshot_ID> <snapshot_name> <wds_cg_id>" && exit -1

cg_ID=$1
snapshot_ID=$2
snapshot_name=$3
wds_cg_id=$4

state='error'
wds_snap_id=''
snapshot_size=0

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' '' '0' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_cg_id" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' '' '0' 'wds_cg_id is not set'"
    exit -1
fi

get_wds_token

# Create consistency group snapshot
# 创建一致性组快照
wds_snapshot_name="cg_snap_${snapshot_ID}"
result=$(wds_curl "POST" "api/v2/sync/block/cg_snaps/" "{\"description\": \"$snapshot_name\", \"name\": \"$wds_snapshot_name\", \"cg_id\": \"$wds_cg_id\"}")
ret_code=$(echo $result | jq -r .ret_code)
message=$(echo $result | jq -r .message)

if [ "$ret_code" != "0" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' 'error' '' '0' 'failed to create CG snapshot: $message'"
    exit -1
fi

wds_snap_id=$(echo $result | jq -r .id)
if [ -z "$wds_snap_id" ] || [ "$wds_snap_id" == "null" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' 'error' '' '0' 'failed to get WDS snapshot ID'"
    exit -1
fi

# Get snapshot size
# 获取快照大小
snapshot_size=$(wds_curl GET "api/v2/sync/block/cg_snaps/$wds_snap_id" | jq -r '.size // 0')
if [ -z "$snapshot_size" ] || [ "$snapshot_size" == "null" ]; then
    snapshot_size=0
fi

state='available'
echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' '$wds_snap_id' '$snapshot_size' 'success'"
