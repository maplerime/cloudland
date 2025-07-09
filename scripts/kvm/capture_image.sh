#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 5 ] && echo "$0 <img_ID> <img_Prefix> <vm_ID> <boot_volume> <storage_ID>" && exit -1

img_ID=$1
prefix=$2
vm_ID=inst-$3
boot_volume=$4
storage_ID=$5
image_name=image-$img_ID-$prefix
state=error

# its better to let user shutdown the vm before capturing the image
# virsh suspend $vm_ID
if [ -z "$wds_address" ]; then
    # capture the image from the running instance locally
    image=${image_dir}/image-$vm_ID.qcow2
    inst_img=$cache_dir/instance/${vm_ID}.disk

    format=$(qemu-img info $inst_img | grep 'file format' | cut -d' ' -f3)
    qemu-img convert -f $format -O qcow2 $inst_img $image
    [ -s "$image" ] && state=available
    sync_target /opt/cloudland/cache/image/
    volume_id=""
else
    # clone the image from the boot volume on the remote storage WDS
    if [ -z "$boot_volume" ]; then
        echo "|:-COMMAND-:| capture_image.sh '$img_ID' 'error' 'qcow2' 'boot_volume is not specified' '' '$storage_ID'"
        exit -1
    fi
    get_wds_token
    # refine the image capture flow
    # 1. take the snapshot of the boot volume
    snapshot_ret=$(wds_curl POST "api/v2/sync/block/snaps/" "{\"description\":\"snapshot for image $image_name\", \"name\":\"$image_name\", \"volume_id\":\"$boot_volume\"}")
    read -d'\n' -r snapshot_id ret_code message < <(jq -r ".id, .ret_code, .message" <<<$snapshot_ret)
    if [ "$ret_code" != "0" ]; then
        log_debug $vm_ID "failed to create snapshot for boot volume $boot_volume: $message"
        echo "|:-COMMAND-:| capture_image.sh '$img_ID' 'error' 'qcow2' 'failed to create snapshot for the boot volume: $message' '$storage_ID'"
        exit -1
    fi
    log_debug $vm_ID "snapshot $snapshot_id created for boot volume $boot_volume"
    # 2. copy_clone the snapshot
    clone_ret=$(wds_curl PUT "api/v2/sync/block/snaps/$snapshot_id/copy_clone" "{\"name\":\"$image_name\", \"speed\": 32, \"phy_pool_id\": \"$wds_pool_id\"}")
    read -d'\n' -r task_id ret_code message < <(jq -r ".task_id, .ret_code, .message" <<<$clone_ret)
    if [ "$ret_code" != "0" ]; then
        log_debug $vm_ID "failed to clone snapshot $snapshot_id: $message"
        echo "|:-COMMAND-:| capture_image.sh '$img_ID' 'error' 'qcow2' 'failed to clone the snapshot: $message' '$storage_ID'"
        exit -1
    fi
    log_debug $vm_ID "clone task $task_id created for snapshot $snapshot_id"
    for i in {1..100}; do
         st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
	     [ "$st" = "TASK_COMPLETE" ] && state=uploaded && break
	     [ "$st" = "TASK_FAILED" ] && state=failed && break
	    sleep 5
    done
    # 3. delete the snapshot
    delete_ret=$(wds_curl DELETE "api/v2/sync/block/snaps/${snapshot_id}?force=false")
    read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$delete_ret)
    log_debug $vm_ID"delete snapshot $snapshot_id: $message"

    # 4. get the volume id from the image name
    volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$image_name" | jq -r '.volumes[0].id')
    [ -n "$volume_id" ] && state=available
fi
# virsh resume $vm_ID
echo "|:-COMMAND-:| capture_image.sh '$img_ID' '$state' 'qcow2' 'success' '$volume_id' '$storage_ID'"
