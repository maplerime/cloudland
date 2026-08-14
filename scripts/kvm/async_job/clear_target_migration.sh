#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 3 ] && die "$0 <migrate_ID> <task_ID> <vm_ID>"

migrate_ID=$1
task_ID=$2
ID=$3
vm_ID=inst-$ID

kill $(cat $run_dir/${vm_ID}-$migrate_ID)
rm -f $run_dir/${vm_ID}-$migrate_ID
dom_state=$(virsh domstate $vm_ID)
if [ -n "$dom_state" ]; then
    virsh shutdown $vm_ID
    sleep 5
    virsh destroy $vm_ID
    virsh undefine --nvram $vm_ID
fi
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID

# This VM was speculatively added to vm_instance_map / vm_traffic_billing_map
# when target_migration.sh reported target_prepared, before the actual warm
# migration data transfer (source_migration.sh) ran. Since that transfer just
# failed (hence this cleanup), undo those speculative adds here -- both calls
# are idempotent no-ops if the VM was never marked to begin with.
../generate_vm_instance_map.sh remove $vm_ID
../generate_vm_traffic_billing_map.sh remove $vm_ID

state=rollback
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'target hyper clear'"
sync_vm $ID
