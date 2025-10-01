#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 3 ] && die "$0 <migrate_ID> <task_ID> <vm_ID>"

migrate_ID=$1
task_ID=$2
ID=$3
vm_ID=inst-$ID
state="failed"

for i in {1..1800}; do
    vm_state=$(virsh domstate $vm_ID)
    if [ "$vm_state" = "running" ]; then
        echo
        state="completed"
        
        # Update vm_instance_map metrics - ensure VM is properly tracked
        echo "Updating vm_instance_map metrics: ensuring VM $vm_ID is properly tracked"
        ./generate_vm_instance_map.sh update $vm_ID
        
        vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
        virsh define $vm_xml
        virsh autostart $vm_ID
        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
        exit 0
    fi
    sleep 1
done

state="timeout"
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
