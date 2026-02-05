#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

# Parameters: cg_ID, snapshot_ID, snapshot_name, wds_cg_id
[ $# -lt 4 ] && echo "$0 <cg_ID> <snapshot_ID> <snapshot_name> <wds_cg_id>" && exit -1

cg_ID=$1
snapshot_ID=$2
snapshot_name=$3
wds_cg_id=$4

state='error'
wds_snap_id=''
snapshot_size=0

log_debug $cg_ID "create_cg_snapshot_wds: Starting, cg_ID=$cg_ID, snapshot_ID=$snapshot_ID, snapshot_name=$snapshot_name, wds_cg_id=$wds_cg_id"

if [ -z "$wds_address" ]; then
    log_debug $cg_ID "create_cg_snapshot_wds: Error - wds_address is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' '' '0' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_cg_id" ]; then
    log_debug $cg_ID "create_cg_snapshot_wds: Error - wds_cg_id is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' '' '0' 'wds_cg_id is not set'"
    exit -1
fi

get_wds_token

# Create consistency group snapshot
# 创建一致性组快照
wds_snapshot_name="cg_snap_${snapshot_ID}"
log_debug $cg_ID "create_cg_snapshot_wds: Creating CG snapshot, wds_snapshot_name=$wds_snapshot_name"
result=$(wds_curl "POST" "api/v2/sync/block/cg_snaps/" "{\"description\": \"$snapshot_name\", \"name\": \"$wds_snapshot_name\", \"cg_id\": \"$wds_cg_id\"}")
log_debug $cg_ID "create_cg_snapshot_wds: WDS API response: $result"
ret_code=$(echo $result | jq -r '.ret_code // empty')
message=$(echo $result | jq -r '.message // empty')

if [ -z "$ret_code" ] || [ "$ret_code" != "0" ]; then
    log_debug $cg_ID "create_cg_snapshot_wds: Failed to create CG snapshot, ret_code=$ret_code, message=$message"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' 'error' '' '0' 'failed to create CG snapshot: $message (ret_code: $ret_code)'"
    exit -1
fi
log_debug $cg_ID "create_cg_snapshot_wds: CG snapshot created successfully"

# Fetch the actual snapshot ID by querying the list
# 通过查询列表获取实际的快照 ID
log_debug $cg_ID "create_cg_snapshot_wds: Querying snapshot list to get wds_snap_id"
query_result=$(wds_curl "GET" "api/v2/block/cg_snaps?cg_id=$wds_cg_id&name=$wds_snapshot_name")
wds_snap_id=$(echo $query_result | jq -r '.cg_snaps[0].id')
if [ -z "$wds_snap_id" ] || [ "$wds_snap_id" == "null" ]; then
    log_debug $cg_ID "create_cg_snapshot_wds: Failed to retrieve WDS snapshot ID"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' 'error' '' '0' 'failed to retrieve WDS snapshot ID'"
    exit -1
fi
log_debug $cg_ID "create_cg_snapshot_wds: Retrieved wds_snap_id=$wds_snap_id"

# Get snapshot size by summing all volume snapshot sizes
# 通过累加所有卷快照大小获取总快照大小
log_debug $cg_ID "create_cg_snapshot_wds: Getting snapshot size"
detail_result=$(wds_curl GET "api/v2/block/cg_snaps/$wds_snap_id")
snapshot_size=$(echo $detail_result | jq -r '[.cg_snap_detail[].snap_size] | add // 0')
if [ -z "$snapshot_size" ] || [ "$snapshot_size" == "null" ]; then
    snapshot_size=0
fi
log_debug $cg_ID "create_cg_snapshot_wds: snapshot_size=$snapshot_size"

state='available'
log_debug $cg_ID "create_cg_snapshot_wds: Completed successfully, state=$state, wds_snap_id=$wds_snap_id, snapshot_size=$snapshot_size"
echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' '$wds_snap_id' '$snapshot_size' 'success'"
