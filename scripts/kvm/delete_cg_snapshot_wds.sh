#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# Parameters: snapshot_ID, wds_snap_id
[ $# -lt 2 ] && echo "$0 <snapshot_ID> <wds_snap_id>" && exit -1

snapshot_ID=$1
wds_snap_id=$2

state='error'

log_debug $snapshot_ID "delete_cg_snapshot_wds: Starting, snapshot_ID=$snapshot_ID, wds_snap_id=$wds_snap_id"

if [ -z "$wds_address" ]; then
    log_debug $snapshot_ID "delete_cg_snapshot_wds: Error - wds_address is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_snap_id" ]; then
    log_debug $snapshot_ID "delete_cg_snapshot_wds: Error - wds_snap_id is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' 'wds_snap_id is not set'"
    exit -1
fi

get_wds_token

# Delete consistency group snapshot
# 删除一致性组快照
log_debug $snapshot_ID "delete_cg_snapshot_wds: Deleting CG snapshot, wds_snap_id=$wds_snap_id"
result=$(wds_curl "DELETE" "api/v2/sync/block/cg_snaps/$wds_snap_id")
log_debug $snapshot_ID "delete_cg_snapshot_wds: WDS API response: $result"
ret_code=$(echo $result | jq -r '.ret_code // empty')
message=$(echo $result | jq -r '.message // empty')

if [ -z "$ret_code" ] || [ "$ret_code" != "0" ]; then
    log_debug $snapshot_ID "delete_cg_snapshot_wds: Failed to delete CG snapshot, ret_code=$ret_code, message=$message"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' 'error' 'failed to delete CG snapshot: $message (ret_code: $ret_code)'"
    exit -1
fi

state='deleted'
log_debug $snapshot_ID "delete_cg_snapshot_wds: Completed successfully, state=$state"
echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' 'success'"
