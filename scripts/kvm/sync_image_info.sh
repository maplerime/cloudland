#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 2 ] && die "$0 <ID> <prefix>"

ID=$1
prefix=$2
image=image-$ID-$prefix

if [ -n "$wds_address" ]; then
    get_wds_token
    volumes=$(wds_curl GET "api/v2/sync/block/volumes?name=$image" | jq -c '.volumes')

    if echo "$volumes" | jq -e 'length > 0' >/dev/null 2>&1; then
        pairs=""
        while read -r volume; do
            volume_id=$(echo "$volume" | jq -r '.id')
            pool_id=$(echo "$volume" | jq -r '.pool_id')
            pairs+="${pool_id},${volume_id};"
        done < <(echo "$volumes" | jq -c '.[]')

        # Remove the trailing semicolon
        pairs=${pairs%;}
        echo "|:-COMMAND-:| $(basename "$0") '$ID' '$pairs'"
    fi
fi