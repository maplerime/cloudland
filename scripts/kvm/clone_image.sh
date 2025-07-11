#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 4 ] && die "$0 <ID> <prefix> <target_pool_ID> <storage_ID>"

ID=$1
prefix=$2
target_pool_ID=$3
storage_ID=$4
pool_prefix=$(get_uuid_prefix "$target_pool_ID")
source_image=image-$ID-$prefix
target_image=$source_image-$pool_prefix
state=error

# now, maybe we have not default pool volume id
# so, we need use source image name to get source volume id
source_volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$source_image" | jq -r '.volumes[0].id')
if [ -z "$source_volume_id" -o "$source_volume_id" = null ]; then
    log_debug $ID "source volume $source_image not found"
    echo "|:-COMMAND-:| sync_image_info.sh '$storage_ID' '' '$state'"
    exit -1
fi

# 1. take the snapshot of the boot volume
snapshot_ret=$(wds_curl POST "api/v2/sync/block/snaps/" "{\"description\":\"snapshot for image $source_image\", \"name\":\"$source_image\", \"volume_id\":\"$source_volume_id\"}")
read -d'\n' -r snapshot_id ret_code message < <(jq -r ".id, .ret_code, .message" <<<$snapshot_ret)
if [ "$ret_code" != "0" ]; then
    log_debug $ID "failed to create snapshot for source volume $source_volume_id: $message"
    echo "|:-COMMAND-:| sync_image_info.sh '$storage_ID' '' '$state'"
    exit -1
fi
log_debug $ID "snapshot $snapshot_id created for source volume $source_volume_id"

# 2. copy_clone the snapshot
clone_ret=$(wds_curl PUT "api/v2/sync/block/snaps/$snapshot_id/copy_clone" "{\"name\":\"$target_image\", \"speed\": 32, \"phy_pool_id\": \"$target_pool_ID\"}")
read -d'\n' -r task_id ret_code message < <(jq -r ".task_id, .ret_code, .message" <<<$clone_ret)
if [ "$ret_code" != "0" ]; then
    log_debug $ID "failed to clone snapshot $snapshot_id: $message"
    echo "|:-COMMAND-:| sync_image_info.sh '$storage_ID' '' '$state'"
    exit -1
fi
log_debug $ID "clone task $task_id created for snapshot $snapshot_id"
for i in {1..100}; do
    st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
    [ "$st" = "TASK_COMPLETE" ] && state=uploaded && break
    [ "$st" = "TASK_FAILED" ] && state=error && break
    sleep 5
done

# 3. delete the snapshot
delete_ret=$(wds_curl DELETE "api/v2/sync/block/snaps/${snapshot_id}?force=true")
read -d'\n' -r ret_code message < <(jq -r ".ret_code, .message" <<<$delete_ret)
log_debug $ID "delete snapshot $snapshot_id: $message"

volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$target_image" | jq -r '.volumes[0].id')
[ -n "$volume_id" ] && state=synced
echo "|:-COMMAND-:| sync_image_info.sh '$storage_ID' '$volume_id' '$state'"