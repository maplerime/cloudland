#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 4 ] && echo "$0 <volume_ID> <volume_UUID> <size> <state>" && exit -1

vol_ID=$1
vol_UUID=$2
vol_size=$3
vol_state=$4
vol_path="$volume_dir/volume-${vol_ID}.disk"
if [ ! -f "$vol_path" ]; then
    echo "Volume file not found: $vol_path"
    exit 1
fi

old_size=$(qemu-img info $vol_path | grep 'virtual size:' | cut -d' ' -f5 | tr -d '(')
let new_size=$vol_size*1024*1024*1024

# new size must be larger than current size
if [ "$old_size" -gt "$new_size" ]; then
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' 'volume-${vol_ID}.disk' 'error' 'new size must be larger than current size'"
    exit -1
fi

# resize the volume
qemu-img resize -q $vol_path "${vol_size}G"
if [ $? -eq 0 ]; then
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' 'volume-${vol_ID}.disk' $vol_state '${new_size}G' 'success'"
else
    echo "|:-COMMAND-:| create_volume_local '$vol_ID' 'volume-${vol_ID}.disk' 'error' 'resize failed'"
    exit 1
fi