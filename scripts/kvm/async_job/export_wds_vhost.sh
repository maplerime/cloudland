#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

# Arguments: task_ID resource_type wds_volume_id
# resource_type: "volume" or "image"
[ $# -lt 3 ] && echo "$0 <task_ID> <resource_type> <wds_volume_id>" && exit -1

task_ID=$1
resource_type=$2
wds_volume_id=$3
state='failed'

get_wds_token

# Get the USS gateway ID for the current node
uss_id=$(get_uss_gateway)
if [ -z "$uss_id" ]; then
    log_debug $task_ID "EXPORT no USS gateway found on this node"
    echo "|:-COMMAND-:| $(basename $0) '$task_ID' '$resource_type' 'failed' '' 'no uss gateway found on this node'"
    exit -1
fi

# Resolve the WDS volume name from its UUID (works for both volumes and images)
vol_WDS_NAME=$(wds_curl GET "api/v2/sync/block/volumes/$wds_volume_id" | jq -r .volume_detail.name)
if [ -z "$vol_WDS_NAME" ] || [ "$vol_WDS_NAME" = "null" ]; then
    log_debug $task_ID "EXPORT failed to resolve volume name for UUID $wds_volume_id"
    echo "|:-COMMAND-:| $(basename $0) '$task_ID' '$resource_type' 'failed' '' 'failed to get volume name from WDS'"
    exit -1
fi

path="/var/data/uss-vol-dts/export-${resource_type}-${task_ID}-$(date +%s).img"

log_debug $task_ID "EXPORT type=$resource_type vol=$vol_WDS_NAME uss=$uss_id path=$path"

result=$(wds_curl PUT "api/v2/sync/block/volumes/export" \
    "{\"volname\": \"$vol_WDS_NAME\", \"ussid\": \"$uss_id\", \"path\": \"$path\", \"speed\": 32}")
ret_code=$(echo $result | jq -r .ret_code)
wds_task_id=$(echo $result | jq -r .task_id)

if [ "$ret_code" != "0" ] || [ -z "$wds_task_id" ] || [ "$wds_task_id" = "null" ]; then
    msg=$(echo $result | jq -r .message)
    log_debug $task_ID "EXPORT WDS export call failed: $msg"
    echo "|:-COMMAND-:| $(basename $0) '$task_ID' '$resource_type' 'failed' '' 'wds export failed: $msg'"
    exit -1
fi

log_debug $task_ID "EXPORT WDS task_id=$wds_task_id, polling..."

# Poll every 10s, max 720 iterations (2 hours)
for i in {1..720}; do
    st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$wds_task_id" | jq -r .task.state)
    [ "$st" = "TASK_COMPLETE" ] && state=done  && break
    [ "$st" = "TASK_FAILED"   ] && state=failed && break
    sleep 10
done

log_debug $task_ID "EXPORT finished with state=$state"
echo "|:-COMMAND-:| $(basename $0) '$task_ID' '$resource_type' '$state' '$path' 'done'"
