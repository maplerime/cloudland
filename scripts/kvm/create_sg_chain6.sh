#!/bin/bash
# IPv6 companion to create_sg_chain.sh; caller holds the firewall lock.
set -e
cd "$(dirname "$0")"
vnic=$1
ip=${2%%/*}
mac=$3
allow_spoofing=$4
more_addresses=$(cat)
chain_in=secgroup6-in-$vnic
chain_out=secgroup6-out-$vnic
chain_as=secgroup6-as-$vnic

fw6() {
    local action=$1 chain=$2
    shift 2
    case "$action" in
        -N) ip6tables -w -S "$chain" >/dev/null 2>&1 || ip6tables -w -N "$chain" ;;
        -I) ip6tables -w -C "$chain" "$@" 2>/dev/null || ip6tables -w -I "$chain" 1 "$@" ;;
        *) ip6tables -w "$action" "$chain" "$@" ;;
    esac
}
for chain in "$chain_in" "$chain_out" "$chain_as"; do
    fw6 -N "$chain"
    fw6 -F "$chain"
done
fw6 -I FORWARD -m physdev --physdev-out "$vnic" --physdev-is-bridged -j "$chain_in"
fw6 -I FORWARD -m physdev --physdev-in "$vnic" --physdev-is-bridged -j "$chain_out"
fw6 -I INPUT -m physdev --physdev-in "$vnic" --physdev-is-bridged -j "$chain_out"

# Mandatory ICMPv6 errors (especially Packet Too Big) and on-link neighbour
# discovery must work even with a restrictive application security group.
for kind in 1 2 3 4; do
    fw6 -A "$chain_in" -p ipv6-icmp --icmpv6-type "$kind" -j RETURN
done
for kind in 134 135 136; do
    fw6 -A "$chain_in" -p ipv6-icmp --icmpv6-type "$kind" -m hl --hl-eq 255 -j RETURN
done
fw6 -A "$chain_in" -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
fw6 -A "$chain_in" -m conntrack --ctstate INVALID -j DROP
fw6 -A "$chain_in" -j DROP

if [ "$allow_spoofing" = true ]; then
    fw6 -A "$chain_as" -j RETURN
else
    ipset create sgas6-$vnic hash:ip,mac family inet6 -exist
    ipset flush sgas6-$vnic
    # Linux/Windows both need the MAC-derived link-local address during NDP.
    IFS=: read -r a b c d e f <<< "$mac"
    printf -v link_local 'fe80::%x%s:%sff:fe%s:%s%s' "$((16#$a ^ 2))" "$b" "$c" "$d" "$e" "$f"
    ipset add sgas6-$vnic "$link_local,$mac" -exist
    [[ "$ip" != *:* ]] || ipset add sgas6-$vnic "$ip,$mac" -exist
    while IFS= read -r extra; do
        [[ "$extra" != *:* ]] || ipset add sgas6-$vnic "${extra%%/*},$mac" -exist
    done < <(jq -r '.[]?' <<< "$more_addresses")
    fw6 -A "$chain_as" -m set --match-set sgas6-$vnic src,src -j RETURN
    fw6 -A "$chain_as" -j DROP
fi
# Only Duplicate Address Detection may use the unspecified source address.
fw6 -A "$chain_out" -s ::/128 -d ff02::1:ff00:0/104 -m mac --mac-source "$mac" -p ipv6-icmp --icmpv6-type 135 -m hl --hl-eq 255 -j RETURN
# Windows may randomise its link-local address; it is only usable for NDP.
for kind in 133 135 136; do
    fw6 -A "$chain_out" -s fe80::/10 -m mac --mac-source "$mac" -p ipv6-icmp --icmpv6-type "$kind" -m hl --hl-eq 255 -j RETURN
done
fw6 -A "$chain_out" -j "$chain_as"
# Guests are hosts, and may not advertise themselves as routers.
fw6 -A "$chain_out" -p ipv6-icmp --icmpv6-type 134 -j DROP
fw6 -A "$chain_out" -p ipv6-icmp --icmpv6-type 137 -j DROP
for kind in 133 135 136; do
    fw6 -A "$chain_out" -p ipv6-icmp --icmpv6-type "$kind" -m hl --hl-eq 255 -j RETURN
done
for kind in 1 2 3 4; do
    fw6 -A "$chain_out" -p ipv6-icmp --icmpv6-type "$kind" -j RETURN
done
fw6 -A "$chain_out" -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
fw6 -A "$chain_out" -m conntrack --ctstate INVALID -j DROP
fw6 -A "$chain_out" -j DROP
