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

log_debug $ID "source_migration.sh: Starting migration_ID=$migration_ID, task_ID=$task_ID, target_hyper=$target_hyper, migration_type=$migration_type"

log_debug $ID "source_migration.sh: Dumping XML for $vm_ID"
virsh dumpxml $vm_ID >$xml_dir/$vm_ID/${vm_ID}.xml
if [ "$migration_type" = "warm" ]; then
    state='source_rollback'
    vm_state=$(virsh domstate $vm_ID)
    if [ "$vm_state" = "shut off" ]; then
        log_debug $ID "source_migration.sh: Starting offline migration to $target_hyper"
        virsh migrate --undefinesource --persistent --offline $vm_ID qemu+ssh://$target_hyper/system
    else
        log_debug $ID "source_migration.sh: Starting live migration to $target_hyper"
        virsh migrate --undefinesource --persistent --suspend --live $vm_ID qemu+ssh://$target_hyper/system
    fi
    old_state=$vm_state
    if [ $? -ne 0 ]; then
        log_debug $ID "source_migration.sh: virsh migrate failed with non-zero exit code"
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'virsh migrate returns non-zero'"
        exit 0
    fi
    log_debug $ID "source_migration.sh: virsh migrate command completed, waiting for VM to disappear from source"
    for i in {1..60}; do
        vm_state=$(virsh domstate $vm_ID)
        if [ -z "$vm_state" ]; then
            if [ "$old_state" = "running" ]; then
	        ssh $target_hyper virsh resume $vm_ID
                if [ $? -ne 0 ]; then
                    log_debug $ID "source_migration.sh: failed to resume vm on the target host"
                    echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'failed to resume vm on target host'"
                    exit 0
                fi
                log_debug $ID "source_migration.sh: vm $vm_ID on target host resumed"
	    fi
            break
        fi
        sleep 0.5
    done
    if [ -n "$vm_state" ]; then
        log_debug $ID "source_migration.sh: VM still exists after 60 seconds wait"
        echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'vm remains after virsh migrate'"
        exit 0
    fi
    log_debug $ID "source_migration.sh: VM successfully removed from source"
else
    log_debug $ID "source_migration.sh: Cold migration - shutting down VM"
    virsh shutdown $vm_ID
    for i in {1..60}; do
	vm_state=$(virsh domstate $vm_ID)
        [ "$vm_state" = "shut off" ] && break
        sleep 0.5
    done
    if [ "$vm_state" != "shut off" ]; then
        log_debug $ID "source_migration.sh: VM did not shut down cleanly, forcing destroy"
        virsh destroy $vm_ID
    fi
    log_debug $ID "source_migration.sh: VM shutdown/destroy completed"
fi

state="source_prepared"
log_debug $ID "source_migration.sh: Migration preparation completed, reporting state=$state"
echo "|:-COMMAND-:| migrate_vm.sh '$migration_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' ''"
