#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -ne 7 ] && echo "$0 <backup_id> <volume_id> <instance_id> <volume_wds_uuid> <snapshot_wds_uuid> <volume_pool_id> <snapshot_pool_id>" && exit -1

backup_id=$1
origin_vol_ID=$2
instance_id=$3
volume_wds_uuid=$4
snapshot_wds_uuid=$5
volume_pool_id=$6
snapshot_pool_id=$7
state='failed_to_restore'
vol_path="wds_vhost://$volume_pool_id/$volume_wds_uuid"
get_wds_token

if [ "$volume_pool_id" == "$snapshot_pool_id" ]; then
    # Restore volume from snapshot in the same pool, just call WDS API volume recovery directly
    log_debug "Restoring volume $volume_wds_uuid from snapshot $snapshot_wds_uuid in the same pool $volume_pool_id"
    restore_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_wds_uuid/recovery" "{\"snap_id\": \"$snapshot_wds_uuid\"}")

    read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$restore_ret)
    if [ "$ret_code" != "0" ]; then
        log_debug "failed to restore volume $volume_wds_uuid from snapshot $snapshot_wds_uuid: $message"
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' '$state' '$vol_path' 'failed to restore volume: $message'"
        exit -1
    fi
else
    # Restore volume from snapshot in different pool here we need do following steps:
    # 1. since the cross-pool backup is a full volume clone, we need copy the clone volume to the original pool
    # 2. unbind vhost
    # 3. bind vhost to the cloned volume
    # 4. delete the original volume
    # 5. return the restore status and WDS volume UUID

    log_debug "Restoring volume $volume_wds_uuid from snapshot $snapshot_wds_uuid in different pool, from $snapshot_pool_id to $volume_pool_id"
    new_vol_name="restored-from-$snapshot_wds_uuid"
    clone_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$snapshot_wds_uuid/copy_clone" "{\"name\": \"$new_vol_name\", \"phy_pool_id\": \"$volume_pool_id\"}")
    read -d'\n' -r ret_code message task_id < <(jq -r ".ret_code, .message, .task_id" <<<$clone_ret)
    if [ "$ret_code" != "0" ]; then
        log_debug "failed to clone volume from $snapshot_wds_uuid: $message"
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'failed to clone volume: $message'"
        exit -1
    fi
    log_debug "Begun cloning volume $snapshot_wds_uuid to new volume named $new_vol_name in pool $volume_pool_id with task $task_id"
    
    for i in {1..600}; do
         st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
	     [ "$st" = "TASK_COMPLETE" ] && state=available && break
	     [ "$st" = "TASK_FAILED" ] && state=failed_to_restore && break
	    sleep 5
    done
    if [ "$state" != "available" ]; then
        log_debug "Failed to clone volume $snapshot_wds_uuid to new volume $new_vol_name in pool $volume_pool_id, task state: $st"
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'failed to clone volume: task state is $st'"
        exit -1
    fi
    # find the new volume UUID
    volumes_ret=$(wds_curl GET "api/v2/sync/block/volumes?name=$new_vol_name")
    read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$volumes_ret)
    if [ "$ret_code" != "0" ]; then
        log_debug "failed to get volume by name $new_vol_name: $message"
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'failed to get volume by name: $message'"
        exit -1
    fi
    new_volume_wds_uuid=$(jq -r ".volumes[0].id" <<<$volumes_ret)
    if [ -z "$new_volume_wds_uuid" ] || [ "$new_volume_wds_uuid" == "null" ]; then
        log_debug "Could not find volume with name $new_vol_name"
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'Could not find cloned volume by name'"
        exit -1
    fi
    log_debug "Found new volume $new_vol_name with uuid $new_volume_wds_uuid"

    vol_path="wds_vhost://$volume_pool_id/$new_volume_wds_uuid"
    log_debug "volume $origin_vol_ID restored to new volume $new_volume_wds_uuid with path $vol_path"

    if [ $instance_id -gt 0 ]; then
        # unbind the old vhost
        log_debug "Unbinding old vhost for instance $instance_id"
        old_vhost_name=$(basename $(ls /var/run/wds/instance-$instance_id-volume-$origin_vol_ID-*))
        vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$old_vhost_name" | jq -r '.vhosts[0].id')
        uss_id=$(get_uss_gateway)
        delete_vhost $origin_vol_ID $vhost_id $uss_id

        # bind the new volume to a new vhost
        for i in {1..20}; do
            vhost_name=instance-$instance_id-volume-$origin_vol_ID-$RANDOM
            [ "$vhost_name" != "$old_vhost_name" ] && break
        done
        vhost_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
        vhost_id=$(echo $vhost_ret | jq -r .id)
        uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$new_volume_wds_uuid\", \"is_snapshot\": false}")
        ret_code=$(echo $uss_ret | jq -r .ret_code)
        if [ "$ret_code" != "0" ]; then
            echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'failed to create wds vhost for boot volume, $vhost_ret, $uss_ret!'"
            exit -1
        fi
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' 'attached' '$vol_path' 'success'"
        exit 0
    fi

    # delete the original volume
    log_debug "Force deleting original volume $volume_wds_uuid"
    wds_curl DELETE "api/v2/sync/block/volumes/$volume_wds_uuid?force=true"

fi

log_debug "volume $volume_wds_uuid restored from snapshot $snapshot_wds_uuid"
state='available'
if [ $instance_id -gt 0 ]; then
    state='attached'
fi

echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$origin_vol_ID' '$state' '$vol_path' 'success'"
