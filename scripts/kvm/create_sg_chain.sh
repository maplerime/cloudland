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
    more_addresses=$(cat)
    naddrs=$(jq length <<< $more_addresses)
    if [ $naddrs -gt 0 ]; then
        for i in {1..300}; do
            bridge=$(readlink /sys/class/net/$vnic/master | xargs basename)
            [ -n "$bridge" ] && break
            sleep 2
        done
        i=0
        while [ $i -lt $naddrs ]; do
            read -d'\n' -r address < <(jq -r ".[$i]" <<<$more_addresses)
            read -d'\n' -r ip netmask < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
            apply_fw -I $chain_as -s $ip/32 -m mac --mac-source $mac -j RETURN
            ./send_spoof_arp.py $bridge $ip $mac &
            let i=$i+1
        done
    fi
fi

apply_fw -N $chain_out
apply_fw -F $chain_out
apply_fw -I secgroup-chain -m physdev --physdev-in $vnic --physdev-is-bridged -j $chain_out
apply_fw -I INPUT -m physdev --physdev-in $vnic --physdev-is-bridged -j $chain_out
apply_fw -A $chain_out -j $chain_as
apply_fw -A $chain_out -m state --state RELATED,ESTABLISHED -j RETURN
apply_fw -A $chain_out -m state --state INVALID -j DROP
apply_fw -A $chain_out -j DROP
