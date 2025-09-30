#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 12 ] && die "$0 <migrate_ID> <task_ID> <vm_ID> <name> <cpu> <memory> <disk_size> <source_hyper> <migration_type> <boot_loader> <pool_ID> <instance_uuid>"

migrate_ID=$1
task_ID=$2
ID=$3
vm_ID=inst-$ID
vm_name=$4
vm_cpu=$5
vm_mem=$6
disk_size=$7
source_hyper=$8
migration_type=$9
boot_loader=${10}
pool_ID=${11}
instance_uuid=${12:-$ID}
state="failed"

if [ -z "$wds_address" ]; then
    state="not_supported"
    echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
    exit 0
fi

md=$(cat)
metadata=$(echo $md | base64 -d)

let fsize=$disk_size*1024*1024*1024
./build_meta.sh "$vm_ID" "$vm_name" <<< $md >/dev/null 2>&1
vm_meta=$cache_dir/meta/$vm_ID.iso
get_wds_token
volumes=$(jq -r .volumes <<< $metadata)
nvolume=$(jq length <<< $volumes)
uss_id=$(get_uss_gateway)
business_network=$(wds_curl GET "api/v2/wds/uss/$uss_id" | jq -r .business_network)
business_network=${business_network%/*}
i=0
while [ $i -lt $nvolume ]; do
    read -d'\n' -r vol_ID volume_id device booting < <(jq -r ".[$i].id, .[$i].uuid, .[$i].device, .[$i].booting" <<<$volumes)
    vhost_name=$(wds_curl GET "api/v2/sync/block/volumes/$volume_id" | jq -r .volume_detail.name)
    vhost_paths=$(wds_curl GET "api/v2/sync/block/volumes/$volume_id/bind_status" | jq -r .path)
    npaths=$(jq length <<< $vhost_paths)
    j=0
    while [ $j -lt $npaths ]; do
	vhost_path=$(jq -r .[$j] <<<$vhost_paths)
	if [ "${vhost_path/$business_network:/}" == "$vhost_path" ]; then
            ret_code=$(wds_curl PUT "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}" | jq -r .ret_code)
            if [ "$ret_code" != "0" ]; then
                echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
	        exit 1
            fi
	fi
        let j=$j+1
    done
    ux_sock=/var/run/wds/$vhost_name
    vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')
    wds_curl PUT "api/v2/sync/block/vhost/unbind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"is_snapshot\": false}"
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_id\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
	exit 1
    fi
    if [ "$booting" == "true" ]; then
        boot_ux_sock=$ux_sock
    else
        vol_xml=$xml_dir/$vm_ID/disk-${vol_ID}.xml
        cp $template_dir/wds_volume.xml $vol_xml
        sed -i "s#VM_UNIX_SOCK#$ux_sock#g;s#VOLUME_TARGET#$device#g;" $vol_xml
    fi
    let i=$i+1
done

[ -z "$vm_mem" ] && vm_mem='1024m'
[ -z "$vm_cpu" ] && vm_cpu=1
let vm_mem=${vm_mem%[m|M]}*1024
vm_nested="disable"
cpu_vendor=$(lscpu | grep "Vendor ID" | awk -F ':' '{print $2}' | tr -d ' ')
if [ "$cpu_vendor" = "GenuineIntel" ]; then
    vm_virt_feature="vmx"
else
    vm_virt_feature="svm"
fi
mkdir -p $xml_dir/$vm_ID
vm_QA="$qemu_agent_dir/$vm_ID.agent"
vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
template=$template_dir/wds_template_with_qa.xml
cp $template $vm_xml
vhost_queue_num=1
if [ "$vm_cpu" -gt 2 ]; then
    vhost_queue_num=2
fi
sed -i "s/VM_ID/$vm_ID/g; s/VM_MEM/$vm_mem/g; s/VM_CPU/$vm_cpu/g; s#VM_UNIX_SOCK#$boot_ux_sock#g; s#VM_META#$vm_meta#g; s#VM_AGENT#$vm_QA#g; s/INSTANCE_UUID/$instance_uuid/g; s/VM_NESTED/$vm_nested/g; s/VM_VIRT_FEATURE/$vm_virt_feature/g" $vm_xml
vm_nvram="$image_dir/${vm_ID}_VARS.fd"
if [ "$boot_loader" = "uefi" ]; then
    cp $nvram_template $vm_nvram
    sed -i \
    -e "s/VM_ID/$vm_ID/g" \
    -e "s/VM_MEM/$vm_mem/g" \
    -e "s/VM_CPU/$vm_cpu/g" \
    -e "s/VHOST_QUEUE_NUM/$vhost_queue_num/g" \
    -e "s#VM_IMG#$vm_img#g" \
    -e "s#VM_UNIX_SOCK#$boot_ux_sock#g" \
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
    -e "s/VM_ID/$vm_ID/g" \
    -e "s/VM_MEM/$vm_mem/g" \
    -e "s/VM_CPU/$vm_cpu/g" \
    -e "s/VHOST_QUEUE_NUM/$vhost_queue_num/g" \
    -e "s#VM_IMG#$vm_img#g" \
    -e "s#VM_UNIX_SOCK#$boot_ux_sock#g" \
    -e "s#VM_META#$vm_meta#g" \
    -e "s#VM_AGENT#$vm_QA#g" \
    -e "s/VM_NESTED/$vm_nested/g" \
    -e "s/VM_VIRT_FEATURE/$vm_virt_feature/g" \
    -e "s/INSTANCE_UUID/$instance_uuid/g" \
    $vm_xml
fi

if [ "$migration_type" = "cold" ]; then
    virsh define $vm_xml
    virsh autostart $vm_ID
    for vol_xml in $xml_dir/$vm_ID/disk-*.xml; do
        virsh attach-device $vm_ID $vol_xml --config --persistent
    done
fi
os_code=$(jq -r '.os_code' <<< $metadata)
jq .vlans <<< $metadata | ./sync_nic_info.sh "$ID" "$vm_name" "$os_code"
state="target_prepared"
if [ "$migration_type" = "cold" ]; then
    virsh start $vm_ID
    [ $? -ne 0 ] && state="failed"
fi
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
async_exec ./async_job/complete_migration.sh "$migrate_ID" "$task_ID" "$ID" "$source_hyper"
