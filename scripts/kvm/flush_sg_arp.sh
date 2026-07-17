#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Broadcast useless MAC ARP and flush conntrack for a vnic
# Usage: flush_sg_arp.sh <vnic> [vnic2 ...]

for vnic in $*; do
    bridge=$(readlink /sys/class/net/$vnic/master 2>/dev/null | xargs basename 2>/dev/null)
    [ -z "$bridge" ] && continue
    # Extract ip,mac pairs from the anti-spoofing ipset
    entries=$(ipset list sgas-$vnic 2>/dev/null | awk '/^[0-9]/ {print $1}')
    [ -z "$entries" ] && continue
    ips=""
    useless_mac=""
    for entry in $entries; do
        ip=${entry%%,*}
        mac=${entry#*,}
        [ -z "$useless_mac" ] && useless_mac=$(echo $mac | sed 's/^52:54/fe:54/')
        ips="$ips $ip"
        conntrack -D -s $ip 2>/dev/null
        conntrack -D -d $ip 2>/dev/null
    done
    [ -n "$ips" ] && ./send_spoof_arp.py $bridge $useless_mac $ips
done
