#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 2 ] && die "$0 <vm_ID> <action>"

function wait_vm_status()
{
    vm_ID=$1
    status=$2
    # wait for 30 seconds
    for i in {1..60}; do
        state=$(virsh dominfo $vm_ID | grep State | cut -d: -f2- | xargs | sed 's/shut off/shut_off/g')
        [ "$state" = "$status" ] && break
        sleep 0.5
    done
}

function get_vm_state()
{
    vm_ID=$1
    virsh dominfo $vm_ID | grep State | cut -d: -f2- | xargs | sed 's/shut off/shut_off/g'
}

vm_ID=inst-$1
action=$2
if [ "$action" = "restart" ]; then
    virsh reboot $vm_ID
    wait_vm_status $vm_ID "running"
elif [ "$action" = "start" ]; then
    virsh start $vm_ID
    wait_vm_status $vm_ID "running"
elif [ "$action" = "stop" ]; then
    # first try to shutdown the vm
    virsh shutdown $vm_ID
    wait_vm_status $vm_ID "shut_off"
    # if the vm is not shut_off, destroy it
    current_state=$(get_vm_state $vm_ID)
    if [ "$current_state" != "shut_off" ]; then
        virsh destroy $vm_ID
        wait_vm_status $vm_ID "shut_off"
    fi
elif [ "$action" = "hard_stop" ]; then
    virsh destroy $vm_ID
    wait_vm_status $vm_ID "shut_off"
elif [ "$action" = "hard_restart" ]; then
    virsh destroy $vm_ID
    wait_vm_status $vm_ID "shut_off"
    virsh start $vm_ID
elif [ "$action" = "pause" ]; then
    virsh suspend $vm_ID
    wait_vm_status $vm_ID "paused"
elif [ "$action" = "resume" ]; then
    virsh resume $vm_ID
    wait_vm_status $vm_ID "running"
else
    die "Invalid action: $action"
fi

state=$(get_vm_state $vm_ID)
echo "|:-COMMAND-:| $(basename $0) '$1' '$state'"
