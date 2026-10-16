#!/bin/bash
set -Eeuo pipefail
cd "$(dirname "$0")"
source ./floating6_common.sh
[[ $# == 9 ]] || { echo "$0 <router> <ext_cidr> <ext_gw> <ext_vlan> <int_cidr> <int_vlan> <fip_id> <inbound> <outbound>" >&2; exit 1; }
f6_ids "$1" "$7"
ext_cidr=$2; ext_gw=${3%/*}; ext_vlan=$4; int_cidr=$5; int_vlan=$6; inbound=$8; outbound=$9
[[ $ext_cidr == *:*/[0-9]* && $ext_gw == *:* && $ext_vlan =~ ^[1-9][0-9]*$ && $int_vlan =~ ^[1-9][0-9]*$ && $inbound =~ ^[0-9]+$ && $outbound =~ ^[0-9]+$ ]] || exit 1
(( inbound <= 20000 && outbound <= 20000 )) || exit 1
private_ip=${int_cidr%/*}; public_ip=${ext_cidr%/*}
number=$(f6_ipv4_number "$private_ip")
printf -v mapped '%s:%x:%x' "$prefix_base" "$((number >> 16))" "$((number & 65535))"
ext_dev=te-$router_id-$ext_vlan
host_dev=ext-$router_id-$ext_vlan
(( ${#host_dev} <= 15 )) || exit 1
command -v "$JOOL_BIN" >/dev/null || { echo "Jool kernel NAT64 tools are required on this hypervisor" >&2; exit 1; }
[[ -d /sys/module/jool ]] || modprobe jool
f6_lock
r ip link show dev "ns-$int_vlan" >/dev/null
[[ $(r sysctl -n net.ipv4.ip_forward) == 1 ]] || exit 1
f6_clear_one
# clear_one reads the previous assignment; restore the new server arguments.
ext_cidr=$2; ext_vlan=$4; int_cidr=$5; int_vlan=$6
f6_pool
rollback() {
    local result=$?
    trap - ERR
    echo "IPv6 floating IP creation failed; rolling back kernel resources" >&2
    f6_clear_one || echo "Rollback incomplete; NAT64 namespace retained for retry" >&2
    exit "$result"
}
ip netns add "$translator"
trap rollback ERR
t ip link set lo alias "cloudland-fip6 $ext_cidr $ext_vlan $int_cidr $int_vlan $pool"
t ip link set lo up
ip link add "$rdev" type veth peer name "$tdev"
ip link set "$rdev" netns "$router"
ip link set "$tdev" netns "$translator"
r ip link set "$rdev" mtu 1450 up
t ip link set "$tdev" mtu 1450 up
r ip -4 addr add "$raddr/31" dev "$rdev"
t ip -4 addr add "$taddr/31" dev "$tdev"
r ip -6 addr add fe80::1/64 dev "$rdev" nodad
t ip -6 addr add fe80::2/64 dev "$tdev" nodad
t sysctl -qw net.ipv4.ip_forward=1 net.ipv6.conf.all.forwarding=1
t ip -4 route replace default via "$raddr"
t ip -6 route replace default via fe80::1 dev "$tdev"
bash ./create_veth.sh "$router" "$host_dev" "$ext_dev"
r sysctl -qw net.ipv6.conf.all.forwarding=1 "net.ipv6.conf.$ext_dev.disable_ipv6=0" "net.ipv6.conf.$ext_dev.ndisc_notify=1"
r ip -6 addr add "$ext_cidr" dev "$ext_dev"
r ip -6 route replace "$prefix" via fe80::2 dev "$rdev"
r ip -6 route replace default via "$ext_gw" dev "$ext_dev" onlink table "$table"
r ip -6 rule add priority 1000 from "$prefix" lookup "$table"
r ip -4 rule add priority 100 to "$pool" lookup main
r ipset add nonat "$pool" -exist
f6_ensure_rule ip6tables nat PREROUTING -d "$public_ip/128" -m comment --comment "$tag" -j DNAT --to-destination "$mapped"
f6_ensure_rule ip6tables filter FORWARD -i "$ext_dev" -o "$rdev" -d "$mapped/128" -m comment --comment "$tag" -j ACCEPT
f6_ensure_rule ip6tables filter FORWARD -i "$rdev" -o "$ext_dev" -s "$prefix" -m comment --comment "$tag" -j ACCEPT
for kind in neighbor-solicitation neighbor-advertisement; do
    f6_ensure_rule ip6tables filter INPUT -i "$ext_dev" -p ipv6-icmp --icmpv6-type "$kind" -m comment --comment "$tag" -j ACCEPT
    f6_ensure_rule ip6tables filter INPUT -i "$rdev" -p ipv6-icmp --icmpv6-type "$kind" -m comment --comment "$tag" -j ACCEPT
done
f6_ensure_rule iptables filter FORWARD -i "$rdev" -o "ns-$int_vlan" -d "$private_ip/32" -m comment --comment "$tag" -j ACCEPT
f6_ensure_rule iptables filter FORWARD -i "ns-$int_vlan" -o "$rdev" -s "$private_ip/32" -d "$taddr/32" -m comment --comment "$tag" -j ACCEPT
if (( inbound || outbound )); then
    r tc qdisc replace dev "$rdev" root handle 1: htb default 30
    r tc class replace dev "$rdev" parent 1: classid 1:30 htb rate 100000mbit
    if (( inbound )); then
        r tc class replace dev "$rdev" parent 1: classid 1:10 htb rate "${inbound}mbit"
        r tc filter replace dev "$rdev" parent 1: protocol ipv6 prio 10 flower dst_ip "$mapped/128" flowid 1:10
    fi
    if (( outbound )); then
        r tc class replace dev "$rdev" parent 1: classid 1:20 htb rate "${outbound}mbit"
        r tc filter replace dev "$rdev" parent 1: protocol ip prio 20 flower src_ip "$private_ip/32" flowid 1:20
    fi
fi
t "$JOOL_BIN" instance add floating --netfilter --pool6 "$prefix"
for protocol in --tcp --udp --icmp; do
    t "$JOOL_BIN" -i floating pool4 add "$protocol" "$taddr" 1024-65535
done
trap - ERR
