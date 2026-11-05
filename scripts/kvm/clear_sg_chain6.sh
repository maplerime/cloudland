#!/bin/bash
# Caller holds the firewall lock. Delete only this NIC's IPv6 rules.
vnic=$1
chain_in=secgroup6-in-$vnic
chain_out=secgroup6-out-$vnic
chain_as=secgroup6-as-$vnic
ip6tables -w -D FORWARD -m physdev --physdev-out "$vnic" --physdev-is-bridged -j "$chain_in" 2>/dev/null
ip6tables -w -D FORWARD -m physdev --physdev-in "$vnic" --physdev-is-bridged -j "$chain_out" 2>/dev/null
ip6tables -w -D INPUT -m physdev --physdev-in "$vnic" --physdev-is-bridged -j "$chain_out" 2>/dev/null
for chain in "$chain_in" "$chain_out" "$chain_as"; do
    ip6tables -w -F "$chain" 2>/dev/null
done
for chain in "$chain_in" "$chain_out" "$chain_as"; do
    ip6tables -w -X "$chain" 2>/dev/null
done
ipset destroy sgas6-$vnic 2>/dev/null
exit 0
