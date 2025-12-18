#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 12 ] && die "$0 <vm_ID> <image> <snapshot> <volume_id> <pool_id> <old_volume_uuid> <cpu> <memory> <disk_size> <hostname> <boot_loader> <instance_uuid> <image_volume_id>"

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
boot_loader=${11}
instance_uuid=${12:-$ID}
image_volume_id=${13}
state=error
vol_state=error

md=$(cat)
metadata=$(echo $md | base64 -d)
read -d'\n' -r sysdisk_iops_limit sysdisk_bps_limit < <(jq -r ".disk_iops_limit, .disk_bps_limit" <<<$metadata)

vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
mv $vm_xml $vm_xml-$(date +'%s.%N')
virsh dumpxml $vm_ID >$vm_xml

# Try to undefine the domain, handle NVRAM case
virsh undefine $vm_ID 2>/dev/null
if [ $? -ne 0 ]; then
    # If normal undefine fails, try with --nvram (for UEFI VMs)
    virsh undefine --nvram $vm_ID
    if [ $? -ne 0 ]; then
        echo "Warning: Failed to undefine domain $vm_ID, continuing anyway..."
    fi
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
    delete_vhost $vol_ID $vhost_id $uss_id
    # delete old volume
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
    # if sysdisk_iops_limit > 0 or sysdisk_bps_limit > 0 update volume qos
    if [ "$sysdisk_iops_limit" -gt 0 -o "$sysdisk_bps_limit" -gt 0 ]; then
        update_ret=$(wds_curl PUT "api/v2/sync/block/volumes/$volume_id/qos" "{\"qos\": {\"iops_limit\": $sysdisk_iops_limit, \"bps_limit\": $sysdisk_bps_limit}}")
        log_debug $vol_ID "update volume qos: $update_ret"
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

# Check if we need to switch boot mode
is_uefi_current=$(grep -c "loader.*type=.pflash" $vm_xml)
should_be_uefi=0
[ "$boot_loader" = "uefi" ] && should_be_uefi=1

if [ "$is_uefi_current" != "$should_be_uefi" ]; then

    log_debug $vm_ID "Switching boot mode to $boot_loader"

    if [ "$boot_loader" = "uefi" ]; then
        # Switch from BIOS to UEFI
        log_debug $vm_ID "Converting BIOS configuration to UEFI"

        # Create NVRAM file
        vm_nvram="$image_dir/${vm_ID}_VARS.fd"
        cp $nvram_template $vm_nvram
        
        # Add UEFI loader and nvram to <os> section
        sed -i '/<\/os>/i\    <loader readonly='\''yes'\'' type='\''pflash'\''>'$uefi_boot_loader'</loader>' $vm_xml
        sed -i '/<\/os>/i\    <nvram>'$vm_nvram'</nvram>' $vm_xml
        
        # Change metadata disk from IDE to SCSI for UEFI
        if grep -q 'target.*dev=.hdd.*bus=.ide' $vm_xml && grep -q '/meta/' $vm_xml; then
            # Change metadata disk from IDE to SCSI and remove old address
            sed -i 's/dev=.hdd./dev="hdb"/g; s/bus=.ide./bus="scsi"/g' $vm_xml
            sed -i '/<target.*hdb.*scsi/,/<\/disk>/ { /<address type=.drive/d }' $vm_xml
            
            # Add SCSI controller if not exists (never remove IDE - other disks might use it)
            if ! grep -q 'controller.*scsi' $vm_xml; then
                sed -i '/<\/devices>/i\    <controller type="scsi" index="0" model="virtio-scsi"/>' $vm_xml
            fi
        fi
        
    else
        # Switch from UEFI to BIOS
        log_debug $vm_ID "Converting UEFI configuration to BIOS"

        # Remove UEFI loader and nvram from <os> section - use line-by-line approach
        # Remove loader lines
        sed -i '/^[[:space:]]*<loader.*type=.*pflash/d' $vm_xml
        sed -i '/^[[:space:]]*<\/loader>/d' $vm_xml  
        # Remove nvram lines
        sed -i '/^[[:space:]]*<nvram>/d' $vm_xml
        
        # Change metadata disk from SCSI to IDE for BIOS
        if grep -q 'target.*dev=.hdb.*bus=.scsi' $vm_xml && grep -q '/meta/' $vm_xml; then
            # Change metadata disk from SCSI to IDE and remove old address
            sed -i 's/dev=.hdb./dev="hdd"/g; s/bus=.scsi./bus="ide"/g' $vm_xml
            sed -i '/<target.*hdd.*ide/,/<\/disk>/ { /<address type=.pci/d }' $vm_xml
            
            # Add IDE controller if not exists (never remove SCSI - other disks might use it)  
            if ! grep -q 'controller.*ide' $vm_xml; then
                sed -i '/<\/devices>/i\    <controller type="ide" index="0"/>' $vm_xml
            fi
        fi
        
        # Clean up NVRAM file if exists
        [ -f "$image_dir/${vm_ID}_VARS.fd" ] && rm -f "$image_dir/${vm_ID}_VARS.fd"
    fi
fi

# Update basic parameters (memory, CPU, instance UUID, and vhost name for WDS)
sed_cmd="s#>.*</memory>#>$vm_mem</memory>#g; s#>.*</currentMemory>#>$vm_mem</currentMemory>#g; s#>.*</vcpu>#>$vm_cpu</vcpu>#g"
if [ -n "$wds_address" ]; then
  sed_cmd="$sed_cmd; s#$old_vhost_name#$vhost_name#g"
fi
# Add replacement for instance UUID in metadata
sed_cmd="$sed_cmd; s#<instance_id>.*</instance_id>#<instance_id>$instance_uuid</instance_id>#g"
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
