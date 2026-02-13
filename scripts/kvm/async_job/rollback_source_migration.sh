#!/bin/bash -xv

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 6 ] && die "$0 <migration_ID> <task_ID> <vm_ID> <router> <target_hyper> <migration_type>"

migration_ID=$1
task_ID=$2
ID=$3
vm_ID=inst-$ID
router=$4
target_hyper=$5
migration_type=$6

./clear_hyper_vhost.sh $ID $target_hyper
vm_xml=$xml_dir/$vm_ID/$vm_ID.xml
virsh define $vm_xml
virsh autostart $vm_ID --disable
virsh start $vm_ID
sync_vm $ID
