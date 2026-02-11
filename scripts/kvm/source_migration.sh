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

virsh dumpxml $vm_ID >$xml_dir/$vm_ID/${vm_ID}.xml
if [ "$migration_type" = "warm" ]; then
    state='source_rollback'
    vm_state=$(virsh domstate $vm_ID | sed 's/shut off/shut_off/g')
    if [ "$vm_state" = "shut off" ]; then
        virsh migrate --persistent --offline $vm_ID qemu+ssh://$target_hyper/system
    else
        virsh migrate --persistent --live $vm_ID qemu+ssh://$target_hyper/system
    fi
    if [ $? -ne 0 ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
        exit 0
    fi
    for i in {1..20}; do
        vm_state=$(virsh domstate $vm_ID)
        [ -z "$vm_state" ] && break
        sleep 1
    done
    if [ -n "$vm_state" ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
        exit 0
    fi
else
    virsh shutdown $vm_ID
    for i in {1..60}; do
	vm_state=$(virsh domstate $vm_ID | sed 's/shut off/shut_off/g')
        [ "$vm_state" = "shut_off" ] && break
        sleep 0.5
    done
    if [ "$vm_state" != "shut_off" ]; then
        virsh destroy $vm_ID
    fi
fi

state="source_prepared"
echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
