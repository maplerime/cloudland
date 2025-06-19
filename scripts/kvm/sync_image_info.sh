#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 2 ] && die "$0 <ID> <prefix>"

ID=$1
prefix=$2
image=image-$ID-$prefix

if [ -n "$wds_address" ]; then
    get_wds_token
    volumes=$(wds_curl GET "api/v2/sync/block/volumes?name=$image" | jq -c '.volumes')
    if echo "$volumes" | jq -e '.volumes | length > 0' >/dev/null 2>&1; then
        echo "$volumes" | jq -c '.volumes[]' | while read -r volume; do
            image_id=$(echo "$volume" | jq -r '.id')
            pool_id=$(echo "$volume" | jq -r '.pool_id')
            echo "|:-COMMAND-:| $(basename "$0") '$ID' '$image_id' '$pool_id'"
        done
    fi
fi
