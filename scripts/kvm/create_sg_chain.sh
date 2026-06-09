#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <interface> <ip> <mac> <allow_spoofing>" && exit -1

vnic=$1
ip=${2%%/*}
mac=$3
allow_spoofing=$4

apply_fw -I FORWARD -m physdev --physdev-out $vnic --physdev-is-bridged -j secgroup-chain
apply_fw -I FORWARD -m physdev --physdev-in $vnic --physdev-is-bridged -j secgroup-chain

chain_in=secgroup-in-$vnic
apply_fw -N $chain_in
apply_fw -F $chain_in
apply_fw -I secgroup-chain -m physdev --physdev-out $vnic --physdev-is-bridged -j $chain_in
apply_fw -A $chain_in -m state --state RELATED,ESTABLISHED -j RETURN
apply_fw -A $chain_in -m state --state INVALID -j DROP
apply_fw -A $chain_in -j DROP

chain_out=secgroup-out-$vnic
chain_as=secgroup-as-$vnic
apply_fw -N $chain_as
apply_fw -F $chain_as
if [ "$allow_spoofing" = true ]; then
    apply_fw -I $chain_as -j RETURN
else
    apply_fw -A $chain_as -s $ip/32 -m mac --mac-source $mac -j RETURN
    apply_fw -A $chain_as -j DROP
fi

more_addresses=$(cat)
naddrs=$(jq length <<< $more_addresses)

if [ "$allow_spoofing" != true ]; then
    # nftables: vmap dispatch + per-vnic set for ARP filtering with rate limit
    nft add table bridge cloudland 2>/dev/null
    nft add chain bridge cloudland forward '{ type filter hook forward priority 0 ; policy accept ; }' 2>/dev/null
    nft add map bridge cloudland arp_dispatch '{ type ifname : verdict ; }' 2>/dev/null
    nft list chain bridge cloudland forward 2>/dev/null | grep -q "arp_dispatch" || {
        nft add rule bridge cloudland forward ether type 0x0806 meta iifname vmap @arp_dispatch
        nft add rule bridge cloudland forward ether daddr ff:ff:ff:ff:ff:ff meta iifname vmap @arp_dispatch
        nft add rule bridge cloudland forward ether daddr 01:00:00:00:00:00/01:00:00:00:00:00 meta iifname vmap @arp_dispatch
    }
    nft add chain bridge cloudland arp-$vnic
    nft flush chain bridge cloudland arp-$vnic
    nft add set bridge cloudland set-$vnic '{ type ether_addr . ipv4_addr ; }' 2>/dev/null
    nft flush set bridge cloudland set-$vnic 2>/dev/null
    nft add element bridge cloudland arp_dispatch { $vnic : jump arp-$vnic }
    ip_count=$((1 + naddrs))
    rate_pps=$((${ip_count} * 5))
    burst_pkts=$((${ip_count} * 5 + 20))
    nft add rule bridge cloudland arp-$vnic ether type 0x0806 limit rate $rate_pps/second burst $burst_pkts packets arp saddr ether . arp saddr ip @set-$vnic accept
    nft add rule bridge cloudland arp-$vnic ether type 0x0806 drop
    nft add rule bridge cloudland arp-$vnic limit rate $rate_pps/second burst $burst_pkts packets accept
    nft add rule bridge cloudland arp-$vnic drop
    nft add element bridge cloudland set-$vnic { $mac . $ip }

    if [ $naddrs -gt 0 ]; then
        for i in {1..300}; do
            bridge=$(readlink /sys/class/net/$vnic/master | xargs basename)
            [ -n "$bridge" ] && break
            sleep 2
        done
        i=0
        while [ $i -lt $naddrs ]; do
            read -d'\n' -r address < <(jq -r ".[$i]" <<<$more_addresses)
            read -d'\n' -r extra_ip netmask < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
            apply_fw -I $chain_as -s $extra_ip/32 -m mac --mac-source $mac -j RETURN
            nft add element bridge cloudland set-$vnic { $mac . $extra_ip }
            ./send_spoof_arp.py $bridge $extra_ip $mac &
            let i=$i+1
        done
    fi
fi

apply_fw -N $chain_out
apply_fw -F $chain_out
apply_fw -I secgroup-chain -m physdev --physdev-in $vnic --physdev-is-bridged -j $chain_out
apply_fw -I INPUT -m physdev --physdev-in $vnic --physdev-is-bridged -j $chain_out
apply_fw -A $chain_out -m state --state RELATED,ESTABLISHED -j RETURN
apply_fw -A $chain_out -j $chain_as
apply_fw -A $chain_out -m state --state INVALID -j DROP
apply_fw -A $chain_out -j DROP
