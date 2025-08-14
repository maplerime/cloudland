#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 5 ] && echo "$0 <vm_ID> <interface_ID> <vlan> <vm_ip> <vm_mac>" && exit -1

ID=$1
vm_ID=inst-$1
iface_ID=$2
vlan=$3
vm_ip=$4
vm_mac=$5
nic_name=tap$(echo $vm_mac | cut -d: -f4- | tr -d :)
vm_br=br$vlan
./clear_link.sh $vlan
state=$(virsh dominfo $vm_ID | grep State | cut -d: -f2- | xargs)
if [ "$state" = "running" ]; then
    virsh detach-interface $vm_ID bridge --mac $vm_mac --live --config
else
    virsh detach-interface $vm_ID bridge --mac $vm_mac --config
fi
./clear_sg_chain.sh $nic_name

meta_file="$async_job_dir/$nic_name"
# build item to delete
del_line="vm_ip=$vm_ip floating_ip=$floating_ip vm_br=$vm_br router=$router"
if [ -f "$meta_file" ]; then
    # delete item
    grep -vF -- "$del_line" "$meta_file" > "${meta_file}.tmp" && mv "${meta_file}.tmp" "$meta_file"
    # delete file if empty
    [ ! -s "$meta_file" ] && rm -f "$meta_file"
fi
echo "|:-COMMAND-:| $(basename $0) '$ID' '$iface_ID' '$SCI_CLIENT_ID'"
