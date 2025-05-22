#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && echo "$0 <router> <int_ip> <os_code> <update_meta> <vm_ID>" && exit -1

ID=$1
router=router-$ID
int_ip=${2%/*}
os_code=$3
update_meta=$4
inst_ID=$5
vm_ID=inst-$5

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
        site_addrs=$(jq -r ".[$i].addresses" <<<$sites)
        naddr=$(jq length <<<$site_addrs)
        j=0
        while [ $j -lt $naddr ]; do
            read -d'\n' -r address < <(jq -r ".[$j]" <<<$site_addrs)
	    ext_ip=${address%/*}
            virsh qemu-agent-command "$vm_ID" "{\"execute\":\"guest-exec\",\"arguments\":{\"path\":\"C:\Windows\System32\netsh.exe\",\"arg\":[\"interface\",\"ipv4\",\"add\",\"address\",\"eth0\",\"$ext_ip\",\"255.255.255.255\"], \"capture-output\": true}}"
        done
    done
    sleep 5
fi

i=0
while [ $i -lt $nsite ]; do
    read -d'\n' -r site_ID site_vlan gateway < <(jq -r ".[$i].site_id, .[$i].site_vlan, .[$i].gateway" <<<$sites)
    site_addrs=$(jq -r ".[$i].addresses" <<<$sites)
    gateway=${gateway%/*}
    ../create_route_table.sh $ID $site_vlan
    queue_id=$(($site_ID % 4294967295))
    naddr=$(jq length <<<$site_addrs)
    ip netns exec $router ipset create site-$site_ID nethash
    j=0
    while [ $j -lt $naddr ]; do
        read -d'\n' -r address < <(jq -r ".[$j]" <<<$site_addrs)
        ext_dev=te-${ID}-${site_vlan}
	ext_ip=${address%/*}
        if [ "$os_code" = "windows" ]; then
            virsh qemu-agent-command "$vm_ID" "{\"execute\":\"guest-exec\",\"arguments\":{\"path\":\"C:\Windows\System32\netsh.exe\",\"arg\":[\"interface\",\"ipv4\",\"add\",\"address\",\"eth0\",\"$ext_ip\",\"255.255.255.255\"], \"capture-output\": true}}"
	fi
	ip netns exec $router ipset add site-$site_ID $ext_ip
        ip netns exec $router ip addr add $address dev $ext_dev
        ip netns exec $router arping -c 3 -U -I $ext_dev $ext_ip &
    	sites_json="$sites_json,{
            \"type\": \"ipv4\",
            \"ip_address\": \"$ext_ip\",
            \"netmask\": \"255.255.255.255\",
            \"link\": \"eth0\",
            \"id\": \"network0\"
        }"
	gw_mac=$(ip netns exec $router arping -c 1 -I $ext_dev $gateway | grep 'Unicast reply' | awk '{print $5}' | tr -d '[]')
        let j=$j+1
    done
    ip netns exec $router iptables -t mangle -D PREROUTING -m set --match-set site-$site_ID dst -j TEE --gateway $int_ip
    ip netns exec $router iptables -t mangle -I PREROUTING -m set --match-set site-$site_ID dst -j TEE --gateway $int_ip
    ip netns exec $router iptables -D INPUT -m set --match-set site-$site_ID dst -j DROP
    ip netns exec $router iptables -I INPUT -m set --match-set site-$site_ID dst -j DROP
    ip netns exec $router iptables -t mangle -D PREROUTING -m set --match-set site-$site_ID src -j NFQUEUE --queue-num $queue_id
    ip netns exec $router iptables -t mangle -I PREROUTING -m set --match-set site-$site_ID src -j NFQUEUE --queue-num $queue_id
    ip netns exec $router python3 ./forward_pkt.py "$queue_id" "$ext_dev" "$gw_mac" >/dev/null 2>&1 &
    let i=$i+1
done

if [ "$os_code" = "linux" -a "$update_meta" = "true" ]; then
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
