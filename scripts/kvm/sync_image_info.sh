#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 2 ] && die "$0 <ID> <prefix> <pool_ID>"

ID=$1
prefix=$2
pool_ID=$4
image=image-$ID-$prefix

if [ -z "$pool_ID" ]; then
    pool_ID=$wds_pool_id
fi
if [ "$pool_ID" != "$wds_pool_id" ]; then
    pool_prefix=$(get_pool_prefix "$pool_ID")
    image=image-$pool_prefix
fi

state=error
if [ -n "$wds_address" ]; then
    get_wds_token
    volume_ID=$(wds_curl GET "api/v2/sync/block/volumes?name=$image" | jq -r '.volumes[0].id')
    if [ -z "$volume_ID" ]; then
        echo "|:-COMMAND-:| $(basename "$0") '$ID' '$pool_ID' '' '$state'"
        exit 1
    fi
    state=synced
    echo "|:-COMMAND-:| $(basename "$0") '$ID' '$pool_ID' '$volume_ID' '$state'"
fi