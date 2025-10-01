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

vm_xml=$(virsh dumpxml $vm_ID)
if [ "$migration_type" = "warm" ]; then
    virsh migrate --persistent --live $vm_ID qemu+ssh://$target_hyper/system
    if [ $? -ne 0 ]; then
        ./clear_source_vhost.sh
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
	exit 1
    fi
else
    virsh shutdown $vm_ID
    for i in {1..60}; do
        vm_state=$(virsh dominfo $vm_ID | grep State | cut -d: -f2- | xargs | sed 's/shut off/shut_off/g')
        [ "$vm_state" = "shut_off" ] && break
        sleep 0.5
    done
    if [ "$vm_state" != "shut_off" ]; then
        virsh destroy $vm_ID
    fi
fi
virsh undefine $vm_ID
if [ $? -ne 0 ]; then
    virsh undefine --nvram $vm_ID
fi

# Update vm_instance_map metrics - remove VM from source hypervisor
echo "Updating vm_instance_map metrics: removing VM $vm_ID from source hypervisor"
./generate_vm_instance_map.sh remove $vm_ID

./clear_source_vhost.sh

count=$(echo $vm_xml | xmllint --xpath 'count(/domain/devices/interface)' -)
for (( i=1; i <= $count; i++ )); do
    vif_dev=$(echo $vm_xml | xmllint --xpath "string(/domain/devices/interface[$i]/target/@dev)" -)
    ./clear_sg_chain.sh $vif_dev
done
./clear_local_router.sh $router
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID
state="source_prepared"
echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
