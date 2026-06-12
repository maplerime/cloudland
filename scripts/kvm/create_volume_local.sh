#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <vol_ID> <size> <inst_ID>" && exit -1

vol_ID=$1
size=$2
inst_ID=${3:-0}
state='error'

qemu-img create -f qcow2 -o cluster_size=2M $volume_dir/volume-${vol_ID}.disk ${size}G
[ $? -eq 0 ] && state='available'
echo "|:-COMMAND-:| $(basename $0) '$SCI_CLIENT_ID' '$vol_ID' 'local://volume-${vol_ID}.disk' '$state' 'success'"

if [ "$state" = "available" ] && [ -n "$inst_ID" ] && [ "$inst_ID" != "0" ]; then
    ./attach_volume_local.sh "$inst_ID" "$vol_ID" "volume-${vol_ID}.disk"
fi
