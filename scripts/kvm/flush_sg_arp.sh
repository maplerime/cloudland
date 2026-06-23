#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Broadcast useless MAC ARP and flush conntrack for a vnic
# Usage: flush_sg_arp.sh <vnic> [vnic2 ...]

for vnic in $*; do
    chain_as=secgroup-as-$vnic
    bridge=$(readlink /sys/class/net/$vnic/master 2>/dev/null | xargs basename 2>/dev/null)
    mac=$(iptables -nL $chain_as 2>/dev/null | grep -m1 RETURN | grep -oP 'MAC\K\S+')
    [ -z "$bridge" ] || [ -z "$mac" ] && continue
    useless_mac=$(echo $mac | sed 's/^52:54/fe:54/')
    iptables -nL $chain_as 2>/dev/null | awk '/RETURN/ {print $4}' | while read ip; do
        ./send_spoof_arp.py $bridge $ip $useless_mac
        conntrack -D -s $ip 2>/dev/null
        conntrack -D -d $ip 2>/dev/null
    done
done
