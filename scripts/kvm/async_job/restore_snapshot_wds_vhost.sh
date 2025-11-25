#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -ne 7 ] && echo "$0 <task_id> <backup_id> <volume_id> <instance_id> <volume_wds_uuid> <snapshot_wds_uuid>" && exit -1

task_ID=$1
backup_id=$2
origin_vol_ID=$3
instance_id=$4
volume_wds_uuid=$5
snapshot_wds_uuid=$6
volume_pool_id=$7
# snapshot_pool_id=$8
state='failed_to_restore'
vol_path="wds_vhost://$volume_pool_id/$volume_wds_uuid"
get_wds_token

uss_id=$(get_uss_gateway)

# Restore volume from snapshot in the same pool, just call WDS API volume recovery directly
# first, check if the volume is attached to an instance, if so, we need to unbind the vhost first
if [ $instance_id -gt 0 ]; then
    log_debug $task_ID "BACKUP($backup_ID) Volume $volume_wds_uuid is attached to instance $instance_id, unbinding vhost first."
    old_vhost_name=$(basename $(ls /var/run/wds/instance-${instance_id}-volume-${origin_vol_ID}-*))
    if [ -z "$old_vhost_name" ]; then
        old_vhost_name="instance-${instance_id}-vol-${origin_vol_ID}"
    fi
    if [ -z "$old_vhost_name" ] || [ "$old_vhost_name" == "null" ]; then
        log_debug $task_ID "BACKUP($backup_ID) Could not find vhost name for instance $instance_id volume $origin_vol_ID"
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$task_ID' '$backup_id' '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'could not find vhost name for attached volume'"
        exit -1
    fi
    log_debug $task_ID "BACKUP($backup_ID) Volume $volume_wds_uuid vhost name is $old_vhost_name"
    vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$old_vhost_name" | jq -r '.vhosts[0].id')
    delete_vhost $origin_vol_ID $vhost_id $uss_id
fi
log_debug $task_ID "BACKUP($backup_ID) Restoring volume $volume_wds_uuid from snapshot $snapshot_wds_uuid in the same pool $volume_pool_id"
restore_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_wds_uuid/recovery" "{\"snap_id\": \"$snapshot_wds_uuid\"}")

read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$restore_ret)
if [ "$ret_code" != "0" ]; then
    log_debug $task_ID "BACKUP($backup_ID) failed to restore volume $volume_wds_uuid from snapshot $snapshot_wds_uuid: $message"
    echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$task_ID' '$backup_id' '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'failed to restore volume: $message'"
    exit -1
fi
# if the volume is attached to an instance, bind the vhost again
if [ $instance_id -gt 0 ]; then
    vhost_name=$old_vhost_name

    vhost_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
    vhost_id=$(echo $vhost_ret | jq -r .id)
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_wds_uuid\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$task_ID' '$backup_id' '$origin_vol_ID' 'failed_to_restore' '$vol_path' 'failed to create wds vhost for volume, $vhost_ret, $uss_ret!'"
        exit -1
    fi
fi


log_debug $task_ID "BACKUP($backup_ID) volume $volume_wds_uuid restored from snapshot $snapshot_wds_uuid"
state='available'
if [ $instance_id -gt 0 ]; then
    state='attached'
fi

echo "|:-COMMAND-:| restore_snapshot_wds_vhost '$task_ID' '$backup_id' '$origin_vol_ID' '$state' '$vol_path' 'success'"
