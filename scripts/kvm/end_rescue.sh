#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID>"

ID=$1
vm_ID=inst-$ID-rescue
vm_xml=$(virsh dumpxml $vm_ID)
virsh undefine $vm_ID
cmd="virsh destroy $vm_ID"
result=$(eval "$cmd")

if [ -z "$wds_address" ]; then	
    rm -f ${image_dir}/${vm_ID}.*
else
    get_wds_token
    vhosts=$(basename $(ls /var/run/wds/instance-${ID}-rescue*))
    for vhost_name in $vhosts; do
        if [ -S "/var/run/wds/$vhost_name" ]; then
           vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')
           uss_id=$(get_uss_gateway)
           wds_curl PUT "api/v2/sync/block/vhost/unbind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"is_snapshot\": false}"
           wds_curl DELETE "api/v2/sync/block/vhost/$vhost_id"
        fi
    done
    if [ -n "$rescue_volume" ]; then
        wds_curl DELETE "api/v2/sync/block/volumes/$rescue_volume?force=false"
    fi
fi
echo "|:-COMMAND-:| $(basename $0) '$ID'"
