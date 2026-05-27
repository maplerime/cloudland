#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Limit ARP packet rate on a VM interface using tc ingress policing
# Rate is auto-calculated from IP count, burst is fixed:
#   per IP: 5 ARP/s (2kbit), burst: 10 packets (420 bytes)
# Usage:
# $0 <nic_name> add <ip_count>    # Add ARP rate limit scaled by IP count
# $0 <nic_name> delete            # Remove ARP rate limit

[ $# -lt 2 ] && echo "$0 <nic_name> add|delete [ip_count]" && exit -1

nic_name=$1
action=$2
ip_count=${3:-1}

if [ "$action" = "delete" ]; then
    tc filter del dev $nic_name parent ffff: protocol 0x0806 2>/dev/null
    # Remove ingress qdisc only if no other filters remain
    filter_count=$(tc filter show dev $nic_name parent ffff: 2>/dev/null | grep -c "filter" || true)
    [ "$filter_count" -eq 0 ] && tc qdisc del dev $nic_name handle ffff: ingress 2>/dev/null
elif [ "$action" = "add" ]; then
    # rate: per IP 5 ARP/s (2kbit), burst: per-IP rate + 10 extra ARP packets
    rate=$((${ip_count} * 2))kbit
    burst=$((${ip_count} * 5 * 42 + 20 * 42))
    # Add ingress qdisc if not present
    tc qdisc add dev $nic_name handle ffff: ingress 2>/dev/null || true
    # Remove existing ARP filter if any
    tc filter del dev $nic_name parent ffff: protocol 0x0806 2>/dev/null || true
    # Add ARP rate limiter
    tc filter add dev $nic_name parent ffff: protocol 0x0806 \
        u32 match u32 0 0 \
        action police rate $rate burst $burst \
        conform-exceed drop/ok
else
    echo "Unknown action: $action (use add or delete)"
    exit -1
fi
