#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 5 ] && echo "$0 <volume_ID> <volume_UUID> <size> <booting> <vm_ID>" && exit -1

vol_ID=$1
vol_UUID=$2
vol_size=$3
booting=$4
vm_ID=$5
if [ "$booting" = "false" ]; then
    vol_path="$volume_dir/volume-${vol_ID}.disk"
else
    vol_path="$image_dir/inst-${vm_ID}.disk"
fi
if [ ! -f "$vol_path" ]; then
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'error'"
    exit -1
fi

# if volume attached to a VM, stop the VM first
if [ "$vm_ID" != "0" ]; then
    ./action_vm.sh $vm_ID hard_stop
fi
old_size=$(qemu-img info -U $vol_path | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')
let new_size=$vol_size*1024*1024*1024

# new size must be larger than current size
if [ "$old_size" -ge "$new_size" ]; then
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'error'"
    exit -1
fi

# resize the volume
qemu-img resize -q $vol_path "${vol_size}G"
if [ $? -eq 0 ]; then
    if [ "$vm_ID" != "0" ]; then
        virsh start inst-$vm_ID
    fi
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'success'"
else
    echo "|:-COMMAND-:| resize_volume '$vol_ID' 'error'"
    exit -1
fi