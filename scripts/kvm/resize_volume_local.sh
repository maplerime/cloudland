#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 5 ] && echo "$0 <boot_vm_ID> <volume_ID> <volume_UUID> <size> <state>" && exit -1

vm_ID=$1
vol_ID=$2
vol_UUID=$3
vol_size=$4
vol_state=$5
if [ -z "$vm_ID" ] || ["$vm_ID" = "0"]; then
    vol_path="$image_dir/inst-${vm_ID}.disk"
else
    vol_path="$volume_dir/volume-${vol_ID}.disk"
fi
if [ ! -f "$vol_path" ]; then
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' '' 'error' 'volume not found'"
    exit -1
fi

old_size=$(qemu-img info -U $vol_path | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')
let new_size=$vol_size*1024*1024*1024

# new size must be larger than current size
if [ "$old_size" -ge "$new_size" ]; then
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' '' 'error' 'new size must be larger than current size'"
    exit -1
fi

# resize the volume
qemu-img resize -q $vol_path "${vol_size}G"
if [ $? -eq 0 ]; then
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' '' '$vol_state' 'success'"
else
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' '' 'error' 'resize failed'"
    exit -1
fi