#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# backup.ID, backup.UUID, volume_ID, wdsUUID, wdsOriginPoolID
[ $# -lt 6 ] && echo "$0 <backup_ID> <backup_UUID> <backup_Name> <volume_ID> <wdsUUID> <wdsOriginPoolID> <wdsPoolID" && exit -1

backup_ID=$1
backup_UUID=$2
backup_Name=$3
volume_ID=$4
wdsUUID=$5
wdsOriginPoolID=$6

if [ $# -lt 7 ]; then
    wdsPoolID=$wdsOriginPoolID
else
    wdsPoolID=$7
fi

state='error'
snapshot_id=''

get_wds_token

# 1. take the snapshot of the volume
snapshot_ret=$(wds_curl POST "api/v2/sync/block/snaps/" -d "{
  \"description\": \"$backup_ID\",
  \"name\": \"$backup_UUID\",
  \"volume_id\": \"$wdsUUID\"
}")

read -d'\n' -r snapshot_id ret_code message < <(jq -r ".id, .ret_code, .message" <<<$snapshot_ret)
if [ "$ret_code" != "0" ]; then 
    log_debug $backup_ID "failed to create snapshot for volume $volume_ID: $message"
    echo "|:-COMMAND-:| $(basename $0) '$backup_ID' 'error' 'qcow2' 'failed to create snapshot: $message'"
    exit -1
fi

log_debug $backup_ID "snapshot $snapshot_id created for volume $volume_ID in pool $wdsOriginPoolID"

if [ -z "$wdsPoolID" ] || [ "$wdsPoolID" == "$wdsOriginPoolID" ]; then
    log_debug $backup_ID "snapshot $snapshot_id is in the same pool $wdsOriginPoolID, no need to move"
else
    # 2. copy_clone the snapshot
    log_debug $backup_ID "copying snapshot $snapshot_id from pool $wdsOriginPoolID to pool $wdsPoolID"
    clone_ret=$(wds_curl PUT "api/v2/sync/block/snaps/$snapshot_id/copy_clone" "{\"name\":\"$backup_UUID\", \"speed\": 32, \"phy_pool_id\": \"$wdsPoolID\"}")

    read -d'\n' -r task_id ret_code message < <(jq -r ".task_id, .ret_code, .message" <<<$clone_ret)
    if [ "$ret_code" != "0" ]; then 
        log_debug $backup_ID "failed to clone snapshot $snapshot_id: $message"
        echo "|:-COMMAND-:| $(basename $0) '$backup_ID' 'error' ' ' 'failed to clone the snapshot: $message'"
        exit -1
    fi
    log_debug $backup_ID "clone task $task_id created for snapshot $snapshot_id"
    for i in {1..600}; do
         st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
	     [ "$st" = "TASK_COMPLETE" ] && state=available && break
	     [ "$st" = "TASK_FAILED" ] && state=failed && break
	    sleep 5
    done

    # 3. delete the snapshot
    delete_ret=$(wds_curl DELETE "api/v2/sync/block/snaps/${snapshot_id}?force=false")
    read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$delete_ret)
    log_debug $backup_ID "delete snapshot $snapshot_id: $message"
    # 4. get the volume id from the image name
    snapshot_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$backup_UUID" | jq -r '.volumes[0].id')
fi

[ -n "$snapshot_id" ] && state='available'

echo "|:-COMMAND-:| $(basename $0) '$backup_ID' '$state' 'wds_vhost://$wdsOriginPoolID/$snapshot_id' 'success'"