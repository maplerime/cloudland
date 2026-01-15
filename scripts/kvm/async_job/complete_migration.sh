#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 3 ] && die "$0 <migrate_ID> <task_ID> <vm_ID>"

migrate_ID=$1
task_ID=$2
ID=$3
vm_ID=inst-$ID
state="failed"

for i in {1..600}; do
    sleep 3
    vm_state=$(virsh domstate $vm_ID)
    if [ -n "$vm_state" ]; then
        state="completed"
        vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
        virsh define $vm_xml
        virsh autostart $vm_ID

        # Update vm_instance_map metrics - add VM to current hypervisor
        echo "Updating vm_instance_map metrics: adding VM $vm_ID to current hypervisor"
        ../generate_vm_instance_map.sh add $vm_ID

        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
        exit 0
    fi
done

state="timeout"
# Migration timeout, clean up metrics for VM
echo "Migration timeout, cleaning up metrics for VM $vm_ID"
../generate_vm_instance_map.sh remove $vm_ID

rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
