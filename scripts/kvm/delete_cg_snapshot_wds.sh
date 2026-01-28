#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# Parameters: snapshot_ID, wds_snap_id
[ $# -lt 2 ] && echo "$0 <snapshot_ID> <wds_snap_id>" && exit -1

snapshot_ID=$1
wds_snap_id=$2

state='error'

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_snap_id" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' 'wds_snap_id is not set'"
    exit -1
fi

get_wds_token

# Delete consistency group snapshot
# 删除一致性组快照
result=$(wds_curl "DELETE" "api/v2/sync/block/cg_snaps/$wds_snap_id")
ret_code=$(echo $result | jq -r .ret_code)
message=$(echo $result | jq -r .message)

if [ "$ret_code" != "0" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' 'error' 'failed to delete CG snapshot: $message'"
    exit -1
fi

state='deleted'
echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$state' 'success'"
