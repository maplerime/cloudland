#!/bin/bash

cd $(dirname $0)
source ../cloudrc

# cg_ID, wds_cg_id
[ $# -lt 2 ] && echo "$0 <cg_ID> <wds_cg_id>" && exit -1

cg_ID=$1
wds_cg_id=$2

state='error'

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_cg_id" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'wds_cg_id is not set'"
    exit -1
fi

get_wds_token

# Delete consistency group
# 删除一致性组
result=$(wds_curl "DELETE" "api/v2/sync/block/consistency_groups/$wds_cg_id")
ret_code=$(echo $result | jq -r .ret_code)
message=$(echo $result | jq -r .message)

if [ "$ret_code" != "0" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$cg_ID' 'error' 'failed to delete consistency group: $message'"
    exit -1
fi

state='deleted'
echo "|:-COMMAND-:| $(basename $0) '$cg_ID' '$state' 'success'"
