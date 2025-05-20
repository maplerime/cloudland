#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <hostname> <os_code>" && exit -1

ID=$1
vm_name=$2
os_code=$3
vlans=$(cat)
nvlan=$(jq length <<< $vlans)
i=0
while [ $i -lt $nvlan ]; do
    read -d'\n' -r vlan ip mac gateway router inbound outbound allow_spoofing < <(jq -r ".[$i].vlan, .[$i].ip_address, .[$i].mac_address, .[$i].gateway, .[$i].router, .[$i].inbound, .[$i].outbound, .[$i].allow_spoofing" <<<$vlans)
    jq -r .[$i].security <<< $vlans | ./apply_vm_nic.sh "$ID" "$vlan" "$ip" "$mac" "$gateway" "$router" "$inbound" "$outbound" "$allow_spoofing"
<<<<<<< HEAD
    sites_ip_info=$(jq -r .[$i].sites_ip_info <<< $vlans)
    if [ -n "$sites_ip_info" ]; then
        echo "$sites_ip_info" | async_exec ./async_job/apply_sites_ip.sh "$ID" "$os_code" "false"
    fi
=======
>>>>>>> 4e4ad52ae441fd1b46626d10333a2bf70a54c704
    ./set_host.sh "$router" "$vlan" "$mac" "$vm_name" "$ip"
    let i=$i+1
done
