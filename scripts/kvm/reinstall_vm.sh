#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 10 ] && die "$0 <vm_ID> <image> <snapshot> <volume_id> <pool_id> <old_volume_uuid> <cpu> <memory> <disk_size> <hostname>"

ID=$1
vm_ID=inst-$ID
img_name=$2
snapshot=$3
vol_ID=$4
pool_ID=$5
old_volume_id=$6
vm_cpu=$7
vm_mem=$8
disk_size=$9
vm_name=${10}
state=error
vol_state=error

md=$(cat)
metadata=$(echo $md | base64 -d)

vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
mv $vm_xml $vm_xml-$(date +'%s.%N')
virsh dumpxml $vm_ID >$vm_xml
virsh undefine $vm_ID
if [ $? -ne 0 ]; then
    virsh undefine --nvram $vm_ID
fi
virsh destroy $vm_ID
let fsize=$disk_size*1024*1024*1024

# rebuild metadata
./build_meta.sh "$vm_ID" "$vm_name" <<< $md >/dev/null 2>&1

if [ -z "$wds_address" ]; then
    vm_img=$volume_dir/$vm_ID.disk
    if [ ! -f "$vm_img" ]; then
        vm_img=$image_dir/$vm_ID.disk
        if [ ! -s "$image_cache/$img_name" ]; then
            echo "Image is not available!"
            echo "|:-COMMAND-:| create_volume_local '$vol_ID' 'volume-${vol_ID}.disk' '$vol_state' 'image $img_name not available!'"
            exit -1
        fi
        format=$(qemu-img info $image_cache/$img_name | grep 'file format' | cut -d' ' -f3)
        cmd="qemu-img convert -f $format -O qcow2 $image_cache/$img_name $vm_img"
        result=$(eval "$cmd")
        vsize=$(qemu-img info $vm_img | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')
        if [ "$vsize" -gt "$fsize" ]; then
            echo "|:-COMMAND-:| create_volume_local '$vol_ID' 'volume-${vol_ID}.disk' '$vol_state' 'flavor is smaller than image size'"
            exit -1
        fi
        qemu-img resize -q $vm_img "${disk_size}G" &> /dev/null
    fi
    vol_state=attached
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' 'volume-${vol_ID}.disk' '$vol_state' 'success'"
else
    get_wds_token
    image=$(basename $img_name .raw)
    old_vhost_name=$(basename $(ls /var/run/wds/instance-$ID-volume-$vol_ID-*))
    vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$old_vhost_name" | jq -r '.vhosts[0].id')
    uss_id=$(get_uss_gateway)
    wds_curl PUT "api/v2/sync/block/vhost/unbind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"is_snapshot\": false}"
    wds_curl DELETE "api/v2/sync/block/vhost/$vhost_id"
    wds_curl DELETE "api/v2/sync/block/volumes/$old_volume_id?force=true"
    if [ -z "$pool_ID" ]; then
        pool_ID=$wds_pool_id
    fi
    if [ "$pool_ID" != "$wds_pool_id" ]; then
        pool_prefix=$(get_uuid_prefix "$pool_ID")
        image=${image}-${pool_prefix}
    fi
    snapshot_name=${image}-${snapshot}
    read -d'\n' -r snapshot_id volume_size <<< $(wds_curl GET "api/v2/sync/block/snaps?name=$snapshot_name" | jq -r '.snaps[0] | "\(.id) \(.snap_size)"')
    if [ -z "$snapshot_id" -o "$snapshot_id" = null ]; then
	    image_volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$image" | jq -r '.volumes[0].id')
	    snapshot_ret=$(wds_curl POST "api/v2/sync/block/snaps" "{\"name\": \"$snapshot_name\", \"description\": \"$snapshot_name\", \"volume_id\": \"$image_volume_id\"}")
        read -d'\n' -r snapshot_id volume_size <<< $(wds_curl GET "api/v2/sync/block/snaps?name=$snapshot_name" | jq -r '.snaps[0] | "\(.id) \(.snap_size)"')
        if [ -z "$snapshot_id" -o "$snapshot_id" = null ]; then
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'failed to create image snapshot, $snapshot_ret'"
            exit -1
        fi
        wds_curl DELETE "api/v2/sync/block/snaps/$image-$(($snapshot-1))?force=false"
    fi

    for i in {1..10}; do
        vhost_name=instance-$ID-volume-$vol_ID-$RANDOM
	      [ "$vhost_name" != "$old_vhost_name" ] && break
    done
    volume_ret=$(wds_curl POST "api/v2/sync/block/snaps/$snapshot_id/clone" "{\"name\": \"$vhost_name\"}")
    volume_id=$(echo $volume_ret | jq -r .id)
    if [ -z "$volume_id" -o "$volume_id" = null ]; then
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'failed to create boot volume based on snapshot $snapshot_name, $volume_ret!'"
        exit -1
    fi
    if [ "$fsize" -gt "$volume_size" ]; then
        expand_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_id/expand" "{\"size\": $fsize}")
        ret_code=$(echo $expand_ret | jq -r .ret_code)
        if [ "$ret_code" != "0" ]; then
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$pool_ID/$volume_id' 'failed to expand boot volume to size $fsize, $expand_ret'"
            exit -1
        fi
    fi
    vhost_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
    vhost_id=$(echo $vhost_ret | jq -r .id)
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_id\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$pool_ID/$volume_id' 'failed to create wds vhost for boot volume, $vhost_ret, $uss_ret!'"
        exit -1
    fi
    vol_state=attached
    echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$pool_ID/$volume_id' 'success'"
fi

[ -z "$vm_mem" ] && vm_mem='1024m'
[ -z "$vm_cpu" ] && vm_cpu=1
let vm_mem=${vm_mem%[m|M]}*1024
sed_cmd="s#>.*</memory>#>$vm_mem</memory>#g; s#>.*</currentMemory>#>$vm_mem</currentMemory>#g; s#>.*</vcpu>#>$vm_cpu</vcpu>#g"
if [ -n "$wds_address" ]; then
  sed_cmd="$sed_cmd; s#$old_vhost_name#$vhost_name#g"
fi
sed -i "$sed_cmd" $vm_xml
virsh define $vm_xml
virsh autostart $vm_ID
virsh start $vm_ID
[ $? -eq 0 ] && state=running
echo "|:-COMMAND-:| launch_vm.sh '$ID' '$state' '$SCI_CLIENT_ID' 'sync'"

# check if the vm is windows and whether to change the rdp port
os_code=$(jq -r '.os_code' <<< $metadata)
if [ "$os_code" = "windows" ]; then
    rdp_port=$(jq -r '.login_port' <<< $metadata)
    if [ -n "$rdp_port" ] && [ "${rdp_port}" != "3389" ]  && [ ${rdp_port} -gt 0 ]; then
        # run the script to change the rdp port in background
        async_exec ./async_job/win_rdp_port.sh $vm_ID $rdp_port
    fi
fi
