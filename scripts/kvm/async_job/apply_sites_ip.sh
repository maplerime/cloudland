#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 2 ] && echo "$0 <router> <int_ip> <update_meta> <vm_ID>" && exit -1

ID=$1
router=router-$ID
int_ip=${2%/*}
update_meta=$3
vm_ID=inst-$4
sites=$(cat)
nsite=$(jq length <<< $sites)
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
	ip netns exec $router ipset add site-$site_ID $ext_ip
        ip netns exec $router ip addr add $address dev $ext_dev
        ip netns exec $router arping -c 3 -U -I $ext_dev $ext_ip
        if [ $j -eq 0 ]; then
            sites_json="$sites_json,"
        fi
	sites_json="$sites_json,{
            \"type\": \"ipv4\",
            \"ip_address\": \"$ext_ip/32\",
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
    ip netns exec $router setsid python3 ./forward_pkt.py "$queue_id" "$ext_dev" "$gw_mac" &
    let i=$i+1
done

if [ "$update_meta" = "true" ]; then
    tmp_mnt=/tmp/mnt-$vm_ID
    working_dir=/tmp/$vm_ID
    mkdir -p $tmp_mnt $working_dir
    virsh qemu-agent-command "$vm_ID" '{"execute":"cloud-init clean"}'
    mount ${cache_dir}/meta/${vm_ID}.iso $tmp_mnt
    cp -r $tmp_mnt/* $working_dir
    net_json=$(cat $working_dir/network_data.json)
    networks="[$(jq -r .networks[0] <<<$net_json)$sites_json]" 
    echo "$net_json" | jq --argjson new_networks "$networks" '.networks |= (map(select(.id == "network0")) + $new_networks)'
fi
