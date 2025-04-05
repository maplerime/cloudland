#!/bin/bash -xv

cd `dirname $0`
source ../cloudrc

[ $# -lt 1 ] && echo "$0 <router>" && exit -1

ID=$1
router=router-$ID
sites=$(cat)
nsite=$(jq length <<< $sites)
i=0
while [ $i -lt $nsite ]; do
    read -d'\n' -r site_ID site_vlan < <(jq -r ".[$i].site_id, .[$i].site_vlan" <<<$sites)
    site_addrs=$(jq -r ".[$i].addresses" <<<$sites)
    queue_id=$(($site_ID % 4294967295))
    naddr=$(jq length <<<$site_addrs)
    j=0
    while [ $j -lt $naddr ]; do
        read -d'\n' -r address < <(jq -r ".[$j]" <<<$site_addrs)
        ext_dev=te-${ID}-${site_vlan}
	ext_ip=${address%/*}
        ip netns exec $router ip addr del $address dev $ext_dev
        let j=$j+1
    done
    ip netns exec $router iptables -t mangle -D PREROUTING -m set --match-set site-$site_ID dst -j TEE --gateway $int_ip
    ip netns exec $router iptables -D INPUT -m set --match-set site-$site_ID dst -j DROP
    ip netns exec $router iptables -t mangle -D PREROUTING -m set --match-set site-$site_ID src -j NFQUEUE --queue-num $queue_id
    ip netns exec $router setsid python3 ./forward_pkt.py "$queue_id" "$ext_dev" "$gw_mac" &
    ip netns exec $router ipset destroy site-$site_ID
    ps -ef | grep "forward_pkt.py $queue_id" | awk '{print $2}' | xargs kill -9
    let i=$i+1
done
