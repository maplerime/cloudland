#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 9 ] && echo "$0 <vm_ID> <vlan> <vm_ip> <vm_mac> <gateway> <router> <inbound> <outbound> <allow_spoofing>" && exit -1

ID=$1
vm_ID=inst-$ID
vlan=$2
vm_ip=$3
vm_mac=$4
gateway=$5
router=$6
inbound=$7
outbound=$8
allow_spoofing=$9
nic_name=tap$(echo $vm_mac | cut -d: -f4- | tr -d :)
vm_br=br$vlan
./create_link.sh $vlan
brctl setageing $vm_br 0
virsh domiflist $vm_ID | grep $vm_mac
if [ $? -ne 0 ]; then
    template=$template_dir/interface.xml
    interface_xml=$xml_dir/$vm_ID/$nic_name.xml
    cp $template $interface_xml
    sed -i "s/VM_MAC/$vm_mac/g; s/VM_BRIDGE/$vm_br/g; s/VM_VTEP/$nic_name/g" $interface_xml
    virsh attach-device $vm_ID $interface_xml --config
    virsh attach-device $vm_ID $interface_xml --live --persistent
fi
vlan_info=$(cat)
udevadm settle
./send_spoof_arp.py "$vm_br" "$vm_ip" "$vm_mac" &
./set_nic_speed.sh "$ID" "$nic_name" "$inbound" "$outbound"
./reapply_secgroup.sh "$vm_ip" "$vm_mac" "$allow_spoofing" "$nic_name" <<< $vlan_info
./set_subnet_gw.sh "$router" "$vlan" "$gateway" "$ext_vlan"

echo "vm_ip=$vm_ip vm_br=$vm_br router=$router" >> "$async_job_dir/$nic_name"
