#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 4 ] && die "$0 <vm_ID> <router> <boot_volume> <image>"

ID=$1
vm_ID=inst-$ID
router=$2
boot_volume=$3
image=$4
vm_xml=$(virsh dumpxml $vm_ID)

# Call generate_vm_instance_map.sh to remove mapping before VM deletion
./generate_vm_instance_map.sh remove $vm_ID

# Remove traffic billing mark too (idempotent no-op if this VM was never marked)
./generate_vm_traffic_billing_map.sh remove $vm_ID

virsh undefine --nvram $vm_ID
cmd="virsh destroy $vm_ID"
result=$(eval "$cmd")

# Clean up VM adjust custom metrics
./cleanup_vm_custom_metrics.sh $ID

# Clean up old format rule_id metrics after VM deletion
echo "=== Starting rule_id metrics cleanup for deleted VM ==="
if [ -f "./cleanup_old_rule_id_metrics.sh" ]; then
    echo "Cleaning up old format rule_id metrics for deleted VM: $vm_ID"
    ./cleanup_old_rule_id_metrics.sh --force || {
        echo "Warning: Rule_id metrics cleanup failed, but VM deletion completed successfully"
    }
else
    echo "Warning: Rule_id metrics cleanup script not found, skipping metrics cleanup"
fi
echo "=== Rule_id metrics cleanup completed ==="

count=$(echo $vm_xml | xmllint --xpath 'count(/domain/devices/interface)' -)
vnics=""
for (( i=1; i <= $count; i++ )); do
    vif_dev=$(echo $vm_xml | xmllint --xpath "string(/domain/devices/interface[$i]/target/@dev)" -)
    vnics="$vnics $vif_dev"
done

# Flush conntrack and broadcast useless MAC ARP before clearing rules
./flush_sg_arp.sh $vnics

for vif_dev in $vnics; do
    ./clear_sg_chain.sh $vif_dev
    meta_file="$async_job_dir/$vif_dev"
    [ -f "$meta_file" ] && rm -f "$meta_file"
done
./clear_local_router.sh $router

if [ -f ${image_dir}/${vm_ID}_VARS.fd ]; then
    rm -f ${image_dir}/${vm_ID}_VARS.fd
fi
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID

./end_rescue.sh $ID
if [ -z "$wds_address" ]; then	
    rm -f ${image_dir}/${vm_ID}.*
else
    get_wds_token
    log_debug $ID "clear_vm: WDS mode, boot_volume=$boot_volume image=$image"
    vhosts=$(ls /var/run/wds/instance-${ID}-* 2>/dev/null)
    for vhost in $vhosts; do
	    vhost_name=$(basename $vhost)
        if [ -S "/var/run/wds/$vhost_name" ]; then
           vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')
           uss_id=$(get_uss_gateway)
           # get volume ID from vhost name
           vol_ID=$(echo $vhost_name | awk -F'-' '{print $4}')
           log_debug $ID "clear_vm: async delete vhost $vhost_name vhost_id=$vhost_id vol_ID=$vol_ID uss_id=$uss_id"
           async_exec ./async_job/delete_wds_vhost.sh  $vol_ID $vhost_id $uss_id
        fi
    done
    if [ -n "$boot_volume" ]; then
        # Synchronously remove any vhost still bound to the boot volume before deleting it.
        # The vhost deletion in the loop above is async, so it may not have finished yet.
        bv_vhost_str=$(wds_curl GET "api/v2/block/volumes/$boot_volume/vhost")
        bv_vhost_count=$(echo $bv_vhost_str | jq -r '.count // 0')
        log_debug $ID "clear_vm: boot volume $boot_volume vhost check count=$bv_vhost_count"
        if [ -n "$bv_vhost_count" ] && [ "$bv_vhost_count" -gt 0 ]; then
            bv_vhost_id=$(echo $bv_vhost_str | jq -r '.vhosts[0].id')
            bv_uss_id=$(wds_curl GET "api/v2/sync/block/vhost/$bv_vhost_id/vhost_binded_uss" | jq -r '.uss[0].id // empty')
            log_debug $ID "clear_vm: deleting boot volume vhost $bv_vhost_id uss_id=$bv_uss_id"
            if [ -n "$bv_uss_id" ]; then
                delete_vhost "$boot_volume" "$bv_vhost_id" "$bv_uss_id"
            else
                delete_vhost "$boot_volume" "$bv_vhost_id"
            fi
        fi
        vhost_paths=$(wds_curl GET "api/v2/sync/block/volumes/$boot_volume/bind_status" | jq -r .path)
  	nvpaths=$(jq length <<< $vhost_paths)
	j=0
	while [ $j -lt $nvpaths ]; do
	    vhost_path=$(jq -r .[$j] <<<$vhost_paths)
            log_debug $ID "clear_vm: removing bind_status blacklist path=$vhost_path"
            wds_curl DELETE "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}"
            let j=$j+1
	done
        log_debug $ID "clear_vm: deleting WDS boot volume $boot_volume"
        boot_del_ret=$(wds_curl DELETE "api/v2/sync/block/volumes/$boot_volume?force=false")
        log_debug $ID "clear_vm: boot volume DELETE result: $(echo $boot_del_ret | jq -r '.ret_code // empty') raw=$boot_del_ret"
    fi
    # async cleanup of image snapshots
    if [ -n "$image" ]; then
        async_exec ./async_job/clear_image_snaps.sh $ID $image
    fi
fi
echo "|:-COMMAND-:| $(basename $0) '$ID'"
