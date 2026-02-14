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

mkdir -p $run_dir/$vm_ID
cat >$run_dir/$vm_ID/volumes.json
virsh dumpxml $vm_ID >$xml_dir/$vm_ID/${vm_ID}.xml
if [ "$migration_type" = "warm" ]; then
    state='source_rollback'
    vm_state=$(virsh domstate $vm_ID)
    if [ "$vm_state" = "shut off" ]; then
        virsh migrate --undefinesource --persistent --offline $vm_ID qemu+ssh://$target_hyper/system
    else
        virsh migrate --undefinesource --persistent --live $vm_ID qemu+ssh://$target_hyper/system
    fi
    if [ $? -ne 0 ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'virsh migrate returns non-zero'"
        exit 0
    fi
    for i in {1..60}; do
        vm_state=$(virsh domstate $vm_ID)
        [ -z "$vm_state" ] && break
        sleep 1
    done
    if [ -n "$vm_state" ]; then
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'vm remains after virsh migrate'"
        exit 0
    fi
else
    virsh shutdown $vm_ID
    for i in {1..60}; do
	vm_state=$(virsh domstate $vm_ID)
        [ "$vm_state" = "shut off" ] && break
        sleep 0.5
    done
    if [ "$vm_state" != "shut off" ]; then
        virsh destroy $vm_ID
    fi
fi

state="source_prepared"
echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' ''"
