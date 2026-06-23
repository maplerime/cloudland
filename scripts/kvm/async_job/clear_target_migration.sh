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
# Local-disk: remove disks copied to the target during the aborted migration.
# Volume list arrives as []VolumeInfo JSON on stdin (heredoc); WDS has no such
# files to clean here.
if [ -z "$wds_address" ]; then
    log_debug $ID "clear_target_migration.sh: local-disk rollback, removing copied disks on target"
    volumes=$(cat)
    rm -f ${image_dir}/${vm_ID}.*
    [ -f ${image_dir}/${vm_ID}_VARS.fd ] && rm -f ${image_dir}/${vm_ID}_VARS.fd
    nvol=$(jq length <<<"$volumes" 2>/dev/null)
    [ -z "$nvol" ] && nvol=0
    # Remove each non-boot data disk by its volume id.
    i=0
    while [ $i -lt $nvol ]; do
        read -d'\n' -r vol_ID booting < <(jq -r ".[$i].id, .[$i].booting" <<<"$volumes")
        [ "$booting" != "true" ] && rm -f ${volume_dir}/volume-${vol_ID}.disk
        let i=$i+1
    done
fi
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID
state=rollback
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state' 'target hyper clear'"
sync_vm $ID
