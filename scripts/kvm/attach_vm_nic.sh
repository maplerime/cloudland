#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 4 ] && echo "$0 <vm_ID> <vm_name> <os_code> <update_meta>" && exit -1

ID=$1
vm_ID=inst-$ID
vm_name=$2
os_code=$3
update_meta=$4

vlan_info=$(cat)
read -d'\n' -r vlan ip mac gateway router inbound outbound allow_spoofing < <(jq -r ".vlan, .ip_address, .mac_address, .gateway, .router, .inbound, .outbound, .allow_spoofing" <<<$vlan_info)
nic_name=tap$(echo $mac | cut -d: -f4- | tr -d :)
vm_br=br$vlan
./create_link.sh $vlan
brctl setageing $vm_br 300
virsh domiflist $vm_ID | grep $mac
if [ $? -ne 0 ]; then
    template=$template_dir/interface.xml
    interface_xml=$xml_dir/$vm_ID/$nic_name.xml
    let queue_num=($(virsh dominfo $vm_ID | grep 'CPU(s)' | awk '{print $2}')+1)/2
    cp $template $interface_xml
    sed -i "s/VM_MAC/$mac/g; s/VM_BRIDGE/$vm_br/g; s/VM_VTEP/$nic_name/g; s/QUEUE_NUM/$queue_num/g" $interface_xml
    virsh attach-device $vm_ID $interface_xml --live --persistent
    [ $? -ne 0 ] && virsh attach-device $vm_ID $interface_xml --config
fi
udevadm settle
async_exec ./send_spoof_arp.py "$vm_br" "${ip%/*}" "$mac"
./set_nic_speed.sh "$ID" "$nic_name" "$inbound" "$outbound"
./reapply_secgroup.sh "$ip" "$mac" "$allow_spoofing" "$nic_name" <<< $vlan_info
./set_subnet_gw.sh "$router" "$vlan" "$gateway" "$ext_vlan"
./set_host.sh "$router" "$vlan" "$mac" "$vm_name" "$ip"
more_addresses=$(jq -r .more_addresses <<< $vlan_info)
if [ -n "$more_addresses" ]; then
    echo "$more_addresses" | ./apply_second_ips.sh "$ID" "$mac" "$os_code" "$update_meta"
fi
echo "vm_ip=${ip%/*} vm_br=$vm_br router=$router" >> "$async_job_dir/$nic_name"
echo "|:-COMMAND-:| $(basename $0) '$ID' '$mac' '$SCI_CLIENT_ID'"
