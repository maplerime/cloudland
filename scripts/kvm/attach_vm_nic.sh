#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 4 ] && echo "$0 <vm_ID> <vm_name> <os_code> <update_meta> [meta_only]" && exit -1

ID=$1
vm_ID=inst-$ID
vm_name=$2
os_code=$3
update_meta=$4
# meta_only=true: only rewrite $async_job_dir/$nic_name and exit, touching neither
# the device, the security groups nor the gateway. This is how clapi's nic-meta
# sync backfills north_south onto instances that were created before the mark
# existed -- replaying the whole attach path fleet-wide would rebuild every
# security group chain for nothing.
meta_only=$5

vlan_info=$(cat)
read -d'\n' -r vlan ip mac gateway router inbound outbound allow_spoofing north_south < <(jq -r ".vlan, .ip_address, .mac_address, .gateway, .router, .inbound, .outbound, .allow_spoofing, .north_south" <<<$vlan_info)
# Whether this nic carries north-south traffic, i.e. whether it is the instance's
# default route. Absent (json from an older clapi) means yes: the metering script
# defaults the same way, and defaulting to "no" would silently stop billing the
# nic instead of merely mis-attributing it.
case "$north_south" in true|false) ;; *) north_south=true ;; esac
nic_name=tap$(echo $mac | cut -d: -f4- | tr -d :)
vm_br=br$vlan
if [ "$meta_only" = "true" ]; then
    echo "vm_ip=${ip%/*} vm_br=$vm_br router=$router north_south=$north_south" > "$async_job_dir/$nic_name"
    exit 0
fi
./create_link.sh $vlan
brctl setageing $vm_br 120
virsh domiflist $vm_ID | grep $mac
if [ $? -ne 0 ]; then
    template=$template_dir/interface.xml
    interface_xml=$xml_dir/$vm_ID/$nic_name.xml
    let queue_num=($(virsh dominfo $vm_ID | grep 'CPU(s)' | awk '{print $2}')+1)/2
    cp $template $interface_xml
    sed -i "s/VM_MAC/$mac/g; s/VM_BRIDGE/$vm_br/g; s/VM_VTEP/$nic_name/g; s/QUEUE_NUM/$queue_num/g" $interface_xml
    virsh attach-device $vm_ID $interface_xml --live --persistent
    [ $? -ne 0 ] && virsh attach-device $vm_ID $interface_xml --config
    echo "vm_ip=${ip%/*} vm_br=$vm_br router=$router north_south=$north_south" >> "$async_job_dir/$nic_name"
fi
udevadm settle
async_exec ./send_spoof_arp.py "$vm_br" "${ip%/*}" "$mac"
./set_nic_speed.sh "$ID" "$nic_name" "$inbound" "$outbound"
./reapply_secgroup.sh "$ip" "$mac" "$allow_spoofing" "$nic_name" <<< $vlan_info
./set_subnet_gw.sh "$router" "$vlan" "$gateway" "$ext_vlan"
./set_host.sh "$router" "$vlan" "$mac" "$vm_name" "$ip"
# Normalize null/[] to empty so only the primary interface (the only one with
# real second ips) triggers apply_second_ips; secondary interfaces must not run
# it, otherwise it would overwrite eth0's gateway on windows.
more_addresses=$(jq -c 'if (.more_addresses | length) > 0 then .more_addresses else empty end' <<< $vlan_info)
if [ -n "$more_addresses" -o "$update_meta" = true ]; then
    ./apply_second_ips.sh "$ID" "$mac" "$os_code" "$update_meta" "$ip" "$gateway" <<<$more_addresses
fi
echo "|:-COMMAND-:| $(basename $0) '$ID' '$mac' '$SCI_CLIENT_ID'"
