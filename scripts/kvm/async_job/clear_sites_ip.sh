#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 2 ] && echo "$0 <vm_ID> <os_code>" && exit -1

ID=$1
sites=$(cat)
nsite=$(jq length <<< $sites)
if [ "$os_code" = "windows" ]; then
    for i in {1..120}; do
        virsh qemu-agent-command "$vm_ID" '{"execute":"guest-ping"}'
        if [ $? -eq 0 ]; then
	    break
        fi
    done
    i=0
    while [ $i -lt $nsite ]; do
        read -d'\n' -r site_ID site_vlan < <(jq -r ".[$i].site_id, .[$i].site_vlan" <<<$sites)
        site_addrs=$(jq -r ".[$i].addresses" <<<$sites)
        naddr=$(jq length <<<$site_addrs)
        j=0
        while [ $j -lt $naddr ]; do
            read -d'\n' -r address < <(jq -r ".[$j]" <<<$site_addrs)
	    read -d'\n' -r site_ip netmask  < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
            virsh qemu-agent-command "$vm_ID" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\netsh.exe","arg":["interface","ipv4","add","address","name=eth0","addr='"$site_ip"'","mask='"$netmask"'"],"capture-output":true}}'
            let j=$j+1
        done
        let i=$i+1
    done
fi
