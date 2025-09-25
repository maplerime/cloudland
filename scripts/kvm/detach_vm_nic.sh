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
interface_xml=$xml_dir/$vm_ID/$nic_name.xml
if [ ! -f "$interface_xml" ]; then
    template=$template_dir/interface.xml
    cp $template $interface_xml
    let queue_num=($(virsh dominfo $vm_ID | grep 'CPU(s)' | awk '{print $2}')+1)/2
    sed -i "s/VM_MAC/$vm_mac/g; s/VM_BRIDGE/br$vlan/g; s/VM_VTEP/$nic_name/g; s/QUEUE_NUM/$queue_num/g" $interface_xml
fi
virsh detach-device $vm_ID $interface_xml --live --persistent
[ $? -ne 0 ] && virsh detach-device $vm_ID $interface_xml --config
rm -f $interface_xml
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
