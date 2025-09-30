#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 4 ] && echo "$0 <vm_ID> <volume_ID> <path> <wds_volume_id>" && exit -1

ID=$1
vm_ID=inst-$1
vol_ID=$2
vol_PATH=$3
wds_volume_id=$4

get_wds_token

# check existing vhost and uss binding first, if exists remove them
vhost_str=$(wds_curl GET /api/v2/block/volumes/$wds_volume_id/vhost)
count=$(echo $vhost_str | jq -r '.count')
ret_code=$(echo $vhost_str | jq -r '.ret_code')
vhost_id=$(echo $vhost_str | jq -r '.vhosts[0].id')
if [ "$ret_code" == "0" ] && [ "$count" -gt 0 ]; then
    if [ "$vhost_id" != "" ]; then
        log_debug $ID "Found existing vhost($vhost_id) for volume($vol_ID), proceeding to unbind and delete"
        # query /api/v2/sync/block/vhost/{vhost_id}/vhost_binded_uss get binded uss_id
        uss_id=$(wds_curl GET "api/v2/sync/block/vhost/$vhost_id/vhost_binded_uss" | jq -r '.uss[0].id')
        if [ -n "$uss_id" ]; then
            # unbind existing vhost from uss
            delete_vhost $vol_ID $vhost_id $uss_id
        else
            delete_vhost $vol_ID $vhost_id
        fi
    else
        log_debug $ID "No existing vhost found for volume($vol_ID)"
    fi
fi

count=$(virsh dumpxml $vm_ID | grep -c "<disk type='vhostuser' device='disk'")
let letter=97+$count
vhost_name=instance-$ID-vol-$vol_ID
uss_id=$(get_uss_gateway)
vhost_id=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}" | jq -r .id)
ret_code=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$wds_volume_id\", \"is_snapshot\": false}" | jq -r .ret_code)
if [ "$ret_code" != "0" ]; then
    wds_curl DELETE "api/v2/sync/block/vhost/$vhost_id"
    echo "|:-COMMAND-:| $(basename $0) '' '$vol_ID' ''"
    exit -1
fi
ux_sock=/var/run/wds/$vhost_name

vol_xml=$xml_dir/$vm_ID/disk-${vol_ID}.xml
cp $template_dir/wds_volume.xml $vol_xml
device=vd$(printf "\\$(printf '%03o' "$letter")")

# dumpxml instance xml to find cpu count
cpu_count=$(virsh dumpxml $vm_ID | grep "<vcpu" | sed -e 's/.*<vcpu[^>]*>\([0-9]*\)<\/vcpu>.*/\1/')
vhost_queue_num=1
if [ "$cpu_count" -gt 2 ]; then
    vhost_queue_num=2
fi
sed -i "s#VM_UNIX_SOCK#$ux_sock#g;s#VOLUME_TARGET#$device#g;s/VHOST_QUEUE_NUM/$vhost_queue_num/g" $vol_xml

virsh attach-device $vm_ID $vol_xml --config --persistent
if [ $? -eq 0 ]; then
    echo "|:-COMMAND-:| $(basename $0) '$1' '$vol_ID' '$device'"
else
    delete_vhost $vol_ID $vhost_id $uss_id
    echo "|:-COMMAND-:| $(basename $0) '' '$vol_ID' ''"
fi
vm_xml=$xml_dir/$vm_ID/$vm_ID.xml
virsh dumpxml --security-info $vm_ID 2>/dev/null | sed "s/autoport='yes'/autoport='no'/g" > $vm_xml.dump && mv -f $vm_xml.dump $vm_xml
