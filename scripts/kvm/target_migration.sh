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

# Storage-type consistency: pool_ID empty => local-disk VM; wds_address empty => local-disk node.
# Reject cross-storage migration (local<->WDS) early.
if [ -z "$pool_ID" ] && [ -n "$wds_address" ]; then
    log_debug $ID "target_migration.sh: local-disk VM targeted at a WDS node, not supported"
    state="not_supported"
    echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'cannot migrate local-disk VM to a WDS node'"
    exit 0
fi
if [ -n "$pool_ID" ] && [ -z "$wds_address" ]; then
    log_debug $ID "target_migration.sh: WDS VM targeted at a local-disk node, not supported"
    state="not_supported"
    echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'cannot migrate WDS VM to a local-disk node'"
    exit 0
fi

md=$(cat)
metadata=$(echo $md | base64 -d)
mkdir -p $xml_dir/$vm_ID
vhost_queue_num=1
if [ "$vm_cpu" -gt 2 ]; then
    vhost_queue_num=2
fi

let fsize=$disk_size*1024*1024*1024
./build_meta.sh "$vm_ID" "$vm_name" <<< $md >/dev/null 2>&1
vm_meta=$cache_dir/meta/$vm_ID.iso

# ---- Local-disk VM on a local-disk node (cross-storage already rejected) ----
# Disks + domain XML arrive via scp from the source (source_migration.sh); the
# meta ISO was just regenerated above. Here we only build the L2 bridges the
# VM's NICs attach to (must exist before the VM starts; create_link.sh is
# idempotent). The VM is defined+started later in complete_migration.sh.
if [ -z "$wds_address" ]; then
    log_debug $ID "target_migration.sh: local-disk target prep, building bridges"
    # Loop over each vlan to ensure its bridge + vxlan exist on this node.
    for vlan in $(jq -r '.vlans[].vlan' <<<$metadata); do
        ./create_link.sh $vlan
    done
    state="target_prepared"
    log_debug $ID "target_migration.sh: local-disk target prepared, state=$state"
    echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' ''"
    exit 0
fi

get_wds_token
volumes=$(jq -r .volumes <<< $metadata)
if [ "$migration_type" = "cold" ]; then
    ./blacklist_hyper_vhost.sh $ID $source_hyper <<< $volumes
    if [ $? -ne 0 ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'failed to put vhost into blacklist'"
        exit 1
    fi
fi
nvolume=$(jq length <<< $volumes)
uss_id=$(get_uss_gateway)
i=0
while [ $i -lt $nvolume ]; do
    read -d'\n' -r vol_ID volume_id device booting < <(jq -r ".[$i].id, .[$i].uuid, .[$i].device, .[$i].booting" <<<$volumes)
    if [ "$booting" = "true" ]; then
        vhost_name=$(wds_curl GET "api/v2/sync/block/volumes/$volume_id" | jq -r .volume_detail.name)
    else
        vhost_name=instance-$ID-vol-$vol_ID
    fi
    ux_sock=/var/run/wds/$vhost_name
    vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')
    wds_curl PUT "api/v2/sync/block/vhost/unbind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"is_snapshot\": false}"
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_id\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'failed to bind uss for vhost'"
	exit 1
    fi
    if [ "$booting" = "true" ]; then
        boot_ux_sock=$ux_sock
    else
        vol_xml=$xml_dir/$vm_ID/disk-${vol_ID}.xml
        cp $template_dir/wds_volume.xml $vol_xml
        sed -i "s#VM_UNIX_SOCK#$ux_sock#g;s#VOLUME_TARGET#$device#g;s#VHOST_QUEUE_NUM#$vhost_queue_num#g" $vol_xml
    fi
    let i=$i+1
done

[ -z "$vm_mem" ] && vm_mem='1024m'
[ -z "$vm_cpu" ] && vm_cpu=1
let vm_mem=${vm_mem%[m|M]}*1024
vm_nested="disable"
[ $vm_cpu -ge 12 ] && vm_nested="require"
cpu_vendor=$(lscpu | grep "Vendor ID" | awk -F ':' '{print $2}' | tr -d ' ')
if [ "$cpu_vendor" = "GenuineIntel" ]; then
    vm_virt_feature="vmx"
else
    vm_virt_feature="svm"
fi
vm_QA="$qemu_agent_dir/$vm_ID.agent"
vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
template=$template_dir/wds_template_with_qa.xml
if [ "$boot_loader" = "uefi" ]; then
    template=$template_dir/wds_template_uefi_with_qa.xml
fi
cp $template $vm_xml
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
    virsh autostart $vm_ID --disable
    for vol_xml in $xml_dir/$vm_ID/disk-*.xml; do
        virsh attach-device $vm_ID $vol_xml --config --persistent
    done
fi
os_code=$(jq -r '.os_code' <<< $metadata)
jq .vlans <<< $metadata | ./sync_nic_info.sh "$ID" "$vm_name" "$os_code"
state="target_prepared"
if [ "$migration_type" = "cold" ]; then
    virsh start $vm_ID
    if [ $? -ne 0 ]; then
        state="failed"
        virsh undefine --nvram $vm_ID
        rm -f ${cache_dir}/meta/${vm_ID}.iso
        rm -rf $xml_dir/$vm_ID
    fi
fi

# Update vm_instance_map metrics - add VM to target hypervisor
if [ "$state" != "failed" ]; then
    echo "Updating vm_instance_map metrics: adding VM $vm_ID to target hypervisor"
    ./generate_vm_instance_map.sh add $vm_ID
fi

echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' ''"
