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
    # ipset: single hash:ip,mac set replaces per-IP RETURN rules
    ipset create sgas-$vnic hash:ip,mac -exist
    ipset flush sgas-$vnic
    ipset add sgas-$vnic $ip,$mac -exist
    apply_fw -A $chain_as -m set --match-set sgas-$vnic src,src -j RETURN
    apply_fw -A $chain_as -j DROP
fi

more_addresses=$(cat)
naddrs=$(jq length <<< $more_addresses)

if [ "$allow_spoofing" != true ]; then
    # ipset holds the (ip,mac) anti-spoofing pairs. It applies regardless of
    # whether the tap exists yet (e.g. a shutoff VM being reapplied): the rule
    # set is ready before the tap appears. NB: bridge FDB / flood / nftables ARP
    # filtering are handled by apply_arp_filter.sh OUTSIDE the iptables lock,
    # because they depend on the tap being present and would otherwise block.
    extra_ips=""
    if [ $naddrs -gt 0 ]; then
        extra_ips=$(jq -r '.[] | split("/")[0]' <<<$more_addresses)
    fi
    ipset_restore=""
    for extra_ip in $extra_ips; do
        ipset_restore="${ipset_restore}add sgas-$vnic $extra_ip,$mac -exist"$'\n'
    done
    [ -n "$ipset_restore" ] && printf '%s' "$ipset_restore" | ipset restore -exist
fi

apply_fw -N $chain_out
apply_fw -F $chain_out
apply_fw -I secgroup-chain -m physdev --physdev-in $vnic --physdev-is-bridged -j $chain_out
apply_fw -I INPUT -m physdev --physdev-in $vnic --physdev-is-bridged -j $chain_out
apply_fw -A $chain_out -m state --state RELATED,ESTABLISHED -j RETURN
apply_fw -A $chain_out -j $chain_as
apply_fw -A $chain_out -m state --state INVALID -j DROP
apply_fw -A $chain_out -j DROP
