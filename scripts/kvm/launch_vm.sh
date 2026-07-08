#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 9 ] && die "$0 <vm_ID> <image> <qa_enabled> <name> <cpu> <memory> <disk_size> <volume_id> <nested_enable> <boot_loader> <instance_uuid>"

ID=$1
vm_ID=inst-$ID
img_name=$2
qa_enabled=$3
vm_name=$4
vm_cpu=$5
vm_mem=$6
disk_size=$7
vol_ID=$8
nested_enable=$9
boot_loader=${10}
instance_uuid=${11:-$ID}
state=error
vm_vnc=""
vol_state=error

md=$(cat)
metadata=$(echo $md | base64 -d)
read -d'\n' -r sysdisk_iops_limit sysdisk_bps_limit < <(jq -r ".disk_iops_limit, .disk_bps_limit" <<<$metadata)
storage_pool_relation=$(jq -c '.storage_pool_relation // {}' <<<$metadata)

let fsize=$disk_size*1024*1024*1024
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
            echo "|:-COMMAND-:| create_volume_local '$SCI_CLIENT_ID' '$vol_ID' 'local://${vm_ID}.disk' '$vol_state' 'image $img_name not available!'"
            exit -1
        fi
        format=$(qemu-img info $image_cache/$img_name | grep 'file format' | cut -d' ' -f3)
        cmd="qemu-img convert -f $format -O qcow2 $image_cache/$img_name $vm_img"
        result=$(eval "$cmd")
        vsize=$(qemu-img info $vm_img | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')
        if [ "$vsize" -gt "$fsize" ]; then
            echo "|:-COMMAND-:| create_volume_local '$SCI_CLIENT_ID' '$vol_ID' 'local://${vm_ID}.disk' '$vol_state' 'flavor is smaller than image size'"
            exit -1
        fi
        qemu-img resize -q $vm_img "${disk_size}G" &> /dev/null
        vol_state=attached
        echo "|:-COMMAND-:| create_volume_local.sh '$SCI_CLIENT_ID' '$vol_ID' 'local://${vm_ID}.disk' '$vol_state' 'success'"
    fi
else
    get_wds_token
    # default the system-disk copy_clone threshold (GB) if the config is unset/empty
    if [ -z "$clone_volume_size_threshold" ]; then clone_volume_size_threshold=1024; fi
    if [ -n "$storage_pool_relation" ] && [ "$storage_pool_relation" != "{}" ]; then
        pool_list=$(jq -r 'keys | join(",")' <<<$storage_pool_relation)
        if [ -n "$pool_list" ]; then
            selected_pool=$(select_pool_lowest_usage "$pool_list")
            if [ -n "$selected_pool" ]; then
                img_vol_from_rel=$(jq -r ".\"$selected_pool\".image_volume_id // empty" <<<$storage_pool_relation)
                snap_from_rel=$(jq -r ".\"$selected_pool\".snapshot // empty" <<<$storage_pool_relation)
                if [ -n "$img_vol_from_rel" ] && [ -n "$snap_from_rel" ]; then
                    pool_ID=$selected_pool
                    image_volume_id=$img_vol_from_rel
                    snapshot=$snap_from_rel
                fi
            fi
        fi
    fi
    if [ -z "$pool_ID" ]; then
        pool_ID=$wds_pool_id
    fi
    image=$(basename $img_name .raw)
    if [ "$pool_ID" != "$wds_pool_id" ]; then
        pool_prefix=$(get_uuid_prefix "$pool_ID")
        image=${image}-${pool_prefix}
    fi
    vhost_name=instance-$ID-volume-$vol_ID-$RANDOM
    if [ "$disk_size" -lt "$clone_volume_size_threshold" ]; then
        # ---- linked clone path (system disk < threshold): unchanged behaviour ----
        is_copy_clone=false
        log_debug $vol_ID "system disk $disk_size GB < threshold $clone_volume_size_threshold GB, using linked clone"
        snapshot_name=${image}-${snapshot}
        read -d'\n' -r snapshot_id volume_size <<< $(wds_curl GET "api/v2/sync/block/snaps?name=$snapshot_name" | jq -r '.snaps[0] | "\(.id) \(.snap_size)"')
        if [ -z "$snapshot_id" -o "$snapshot_id" = null ]; then
            snapshot_ret=$(wds_curl POST "api/v2/sync/block/snaps" "{\"name\": \"$snapshot_name\", \"description\": \"$snapshot_name\", \"volume_id\": \"$image_volume_id\"}")
            read -d'\n' -r snapshot_id volume_size <<< $(wds_curl GET "api/v2/sync/block/snaps?name=$snapshot_name" | jq -r '.snaps[0] | "\(.id) \(.snap_size)"')
            if [ -z "$snapshot_id" -o "$snapshot_id" = null ]; then
                # error handling: image snapshot could not be created
                log_debug $vol_ID "failed to create image snapshot $snapshot_name: $snapshot_ret"
                echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'failed to create image snapshot, $snapshot_ret' '$is_copy_clone'"
                exit -1
            fi
        fi
        volume_ret=$(wds_curl POST "api/v2/sync/block/snaps/$snapshot_id/clone" "{\"name\": \"$vhost_name\"}")
        volume_id=$(echo $volume_ret | jq -r .id)
        if [ -z "$volume_id" -o "$volume_id" = null ]; then
            # error handling: linked clone volume creation failed
            log_debug $vol_ID "failed to create linked clone from snapshot $snapshot_name: $volume_ret"
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'failed to create boot volume based on snapshot $snapshot_name, $volume_ret!' '$is_copy_clone'"
            exit -1
        fi
        log_debug $vol_ID "linked clone volume $volume_id created for boot disk"
    else
        # ---- copy_clone path (system disk >= threshold): independent volume from image volume ----
        is_copy_clone=true
        log_debug $vol_ID "system disk $disk_size GB >= threshold $clone_volume_size_threshold GB, using copy_clone from image volume $image_volume_id"
        # error handling: copy_clone needs a valid source image volume id
        if [ -z "$image_volume_id" -o "$image_volume_id" = null ]; then
            log_debug $vol_ID "image_volume_id is empty, cannot copy_clone boot volume"
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'image_volume_id is empty, cannot copy_clone boot volume' '$is_copy_clone'"
            exit -1
        fi
        # interface D: copy_clone directly from the image volume (no snapshot); phy_pool_id = the resolved pool_ID (same pool as the source image), speed=128
        clone_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$image_volume_id/copy_clone" "{\"name\": \"$vhost_name\", \"phy_pool_id\": \"$pool_ID\", \"speed\": 128}")
        read -d'\n' -r task_id ret_code message < <(jq -r ".task_id, .ret_code, .message" <<<$clone_ret)
        # error handling: copy_clone task must be accepted
        if [ "$ret_code" != "0" -o -z "$task_id" -o "$task_id" = null ]; then
            log_debug $vol_ID "failed to start copy_clone from image volume $image_volume_id: $clone_ret"
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'failed to copy_clone boot volume from image volume $image_volume_id, $clone_ret' '$is_copy_clone'"
            exit -1
        fi
        log_debug $vol_ID "copy_clone task $task_id started for boot volume, polling for completion"
        # poll the copy_clone task (reference: async_job/clone_image.sh); 150 x 5s ~= 750s timeout
        clone_state=error
        for i in {1..150}; do
            st=$(wds_curl GET "api/v2/sync/block/volumes/tasks/$task_id" | jq -r .task.state)
            [ "$st" = "TASK_COMPLETE" ] && clone_state=done && break
            [ "$st" = "TASK_FAILED" ] && clone_state=error && break
            sleep 5
        done
        log_debug $vol_ID "copy_clone task $task_id finished with state $clone_state"
        # reverse-lookup the new volume id AND its size in one call (id and volume_size are siblings in the response)
        read -d'\n' -r volume_id volume_size < <(wds_curl GET "api/v2/sync/block/volumes?name=$vhost_name" | jq -r '.volumes[0] | "\(.id) \(.volume_size)"')
        # error handling: on failure/timeout, delete the orphan volume then report error
        if [ "$clone_state" != "done" -o -z "$volume_id" -o "$volume_id" = null ]; then
            log_debug $vol_ID "copy_clone task $task_id failed or timed out, deleting orphan volume $volume_id"
            [ -n "$volume_id" -a "$volume_id" != null ] && wds_curl DELETE "api/v2/sync/block/volumes/$volume_id?force=true"
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' '' 'copy_clone task $task_id failed or timed out for boot volume' '$is_copy_clone'"
            exit -1
        fi
        [ -z "$volume_size" -o "$volume_size" = null ] && volume_size=0
        log_debug $vol_ID "copy_clone produced independent volume $volume_id with size $volume_size"
    fi
    if [ "$fsize" -gt "$volume_size" ]; then
        expand_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_id/expand" "{\"size\": $fsize}")
        ret_code=$(echo $expand_ret | jq -r .ret_code)
        if [ "$ret_code" != "0" ]; then
            echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$pool_ID/$volume_id' 'failed to expand boot volume to size $fsize, $expand_ret' '$is_copy_clone'"
            exit -1
        fi
    fi
    # if sysdisk_iops_limit > 0 or sysdisk_bps_limit > 0 update volume qos
    if [ "$sysdisk_iops_limit" -gt 0 -o "$sysdisk_bps_limit" -gt 0 ]; then
        sysdisk_bps_limit=$(($sysdisk_bps_limit * $wds_bps_factor))
        update_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_id/qos" "{\"qos\": {\"iops_limit\": $sysdisk_iops_limit, \"bps_limit\": $sysdisk_bps_limit}}")
        log_debug $vol_ID "update volume qos: $update_ret"
    fi
    uss_id=$(get_uss_gateway)
    vhost_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
    vhost_id=$(echo $vhost_ret | jq -r .id)
    uss_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" "{\"vhost_id\": \"$vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$volume_id\", \"is_snapshot\": false}")
    ret_code=$(echo $uss_ret | jq -r .ret_code)
    if [ "$ret_code" != "0" ]; then
        echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$pool_ID/$volume_id' 'failed to create wds vhost for boot volume, $vhost_ret, $uss_ret!' '$is_copy_clone'"
        exit -1
    fi
    vol_state=attached
    echo "|:-COMMAND-:| create_volume_wds_vhost '$vol_ID' '$vol_state' 'wds_vhost://$pool_ID/$volume_id' 'success' '$is_copy_clone'"
    ux_sock=/var/run/wds/$vhost_name
    template=$template_dir/wds_template_with_qa.xml
    if [ "$boot_loader" = "uefi" ]; then
        template=$template_dir/wds_template_uefi_with_qa.xml
    fi
fi

[ -z "$vm_mem" ] && vm_mem='1024m'
[ -z "$vm_cpu" ] && vm_cpu=1
let vm_mem=${vm_mem%[m|M]}*1024
mkdir -p $xml_dir/$vm_ID
vm_QA="$qemu_agent_dir/$vm_ID.agent"
vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
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
#sed -i "s/VM_ID/$vm_ID/g; s/VM_MEM/$vm_mem/g; s/VM_CPU/$vm_cpu/g; s#VM_IMG#$vm_img#g; s#VM_UNIX_SOCK#$ux_sock#g; s#VM_META#$vm_meta#g; s#VM_AGENT#$vm_QA#g; s/VM_NESTED/$vm_nested/g; s/VM_VIRT_FEATURE/$vm_virt_feature/g; s/INSTANCE_UUID/$instance_uuid/g" $vm_xml
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
jq .vlans <<< $metadata | ./sync_nic_info.sh "$ID" "$vm_name" "$os_code"
virsh start $vm_ID
[ $? -eq 0 ] && state=running
echo "|:-COMMAND-:| $(basename $0) '$ID' '$state' '$SCI_CLIENT_ID' 'init' '$snapshot'"

# check if the vm is windows and whether to change the rdp port
if [ "$os_code" = "windows" ]; then
    rdp_port=$(jq -r '.login_port' <<< $metadata)
    if [ -n "$rdp_port" ] && [ "${rdp_port}" != "3389" ]  && [ ${rdp_port} -gt 0 ]; then
        # run the script to change the rdp port in background
        async_exec ./async_job/win_rdp_port.sh $ID $rdp_port
    fi
    async_exec ./async_job/win_primary_ip.sh $ID <<< $metadata
fi
