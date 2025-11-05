#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 4 ] && die "$0 <ID> <prefix> <url> <storage_ID>"

ID=$1
prefix=$2
url=$3
storage_ID=$4
image_name=image-$ID-$prefix
state=error
mkdir -p $image_cache
image=$image_cache/$image_name
inet_access curl -s -k $url -o $image

if [ ! -s "$image" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$format' '0' 'null' '$storage_ID'"
    exit -1
fi
format=$(qemu-img info $image | grep 'file format' | cut -d' ' -f3)
[ "$format" = "qcow2" -o "$format" = "raw" ] && state=downloaded
image_size=$(qemu-img info ${image} | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')

if [ -z "$wds_address" ]; then
    mv $image ${image}.$format
    state=available
    #sync_target /opt/cloudland/cache/image
    volume_id=""
else
    get_wds_token
    qemu-img convert -f $format -O raw ${image} ${image}.raw
    format=raw
    uss_id=$(get_uss_gateway)
    uss_service=$(systemctl -a | grep uss | awk '{print $1}')
    if [ -n "$uss_service" ]; then
        cat /etc/systemd/system/$uss_service | grep cloudland
        if [ $? -ne 0 ]; then
            wds_curl PUT "api/v2/sync/wds/uss/$uss_id" '{"action":"add","mount_path":"/opt/cloudland/cache/image"}'
            systemctl restart $uss_service
        fi
    else
        docker ps | grep USS | awk '{print $1}' | xargs docker inspect | grep cloudland
        if [ $? -ne 0 ]; then
            wds_curl PUT "api/v2/sync/wds/uss/$uss_id" '{"action":"add","mount_path":"/opt/cloudland/cache/image"}'
            sleep 60
        fi
    fi
    for i in {1..5}; do
        task_ret=$(wds_curl "PUT" "api/v2/sync/block/volumes/import" "{\"volname\": \"$image_name\", \"path\": \"${image}.raw\", \"ussid\": \"$uss_id\", \"start_blockid\": 0, \"volsize\": $image_size, \"poolid\": \"$wds_pool_id\", \"num_block\": 0, \"speed\": 8}")
        task_id=$(jq -r .task_id <<<$task_ret)
        state=uploading
        echo $task_ret >>$log_dir/image_upload.log
        [ -z "$task_id" -o "$task_id" = null ] && continue
        for j in {1..1000}; do
            st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
            [ "$st" = "TASK_COMPLETE" ] && state=uploaded && break
            [ "$st" = "TASK_FAILED" ] && state=failed && break
            sleep 5
        done
        volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$image_name" | jq -r '.volumes[0].id')
        [ -n "$volume_id" -a "$state" = "uploaded" ] && state=available && break
    done
fi
rm -f ${image}
echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$format' '$image_size' '$volume_id' '$storage_ID'"
