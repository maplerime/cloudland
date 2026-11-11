#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 13 ] && die "$0 <instance_id> <image> <image_volume_id> <hostname> <cpu> <memory> <disk_size> <volume_id> <nested_enable> <boot_loader> <instance_uuid> <pool_id> <old_wds_volume_id>"

ID=$1
vm_ID=inst-$ID
img_name=$2
image_volume_id=$3
vm_name=$4
vm_cpu=$5
vm_mem=$6
disk_size=$7
vol_ID=$8
nested_enable=$9
boot_loader=${10}
instance_uuid=${11}
pool_ID=${12}
old_wds_volume_id=${13}
snapshot=""
volume_path=""
if [ -z "$wds_address" ]; then
    volume_path=local://${vm_ID}.disk
fi

md=$(cat)
metadata=$(echo $md | base64 -d)
read -d'\n' -r sysdisk_iops_limit sysdisk_bps_limit < <(jq -r ".disk_iops_limit, .disk_bps_limit" <<<$metadata)

vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
mv $vm_xml $vm_xml-$(date +'%s.%N')

# Stop and remove the old domain before rebuilding it.
virsh destroy "$vm_ID"
virsh undefine --nvram "$vm_ID"

# virsh destroy returns before QEMU has fully exited; the WDS vhost-user connection is held by the
# QEMU process and is only released once that process is gone. Unbinding too early makes WDS fail
# with "Device or resource busy (-16)". Wait until the domain is really stopped AND the QEMU
# process is gone before tearing down any vhost. Best-effort: on timeout we continue anyway (a
# failed teardown only leaves reclaimable zombies, it never breaks the reinstall).
confirmed=0
for i in $(seq 1 30); do
    dom_state=$(virsh domstate $vm_ID 2>/dev/null)
    # The trailing comma matches `-name guest=inst-<ID>,debug-threads=on` and avoids matching a
    # longer id (inst-378 vs inst-3789). If this libvirt/QEMU does not use the guest= form, pgrep
    # simply never matches and domstate alone gates the wait.
    if { [ -z "$dom_state" ] || [ "$dom_state" = "shut off" ]; } && ! pgrep -f "guest=$vm_ID," >/dev/null 2>&1; then
        confirmed=1
        break
    fi
    sleep 1
done
[ "$confirmed" -ne 1 ] && log_debug $vm_ID "domain not confirmed stopped after 30s, proceeding best-effort"
let fsize=$disk_size*1024*1024*1024

# rebuild metadata
./build_meta.sh "$vm_ID" "$vm_name" <<< $md >/dev/null 2>&1
vm_meta=$cache_dir/meta/$vm_ID.iso
template=$template_dir/template_with_qa.xml
if [ "$boot_loader" = "uefi" ]; then
    template=$template_dir/template_uefi_with_qa.xml
fi

if [ -z "$wds_address" ]; then
    vm_img=$volume_dir/$vm_ID.disk
    if [ ! -f "$vm_img" ]; then
        vm_img=$image_dir/$vm_ID.disk
        if [ ! -s "$image_cache/$img_name" ]; then
            echo "Image is not available!"
            echo "|:-COMMAND-:| create_volume_local '$SCI_CLIENT_ID' '$vol_ID' '$volume_path' 'reinstall_error' 'image $img_name is not available'"
            exit -1
        fi
        format=$(qemu-img info $image_cache/$img_name | grep 'file format' | cut -d' ' -f3)
        cmd="qemu-img convert -f $format -O qcow2 $image_cache/$img_name $vm_img"
        result=$(eval "$cmd")
        vsize=$(qemu-img info $vm_img | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')
        if [ "$vsize" -gt "$fsize" ]; then
            echo "|:-COMMAND-:| create_volume_local '$SCI_CLIENT_ID' '$vol_ID' '$volume_path' 'reinstall_error' 'flavor is smaller than image size'"
            exit -1
        fi
        qemu-img resize -q $vm_img "${disk_size}G" &> /dev/null
    fi
    echo "|:-COMMAND-:| create_volume_local '$SCI_CLIENT_ID' '$vol_ID' '$volume_path' 'attached' 'success'"
else
    get_wds_token
    image=$(basename $img_name .raw)
    uss_id=$(get_uss_gateway)
    # Remove the old boot vhost, then delete the old boot volume.
    if [ -n "$old_wds_volume_id" ]; then
        old_vhosts_ret=$(wds_curl GET "api/v2/block/volumes/$old_wds_volume_id/vhost" '{}')
        ret_code=$(jq -r '.ret_code' <<< "$old_vhosts_ret")
        if [ "$ret_code" = "0" ]; then
            old_vhost_id=$(jq -r '.vhosts[0].id' <<< "$old_vhosts_ret")
            if [ -n "$old_vhost_id" ] && [ "$old_vhost_id" != null ]; then
                delete_vhost "$old_wds_volume_id" "$old_vhost_id" "$uss_id"
            fi
        else
            log_debug "$ID" "failed to query old boot vhosts: $old_vhosts_ret"
        fi
        delete_ret=$(wds_curl DELETE "api/v2/sync/block/volumes/$old_wds_volume_id?force=false" '{}')
        log_debug "$ID" "delete old boot volume $old_wds_volume_id: $delete_ret"
    fi

    if [ -z "$pool_ID" ]; then
        pool_ID=$wds_pool_id
    fi
    if [ "$pool_ID" != "$wds_pool_id" ]; then
        pool_prefix=$(get_uuid_prefix "$pool_ID")
        image=${image}-${pool_prefix}
    fi
    if ! snapshot_ret=$(wds_get_image_snapshot "$image_volume_id" "$image"); then
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' 'reinstall_error' '$volume_path' 'failed to select image snapshot'"
        exit -1
    fi
    snapshot_id=$(jq -r '.snapshot_id' <<< "$snapshot_ret")
    snapshot=$(jq -r '.snapshot_sequence' <<< "$snapshot_ret")
    volume_size=$(jq -r '.snap_size' <<< "$snapshot_ret")
    vhost_name=instance-$ID-volume-$vol_ID-$RANDOM
    volume_ret=$(wds_curl POST "api/v2/sync/block/snaps/$snapshot_id/clone" "{\"name\": \"$vhost_name\"}")
    volume_id=$(echo $volume_ret | jq -r .id)
    if [ -z "$volume_id" -o "$volume_id" = null ]; then
        log_debug "$ID" "failed to clone boot volume: $volume_ret"
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' 'reinstall_error' '$volume_path' 'failed to clone boot volume'"
        exit -1
    fi
    volume_path=wds_vhost://$pool_ID/$volume_id
    if [ "$fsize" -gt "$volume_size" ]; then
        expand_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_id/expand" "{\"size\": $fsize}")
        ret_code=$(echo $expand_ret | jq -r .ret_code)
        if [ "$ret_code" != "0" ]; then
            log_debug "$ID" "failed to expand boot volume: $expand_ret"
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' 'reinstall_error' '$volume_path' 'failed to expand boot volume'"
            exit -1
        fi
    fi
    # if sysdisk_iops_limit > 0 or sysdisk_bps_limit > 0 update volume qos
    if [ "$sysdisk_iops_limit" -gt 0 -o "$sysdisk_bps_limit" -gt 0 ]; then
        sysdisk_bps_limit=$(($sysdisk_bps_limit * $wds_bps_factor))
        update_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_id/qos" "{\"qos\": {\"iops_limit\": $sysdisk_iops_limit, \"bps_limit\": $sysdisk_bps_limit}}")
        log_debug $vol_ID "update volume qos: $update_ret"
    fi
    vhost_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
    vhost_id=$(echo $vhost_ret | jq -r .id)
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_id\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        log_debug $vol_ID "failed to create wds vhost for boot volume: $vhost_ret, $uss_ret"
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' 'reinstall_error' '$volume_path' 'failed to create wds vhost for boot volume'"
        exit -1
    fi
    echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' 'attached' '$volume_path' 'success'"
    ux_sock=/var/run/wds/$vhost_name
    template=$template_dir/wds_template_with_qa.xml
    if [ "$boot_loader" = uefi ]; then
        template=$template_dir/wds_template_uefi_with_qa.xml
    fi
fi

# Rebuild the domain from a template instead of editing the old XML.
[ -z "$vm_mem" ] && vm_mem='1024m'
[ -z "$vm_cpu" ] && vm_cpu=1
let vm_mem=${vm_mem%[m|M]}*1024
vm_QA="$qemu_agent_dir/$vm_ID.agent"
cp $template $vm_xml
if [ "$nested_enable" = "true" ]; then
    vm_nested="require"
else
    vm_nested="disable"
fi
cpu_vendor=$(lscpu | grep "Vendor ID" | awk -F ':' '{print $2}' | tr -d ' ')
if [ "$cpu_vendor" = "GenuineIntel" ]; then
    vm_virt_feature="vmx"
else
    vm_virt_feature="svm"
fi
vhost_queue_num=1
if [ "$vm_cpu" -gt 2 ]; then
    vhost_queue_num=2
fi
os_code=$(jq -r '.os_code' <<< $metadata)
vm_nvram="$image_dir/${vm_ID}_VARS.fd"
if [ "$boot_loader" = "uefi" ]; then
    cp $nvram_template $vm_nvram
    sed -i \
    -e "s/VM_ID/$vm_ID/g" \
    -e "s/VM_MEM/$vm_mem/g" \
    -e "s/VM_CPU/$vm_cpu/g" \
    -e "s/VHOST_QUEUE_NUM/$vhost_queue_num/g" \
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
    -e "s/VM_ID/$vm_ID/g" \
    -e "s/VM_MEM/$vm_mem/g" \
    -e "s/VM_CPU/$vm_cpu/g" \
    -e "s/VHOST_QUEUE_NUM/$vhost_queue_num/g" \
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
virsh autostart $vm_ID --disable

# Reattach existing XML files for the current data volumes only.
for data_id in $(jq -r '.volumes[] | select(.booting == false) | .id' <<< "$metadata"); do
    vol_xml=$xml_dir/$vm_ID/disk-${data_id}.xml
    virsh attach-device $vm_ID "$vol_xml" --config --persistent
    if [ $? -eq 0 ]; then
        log_debug $ID "Successfully attached data volume $data_id to VM $vm_ID"
    else
        log_debug $ID "Failed to attach data volume $data_id to VM $vm_ID"
        echo "|:-COMMAND-:| launch_vm.sh '$ID' 'reinstall_error' '$SCI_CLIENT_ID' 'failed to attach data volume $data_id' '$snapshot'"
        exit -1
    fi
done

jq .vlans <<< $metadata | ./sync_nic_info.sh "$ID" "$vm_name" "$os_code"
state=reinstall_error
reason="failed to start domain"
virsh start $vm_ID
[ $? -eq 0 ] && state=running && reason="sync"
echo "|:-COMMAND-:| launch_vm.sh '$ID' '$state' '$SCI_CLIENT_ID' '$reason' '$snapshot'"
[ "$state" != running ] && exit -1

# check if the vm is windows and whether to change the rdp port
if [ "$os_code" = "windows" ]; then
    rdp_port=$(jq -r '.login_port' <<< $metadata)
    if [ -n "$rdp_port" ] && [ "${rdp_port}" != "3389" ]  && [ ${rdp_port} -gt 0 ]; then
        # run the script to change the rdp port in background
        async_exec ./async_job/win_rdp_port.sh $ID $rdp_port
    fi
    async_exec ./async_job/win_primary_ip.sh $ID <<< $metadata
fi
