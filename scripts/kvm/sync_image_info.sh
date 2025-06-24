#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && die "$0 <ID> <prefix> <pool_ID> <storage_ID>"

ID=$1
prefix=$2
pool_ID=$3
storage_ID=$4
image=image-$ID-$prefix

if [ -z "$pool_ID" ]; then
    pool_ID=$wds_pool_id
fi
if [ "$pool_ID" != "$wds_pool_id" ]; then
    pool_prefix=$(get_uuid_prefix "$pool_ID")
    image=image-$pool_prefix
fi

state=error
if [ -n "$wds_address" ]; then
    get_wds_token
    volume_ID=$(wds_curl GET "api/v2/sync/block/volumes?name=$image" | jq -r '.volumes[0].id')
    if [ -z "$volume_ID" -o "$volume_ID" = null ]; then
        state=not_found
        echo "|:-COMMAND-:| $(basename "$0") '$storage_ID' '' '$state'"
        exit 1
    fi
    state=synced
    echo "|:-COMMAND-:| $(basename "$0") '$storage_ID' '$volume_ID' '$state'"
fi