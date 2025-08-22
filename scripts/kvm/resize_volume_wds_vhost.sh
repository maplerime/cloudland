#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 5 ] && echo "$0 <volume_ID> <volume_UUID> <size> <booting> <vm_ID>" && exit -1

vol_ID=$1
wds_vol_ID=$2
vol_size=$3

get_wds_token
old_size=$(wds_curl GET "api/v2/sync/block/volumes/$wds_vol_ID" | jq -r '.volume_detail.volume_size')
if [ -z "$old_size" ]; then
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'error'"
    exit -1
fi
let new_size=$vol_size*1024*1024*1024

# new size must be larger than current size
if [ "$old_size" -ge "$new_size" ]; then
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'error'"
    exit -1
fi

# resize the volume
expand_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$wds_vol_ID/expand" "{\"size\": $new_size}")
ret_code=$(echo $expand_ret | jq -r .ret_code)
if [ "$ret_code" != "0" ]; then
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'error'"
    exit -1
else
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'success'"
fi
