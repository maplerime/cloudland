#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 6 ] && die "$0 <migration_ID> <task_ID> <vm_ID> <router> <target_hyper> <migration_type>"

migration_ID=$1
task_ID=$2
ID=$3
vm_ID=inst-$ID
router=$4
target_hyper=$5
migration_type=$6
state=failed

vm_xml=$(cat $xml_dir/$vm_ID/$vm_ID.xml)
virsh destroy $vm_ID
virsh undefine --nvram $vm_ID
./clear_source_vhost.sh $ID

count=$(echo $vm_xml | xmllint --xpath 'count(/domain/devices/interface)' -)
for (( i=1; i <= $count; i++ )); do
    vif_dev=$(echo $vm_xml | xmllint --xpath "string(/domain/devices/interface[$i]/target/@dev)" -)
    ./clear_sg_chain.sh $vif_dev
done
./clear_local_router.sh $router

# Update vm_instance_map metrics - remove VM from source hypervisor
echo "Updating vm_instance_map metrics: removing VM $vm_ID from source hypervisor"
./generate_vm_instance_map.sh remove $vm_ID

rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID

# Clean up all custom metrics for migrated VM
echo "=== Starting VM custom metrics cleanup ==="
if [ -f "/opt/cloudland/scripts/kvm/cleanup_vm_custom_metrics.sh" ]; then
    echo "Cleaning up all custom metrics for migrated VM: $vm_ID (ID: $ID)"
    /opt/cloudland/scripts/kvm/cleanup_vm_custom_metrics.sh $ID || {
        echo "Warning: VM custom metrics cleanup failed, but migration completed successfully"
    }
else
    echo "Warning: VM custom metrics cleanup script not found, skipping metrics cleanup"
fi
echo "=== VM custom metrics cleanup completed ==="
