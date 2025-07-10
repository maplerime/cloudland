#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID>"

ID=$1
vm_ID=inst-$ID
vm_rescue=$vm_ID-rescue
vm_xml=$(virsh dumpxml $vm_rescue)
virsh undefine $vm_rescue
cmd="virsh destroy $vm_rescue"
result=$(eval "$cmd")
rm -f $xml_dir/$vm_ID/*rescue*

virsh start $vm_ID

if [ -z "$wds_address" ]; then	
    rm -f ${image_dir}/${vm_rescue}.*
else
    get_wds_token
    vhosts=$(ls /var/run/wds/instance-${ID}-volume-rescue*)
    for vhost in $vhosts; do
	vhost_name=$(basename $vhost)
        if [ -S "/var/run/wds/$vhost_name" ]; then
            vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')
            uss_id=$(get_uss_gateway)
            wds_curl PUT "api/v2/sync/block/vhost/unbind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"is_snapshot\": false}"
            wds_curl DELETE "api/v2/sync/block/vhost/$vhost_id"
        fi
	volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$vhost_name" | jq -r .volumes[0].id)
        wds_curl DELETE "api/v2/sync/block/volumes/$volume_id?force=false"
    done
fi
