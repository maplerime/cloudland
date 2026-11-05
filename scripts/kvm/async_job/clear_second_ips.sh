#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <mac> <os_code>" && exit -1

ID=$1
vm_ID=inst-$ID
mac=$2
os_code=$3

more_addresses=$(cat)
naddrs=$(jq length <<< "$more_addresses")
[ "$naddrs" -eq 0 ] && exit 0

if [ "$os_code" = "windows" ]; then
    for i in {1..120}; do
        virsh qemu-agent-command "$vm_ID" '{"execute":"guest-ping"}'
        if [ $? -eq 0 ]; then
	    break
        fi
    done
    python3 ../windows_network.py "$vm_ID" --remove <<< "$more_addresses"
fi
