#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Apply bridge FDB pinning, unknown-unicast flood suppression and nftables
# ARP anti-spoof filtering for a VM tap. These all require the tap to be
# present and enslaved to a bridge, so this script is run OUTSIDE the iptables
# flock (a long wait here must not stall other VMs' security-group updates).
#
# Reads the more_addresses JSON array from stdin (same format as create_sg_chain.sh).
#
# Usage: apply_arp_filter.sh <interface> <ip> <mac> <allow_spoofing>

[ $# -lt 3 ] && echo "$0 <interface> <ip> <mac> <allow_spoofing>" && exit -1

vnic=$1
ip=${2%%/*}
mac=$3
allow_spoofing=$4

[ "$allow_spoofing" = true ] && exit 0

more_addresses=$(cat)
naddrs=$(jq length <<< $more_addresses)
extra_ips=""
if [ $naddrs -gt 0 ]; then
    extra_ips=$(jq -r '.[] | split("/")[0]' <<<$more_addresses)
fi

# Wait briefly for the tap to be enslaved to its bridge (covers the attach
# race where the tap appears shortly after). If the VM is shut off the tap
# never appears, so give up quickly instead of blocking.
for i in {1..5}; do
    bridge=$(readlink /sys/class/net/$vnic/master 2>/dev/null | xargs basename 2>/dev/null)
    [ -n "$bridge" ] && break
    sleep 1
done
[ -z "$bridge" ] && exit 0

# Suppress unknown-unicast flooding to this VM port: pin the VM MAC into the
# bridge FDB (so known unicast still reaches it) then disable flood. Keep
# mcast_flood/bcast_flood on (DHCP/ARP/multicast must still work).
bridge fdb replace $mac dev $vnic master static
bridge link set dev $vnic flood off

# nftables: vmap dispatch + per-vnic set for ARP anti-spoof filtering
nft add table bridge cloudland 2>/dev/null
nft add chain bridge cloudland forward '{ type filter hook forward priority 0 ; policy accept ; }' 2>/dev/null
nft add map bridge cloudland arp_dispatch '{ type ifname : verdict ; }' 2>/dev/null
nft list chain bridge cloudland forward 2>/dev/null | grep -q "arp_dispatch" || {
    # Only ARP is dispatched to the per-vnic chain for anti-spoof checking.
    # Non-ARP BUM (broadcast/multicast) falls through to the chain policy
    # (accept) - add a BUM rate-limit dispatch here if throttling is needed.
    nft add rule bridge cloudland forward ether type 0x0806 meta iifname vmap @arp_dispatch
}
nft add chain bridge cloudland arp-$vnic
nft flush chain bridge cloudland arp-$vnic
nft add set bridge cloudland set-$vnic '{ type ether_addr . ipv4_addr ; }' 2>/dev/null
nft flush set bridge cloudland set-$vnic 2>/dev/null
nft add element bridge cloudland arp_dispatch { $vnic : jump arp-$vnic }
# Only ARP reaches this chain, so: legit (mac,ip) accept, everything else drop.
nft add rule bridge cloudland arp-$vnic arp saddr ether . arp saddr ip @set-$vnic counter accept
nft add rule bridge cloudland arp-$vnic counter drop
# Batch all set elements (primary + extras) into a single nft call
nft_elements="$mac . $ip"
for extra_ip in $extra_ips; do
    nft_elements="$nft_elements, $mac . $extra_ip"
done
nft add element bridge cloudland set-$vnic { $nft_elements }

#[ -n "$extra_ips" ] && ./send_spoof_arp.py $bridge $mac $extra_ips &
