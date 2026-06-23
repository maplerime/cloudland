#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 4 ] && die "$0 <migrate_ID> <task_ID> <vm_ID> <migration_type> [vm_name]"

migrate_ID=$1
task_ID=$2
ID=$3
migration_type=$4
vm_name=$5
vm_ID=inst-$ID
echo $$ >$run_dir/${vm_ID}-$migrate_ID
state="failed"

# ---- Local-disk cold: disks are now present (copied by source_migration.sh)
#      and bridges were built in target_migration.sh. Define + start, then
#      reapply host-side networking. The NIC is already in the copied XML, so
#      sync_nic_info -> attach_vm_nic skips the attach and only reapplies the
#      SG/router/host rules (idempotent). ----
if [ -z "$wds_address" ] && [ "$migration_type" = "cold" ]; then
    log_debug $ID "complete_migration.sh: local-disk cold, defining and starting VM on target"
    # metadata (heredoc) carries vlans + os_code; vm_name comes from the args.
    metadata=$(cat | base64 -d)
    os_code=$(jq -r '.os_code' <<<$metadata)
    virsh define $xml_dir/$vm_ID/${vm_ID}.xml
    virsh autostart $vm_ID --disable
    virsh start $vm_ID
    # Start failure -> request rollback (source VM is still defined/shut off).
    if [ $? -ne 0 ]; then
        log_debug $ID "complete_migration.sh: failed to start VM on target, requesting rollback"
        rm -f $run_dir/${vm_ID}-$migrate_ID
        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' 'source_rollback' 'failed to start vm on target'"
        exit 0
    fi
    # Reapply host-side networking (SG/router/host) for each vlan.
    jq .vlans <<<$metadata | ../sync_nic_info.sh "$ID" "$vm_name" "$os_code"
    ../generate_vm_instance_map.sh add $vm_ID
    rm -f $run_dir/${vm_ID}-$migrate_ID
    state="completed"
    log_debug $ID "complete_migration.sh: local-disk cold completed on target"
    echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' ''"
    exit 0
fi

for i in {1..600}; do
    sleep 3
    if [ "$migration_type" = "warm" ]; then
        virsh domjobinfo --completed --keep-completed $vm_ID | grep Completed
        [ $? -eq 0 ] && state="completed"
    else
        vm_state=$(virsh domstate $vm_ID)
        if [ -n "$vm_state" ]; then
            state="completed"
            vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
            virsh define $vm_xml
            virsh start $vm_xml
            virsh autostart $vm_ID --disable
        fi
    fi
    if [ "$state" = "completed" ]; then 
        rm -f $run_dir/${vm_ID}-$migrate_ID
        # Update vm_instance_map metrics - add VM to current hypervisor
        echo "Updating vm_instance_map metrics: adding VM $vm_ID to current hypervisor"
        ../generate_vm_instance_map.sh add $vm_ID

        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' ''"
        exit 0
    fi
done

state="timeout"
# Migration timeout, clean up metrics for VM
echo "Migration timeout, cleaning up metrics for VM $vm_ID"
../generate_vm_instance_map.sh remove $vm_ID

virsh undefine --nvram $vm_ID
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID
rm -f $run_dir/${vm_ID}-$migrate_ID
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'cleanup target'"
sync_vm $ID
