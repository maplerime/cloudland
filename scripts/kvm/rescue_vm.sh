#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 9 ] && die "$0 <vm_ID> <image> <name> <cpu> <memory> <disk_size> <disk_id> <boot_loader> <instance_uuid>"

ID=$1
vm_ID=inst-$ID
img_name=$2
vm_name=$3
vm_cpu=$4
vm_mem=$5
disk_size=$6
disk_ID=$7
boot_loader=$8
instance_uuid=${9:-$ID}
state=error
vm_vnc=""
vol_state=error
snapshot=1
vm_rescue=$vm_ID-rescue

./action_vm.sh $ID stop
./action_vm.sh $ID hard_stop
md=$(cat)
metadata=$(echo $md | base64 -d)
./build_meta.sh "$vm_ID" "$vm_name-rescue" "true" <<< $md >/dev/null 2>&1

vm_meta=$cache_dir/meta/$vm_ID-rescue.iso
template=$template_dir/template_with_qa.xml
if [ "$boot_loader" = "uefi" ]; then
    template=$template_dir/template_uefi_with_qa.xml
fi
if [ -z "$wds_address" ]; then
    vm_img=$volume_dir/$vm_rescue.disk
    if [ ! -f "$vm_img" ]; then
        vm_img=$image_dir/$vm_rescue.disk
        if [ ! -s "$image_cache/$img_name" ]; then
            echo "Image is not available!"
            echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$SCI_CLIENT_ID' 'failed'"
            exit -1
        fi
        format=$(qemu-img info $image_cache/$img_name | grep 'file format' | cut -d' ' -f3)
        cmd="qemu-img convert -f $format -O qcow2 $image_cache/$img_name $vm_img"
        result=$(eval "$cmd")
        vol_state=attached
    fi
    disk_template=$template_dir/volume.xml
else
    get_wds_token
    image=$(basename $img_name .raw)
    vhost_name=instance-$ID-volume-rescue-$RANDOM
    snapshot_name=${image}-${snapshot}
    read -d'\n' -r snapshot_id volume_size <<< $(wds_curl GET "api/v2/sync/block/snaps?name=$snapshot_name" | jq -r '.snaps[0] | "\(.id) \(.snap_size)"')
    if [ -z "$snapshot_id" -o "$snapshot_id" = null ]; then
        image_volume_id=$(wds_curl GET "api/v2/sync/block/volumes?name=$image" | jq -r '.volumes[0].id')
        snapshot_ret=$(wds_curl POST "api/v2/sync/block/snaps" "{\"name\": \"$snapshot_name\", \"description\": \"$snapshot_name\", \"volume_id\": \"$image_volume_id\"}")
        read -d'\n' -r snapshot_id volume_size <<< $(wds_curl GET "api/v2/sync/block/snaps?name=$snapshot_name" | jq -r '.snaps[0] | "\(.id) \(.snap_size)"')
        if [ -z "$snapshot_id" -o "$snapshot_id" = null ]; then
            echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$SCI_CLIENT_ID' 'failed'"
            exit -1
        fi
        wds_curl DELETE "api/v2/sync/block/snaps/$image-$(($snapshot-1))?force=false"
    fi
    volume_ret=$(wds_curl POST "api/v2/sync/block/snaps/$snapshot_id/clone" "{\"name\": \"$vhost_name\"}")
    volume_id=$(echo $volume_ret | jq -r .id)
    if [ -z "$volume_id" -o "$volume_id" = null ]; then
        echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$SCI_CLIENT_ID' 'failed'"
        exit -1
    fi
    uss_id=$(get_uss_gateway)
    vhost_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
    vhost_id=$(echo $vhost_ret | jq -r .id)
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_id\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$wds_pool_id/$volume_id' 'failed to create wds vhost for boot volume, $vhost_ret, $uss_ret!'"
        echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$SCI_CLIENT_ID' 'failed'"
        exit -1
    fi
    vol_state=attached
    ux_sock=/var/run/wds/$vhost_name
    template=$template_dir/wds_template_with_qa.xml
    if [ "$boot_loader" = "uefi" ]; then
        template=$template_dir/wds_template_uefi_with_qa.xml
    fi
    disk_vhost=$(ls /var/run/wds/instance-$ID-volume-$disk_ID-*)
    disk_template=$template_dir/wds_volume.xml
fi

[ -z "$vm_mem" ] && vm_mem='1024m'
[ -z "$vm_cpu" ] && vm_cpu=1
let vm_mem=${vm_mem%[m|M]}*1024
vm_QA="$qemu_agent_dir/$vm_rescue.agent"
vm_xml=$xml_dir/$vm_ID/$vm_rescue.xml
cp $template $vm_xml
cpu_vendor=$(lscpu | grep "Vendor ID" | awk -F ':' '{print $2}' | tr -d ' ')
if [ "$cpu_vendor" = "GenuineIntel" ]; then
    vm_virt_feature="vmx"
else
    vm_virt_feature="svm"
fi
os_code=$(jq -r '.os_code' <<< $metadata)
sed -i "s/VM_ID/$vm_rescue/g; s/VM_MEM/$vm_mem/g; s/VM_CPU/$vm_cpu/g; s#VM_IMG#$vm_img#g; s#VM_UNIX_SOCK#$ux_sock#g; s#VM_META#$vm_meta#g; s#VM_AGENT#$vm_QA#g; s/VM_NESTED/disable/g; s/VM_VIRT_FEATURE/$vm_virt_feature/g; s/INSTANCE_UUID/$instance_uuid/g" $vm_xml
vm_nvram="$image_dir/${vm_rescue}_VARS.fd"
if [ "$boot_loader" = "uefi" ]; then
    cp $nvram_template $vm_nvram
    sed -i \
    -e "s/VM_ID/$vm_rescue/g" \
    -e "s/VM_MEM/$vm_mem/g" \
    -e "s/VM_CPU/$vm_cpu/g" \
    -e "s#VM_IMG#$vm_img#g" \
    -e "s#VM_UNIX_SOCK#$ux_sock#g" \
    -e "s#VM_META#$vm_meta#g" \
    -e "s#VM_AGENT#$vm_QA#g" \
    -e "s/VM_NESTED/$vm_nested/g" \
    -e "s/VM_VIRT_FEATURE/$vm_virt_feature/g" \
    -e "s#VM_BOOT_LOADER#$uefi_boot_loader#g" \
    -e "s#VM_NVRAM#$vm_nvram#g" \
    -e "s/INSTANCE_UUID/$instance_uuid/g" \
    $vm_xml
else
    sed -i \
    -e "s/VM_ID/$vm_rescue/g" \
    -e "s/VM_MEM/$vm_mem/g" \
    -e "s/VM_CPU/$vm_cpu/g" \
    -e "s#VM_IMG#$vm_img#g" \
    -e "s#VM_UNIX_SOCK#$ux_sock#g" \
    -e "s#VM_META#$vm_meta#g" \
    -e "s#VM_AGENT#$vm_QA#g" \
    -e "s/VM_NESTED/$vm_nested/g" \
    -e "s/VM_VIRT_FEATURE/$vm_virt_feature/g" \
    -e "s/INSTANCE_UUID/$instance_uuid/g" \
    $vm_xml
fi

virsh define $vm_xml
./generate_vm_instance_map.sh add $vm_ID

disk_xml=$xml_dir/$vm_ID/disk-${disk_ID}.xml
cp $disk_template $disk_xml
sed -i "s#VM_UNIX_SOCK#$disk_vhost#g;s#VOLUME_TARGET#vdb#g;" $disk_xml
virsh attach-device $vm_rescue $disk_xml --config --persistent

vlans=$(jq .vlans <<< $metadata)
nvlan=$(jq length <<< $vlans)
i=0
while [ $i -lt $nvlan ]; do
    read -d'\n' -r vlan mac < <(jq -r ".[$i].vlan, .[$i].mac_address" <<<$vlans)
    nic_name=tap$(echo $mac | cut -d: -f4- | tr -d :)
    interface_xml=$xml_dir/$vm_ID/$nic_name.xml
    if [ ! -f "$interface_xml" ]; then
        template=$template_dir/interface.xml
        cp $template $interface_xml
        sed -i "s/VM_MAC/$mac/g; s/VM_BRIDGE/br$vlan/g; s/VM_VTEP/$nic_name/g" $interface_xml
    fi
    virsh attach-device $vm_rescue $interface_xml --config --persistent
    let i=$i+1
done
virsh start $vm_rescue
[ $? -eq 0 ] && state=rescuing
echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$SCI_CLIENT_ID' 'sync'"

# check if the vm is windows and whether to change the rdp port
if [ "$os_code" = "windows" ]; then
    rdp_port=$(jq -r '.login_port' <<< $metadata)
    if [ -n "$rdp_port" ] && [ "${rdp_port}" != "3389" ]  && [ ${rdp_port} -gt 0 ]; then
        # run the script to change the rdp port in background
        async_exec ./async_job/win_rdp_port.sh $vm_ID $rdp_port
    fi
fi
