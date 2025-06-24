#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && die "$0 <ID> <prefix> <target_pool_ID> <storage_ID>"

ID=$1
prefix=$2
target_pool_ID=$3
storage_ID=$4
pool_prefix=$(get_pool_prefix "$target_pool_ID")
source_image=image-$ID-$prefix
target_image=$source_image-$pool_prefix
state=error

# if from is volume, copy clone by volume id
source_volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$source_image" | jq -r '.volumes[0].id')
clone_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$source_volume_id/copy_clone" "{\"name\":\"$target_image\", \"speed\": 32, \"phy_pool_id\": \"$target_pool_ID\"}")

read -d'\n' -r task_id ret_code < <(jq -r ".task_id .ret_code" <<< $clone_ret)
[ "$ret_code" != "0" ] && echo "|:-COMMAND-:| sync_image_info.sh '$storage_ID' '' '$state'" && exit -1
state=cloning
for i in {1..100}; do
    st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
    [ "$st" = "TASK_COMPLETE" ] && state=uploaded && break
    [ "$st" = "TASK_FAILED" ] && state=error && break
    sleep 5
done

volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$target_image" | jq -r '.volumes[0].id')
[ -n "$volume_id" ] && state=synced
echo "|:-COMMAND-:| sync_image_info.sh '$storage_ID' '$volume_id' '$state'"