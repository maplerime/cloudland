#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Enable nftables mode
USE_NFTABLES=true

[ $# -lt 1 ] && echo "$0 <interface> [add|delete]" && exit -1

vnic=$1
act=$2
action='-I'
[ "$act" = "delete" ] && action='-D'

chain_in=secgroup-in-$vnic
chain_out=secgroup-out-$vnic

function allow_ipv4()
{
    chain=$1
    args=$2
    proto=$3
    min=$4
    max=$5
    
    # Convert args to nftables format
    nft_args=$(echo "$args" | sed 's/-s \([^ ]*\)/ip saddr \1/' | sed 's/-d \([^ ]*\)/ip daddr \1/')
    
    if [ -z "$min" -a -z "$max" ]; then
        apply_fw $action $chain $proto $nft_args ct state new return
    elif [ "$max" -eq "$min" ]; then
        apply_fw $action $chain $proto $nft_args ct state new $proto dport $max return
    elif [ "$max" -gt "$min" ]; then
        apply_fw $action $chain $proto $nft_args ct state new $proto dport $min-$max return
    fi
}

function allow_icmp()
{
    chain=$1
    args=$2
    ptype=$3
    pcode=$4
    
    # Convert args to nftables format
    nft_args=$(echo "$args" | sed 's/-s \([^ ]*\)/ip saddr \1/' | sed 's/-d \([^ ]*\)/ip daddr \1/')
    
    if [ "$ptype" != "-1" ]; then
        typecode=$ptype
        [ "$pcode" != "-1" ] && typecode=$ptype/$pcode
        # For nftables, ICMP type/code matching is done differently
        # This is a simplified approach
        nft_args="$nft_args icmp type $ptype"
    fi
    apply_fw $action $chain icmp $nft_args return
}

sec_data=$(cat)
i=0
len=$(jq length <<< $sec_data)
while [ $i -lt $len ]; do
    read -d'\n' -r direction remote_ip protocol port_min port_max < <(jq -r ".[$i].direction, .[$i].remote_ip, .[$i].protocol, .[$i].port_min, .[$i].port_max" <<<$sec_data)
    chain=$chain_in
    [ "$direction" = "egress" ] && chain=$chain_out
    args=""
    if [ -n "$remote_ip" ]; then
        [ "$direction" = "ingress" ] && args="ip saddr $remote_ip"
        [ "$direction" = "egress" ] && args="ip daddr $remote_ip"
    fi
    case "$protocol" in
        "tcp")
            allow_ipv4 "$chain" "$args" "tcp" "$port_min" "$port_max"
            ;;
        "udp")
            allow_ipv4 "$chain" "$args" "udp" "$port_min" "$port_max"
            ;;
        "icmp")
            ptype=$port_min
            pcode=$port_max
            allow_icmp "$chain" "$args" "$ptype" "$pcode"
            ;;
        *)
            # Convert args to nftables format
            nft_args=$(echo "$args" | sed 's/-s \([^ ]*\)/ip saddr \1/' | sed 's/-d \([^ ]*\)/ip daddr \1/')
            apply_fw "$action" "$chain" "$protocol" "$nft_args" return
            ;;
    esac
    let i=$i+1
done