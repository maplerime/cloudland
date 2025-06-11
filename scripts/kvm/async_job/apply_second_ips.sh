#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 4 ] && echo "$0 <vm_ID> <mac> <os_code> <update_meta>" && exit -1

ID=$1
vm_ID=inst-$ID
mac=$2
os_code=$3
update_meta=$4
more_addresses=$(cat)
naddrs=$(jq length <<< $more_addresses)
[ $naddrs -eq 0 ] && exit 0 

vnic=tap$(echo $mac | cut -d: -f4- | tr -d :)
for i in {1..30}; do
    bridge=$(readlink /sys/class/net/$vnic/master | xargs basename)
    [ -n "$bridge" ] && break
    sleep 2
done
chain_as=secgroup-as-$vnic
i=0
while [ $i -lt $naddrs ]; do
    read -d'\n' -r address < <(jq -r ".[$i]" <<<$more_addresses)
    read -d'\n' -r ip netmask < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
    apply_fw -I $chain_as -s $ip/32 -m mac --mac-source $mac -j RETURN
    second_addrs_json="$second_addrs_json,{
        \"type\": \"ipv4\",
        \"ip_address\": \"$ip\",
        \"netmask\": \"$netmask\",
        \"link\": \"eth0\",
        \"id\": \"network0\"
    }"
    ../send_spoof_arp.py $bridge $ip $mac
    let i=$i+1
done

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
    while [ $i -lt $naddrs ]; do
        read -d'\n' -r address < <(jq -r ".[$i]" <<<$more_addresses)
        read -d'\n' -r ip netmask  < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
        virsh qemu-agent-command "$vm_ID" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\netsh.exe","arg":["interface","ipv4","add","address","name=eth0","addr='"$ip"'","mask='"$netmask"'"],"capture-output":true}}'
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
    networks="[$(jq -r .networks[0] <<<$net_json)$second_addrs_json]" 
    echo "$net_json" | jq --argjson new_networks "$networks" '.networks |= (map(select(.id != "network0")) + $new_networks)' >$latest_dir/network_data.json
    umount $tmp_mnt
    mkisofs -quiet -R -J -V config-2 -o ${cache_dir}/meta/${vm_ID}.iso $working_dir &> /dev/null
    rm -rf $working_dir
    virsh qemu-agent-command "$vm_ID" '{"execute": "guest-exec", "arguments": {"path": "/usr/bin/cloud-init", "arg": ["clean", "--reboot"]}}'
fi
