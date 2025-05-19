#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <os_code> <update_meta>" && exit -1

ID=$1
vm_ID=inst-$ID
os_code=$2
update_meta=$3

sites=$(cat)
nsite=$(jq length <<< $sites)
if [ "$os_code" = "windows" ]; then
    count=0
    for i in {1..240}; do
        virsh qemu-agent-command "$vm_ID" '{"execute":"guest-ping"}'
        if [ $? -eq 0 ]; then
            let count=$count+1
        fi
	[ $count -gt 10 ] && break
	sleep 5
    done
    i=0
    while [ $i -lt $nsite ]; do
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
elif [ "$os_code" = "linux" -a "$update_meta" = "true" ]; then
    tmp_mnt=/tmp/mnt-$vm_ID
    working_dir=/tmp/$vm_ID
    latest_dir=$working_dir/openstack/latest
    mkdir -p $tmp_mnt $working_dir
    mount ${cache_dir}/meta/${vm_ID}.iso $tmp_mnt
    cp -r $tmp_mnt/* $working_dir
    net_json=$(cat $latest_dir/network_data.json)
    networks="[$(jq -r .networks[0] <<<$net_json)$sites_json]" 
    echo "$net_json" | jq --argjson new_networks "$networks" '.networks |= (map(select(.id != "network0")) + $new_networks)' >$latest_dir/network_data.json
    umount $tmp_mnt
    mkisofs -quiet -R -J -V config-2 -o ${cache_dir}/meta/${vm_ID}.iso $working_dir &> /dev/null
    rm -rf $working_dir
    virsh qemu-agent-command "$vm_ID" '{"execute": "guest-exec", "arguments": {"path": "/usr/bin/cloud-init", "arg": ["clean", "--reboot"]}}'
fi
