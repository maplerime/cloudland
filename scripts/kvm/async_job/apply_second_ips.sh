#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 4 ] && echo "$0 <vm_ID> <mac> <os_code> <update_meta> [primary_ip] [gateway]" && exit -1

ID=$1
vm_ID=inst-$ID
mac=$2
os_code=$3
update_meta=$4
primary_ip=$5
gateway=${6%/*}
more_addresses=$(cat)
naddrs=$(jq length <<< "$more_addresses")

if [ "$os_code" = "windows" ]; then
    wait_qemu_ping $ID 10
    if [ -n "$primary_ip" ]; then
        read -d'\n' -r ip netmask  < <(ipcalc -nb $primary_ip | awk '/Address/ {print $2} /Netmask/ {print $2}')
        virsh qemu-agent-command "$vm_ID" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\netsh.exe","arg":["interface","ipv4","set","address","name=eth0","addr='"$ip"'","mask='"$netmask"'","gateway='"$gateway"'"],"capture-output":true}}'
    fi
    i=0
    while [ $i -lt $naddrs ]; do
        read -d'\n' -r address < <(jq -r ".[$i]" <<< "$more_addresses")
        read -d'\n' -r ip netmask  < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
        virsh qemu-agent-command "$vm_ID" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\netsh.exe","arg":["interface","ipv4","add","address","name=eth0","addr='"$ip"'","mask='"$netmask"'"],"capture-output":true}}'
        let i=$i+1
    done
elif [ "$os_code" = "linux" -a "$update_meta" = "true" ]; then
    i=0
    while [ $i -lt $naddrs ]; do
        read -d'\n' -r address < <(jq -r ".[$i]" <<< "$more_addresses")
        read -d'\n' -r ip netmask < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
        second_addrs_json="$second_addrs_json,{
            \"type\": \"ipv4\",
            \"ip_address\": \"$ip\",
            \"netmask\": \"$netmask\",
            \"link\": \"eth0\",
            \"id\": \"network0\"
        }"
        let i=$i+1
    done
    tmp_mnt=/tmp/mnt-$vm_ID
    working_dir=/tmp/$vm_ID
    latest_dir=$working_dir/openstack/latest
    mkdir -p $tmp_mnt $working_dir
    mount ${cache_dir}/meta/${vm_ID}.iso $tmp_mnt
    cp -r $tmp_mnt/* $working_dir
    net_json=$(cat $latest_dir/network_data.json)
    if [ -n "$primary_ip" ]; then
        read -d'\n' -r ip netmask  < <(ipcalc -nb $primary_ip | awk '/Address/ {print $2} /Netmask/ {print $2}')
        net_json=$(jq --arg ip "$ip" --arg netmask "$netmask" --arg gateway "$gateway" '.networks[] |= (select(.id == "network0") | .ip_address = $ip | .netmask = $netmask | .routes[0].gateway = $gateway)' <<< "$net_json")
    fi
    networks="[$(jq -r '.networks[] | select(.id == "network0" and .routes[0].network == "0.0.0.0")' <<<$net_json)$second_addrs_json]"
    echo "$net_json" | jq --argjson new_networks "$networks" '.networks |= (map(select(.id != "network0")) + $new_networks)' >$latest_dir/network_data.json
    umount $tmp_mnt
    mkisofs -quiet -R -J -V config-2 -o ${cache_dir}/meta/${vm_ID}.iso $working_dir &> /dev/null
    rm -rf $working_dir
    virsh qemu-agent-command "$vm_ID" '{"execute": "guest-exec", "arguments": {"path": "/usr/bin/cloud-init", "arg": ["clean", "--reboot"]}}'
fi
