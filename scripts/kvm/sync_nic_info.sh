#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <hostname> <os_code> <update_meta>" && exit -1

ID=$1
vm_name=$2
os_code=$3
update_meta=$4
[ -z "$update_meta" ] && update_meta=false
vlans=$(cat)
nvlan=$(jq length <<< $vlans)
i=0
while [ $i -lt $nvlan ]; do
    read -d'\n' -r vlan ip mac gateway router inbound outbound allow_spoofing < <(jq -r ".[$i].vlan, .[$i].ip_address, .[$i].mac_address, .[$i].gateway, .[$i].router, .[$i].inbound, .[$i].outbound, .[$i].allow_spoofing" <<<$vlans)
    jq -r .[$i].security <<< $vlans | ./apply_vm_nic.sh "$ID" "$vlan" "$ip" "$mac" "$gateway" "$router" "$inbound" "$outbound" "$allow_spoofing"
    more_addresses=$(jq -r .[$i].more_addresses <<< $vlans)
    if [ -n "$more_addresses" ]; then
        echo "$more_addresses" | ./apply_second_ips.sh "$ID" "$mac" "$os_code" "$update_meta"
    fi
    ./set_host.sh "$router" "$vlan" "$mac" "$vm_name" "$ip"
    let i=$i+1
done
