#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 1 ] && echo "$0 <interface> [add|delete]" && exit -1

vnic=$1
act=$2
# Build all rules for this nic's chains and apply them in a single
# iptables-restore transaction (noflush) instead of one fork per rule.
# add -> insert at head (-I chain 1); delete -> -D chain.
if [ "$act" = "delete" ]; then
    op="-D"
    tail=""
else
    op="-I"
    tail=" 1"
fi

chain_in=secgroup-in-$vnic
chain_out=secgroup-out-$vnic

rules=""

# emit <chain> <rest-of-rule>
function emit()
{
    chain=$1
    shift
    rules="${rules}${op} ${chain}${tail} $*"$'\n'
}

function allow_ipv4()
{
    chain=$1
    args=$2
    proto=$3
    min=$4
    max=$5
    if [ -z "$min" -a -z "$max" ]; then
        emit "$chain" "-p $proto $args -m conntrack --ctstate NEW -j RETURN"
    elif [ "$max" -eq "$min" ]; then
        emit "$chain" "-p $proto -m $proto -m conntrack --ctstate NEW --dport $max $args -j RETURN"
    elif [ "$max" -gt "$min" ]; then
        emit "$chain" "-p $proto -m $proto -m conntrack --ctstate NEW --dport $min:$max $args -j RETURN"
    fi
}

function allow_icmp()
{
    chain=$1
    args=$2
    ptype=$3
    pcode=$4
    if [ "$ptype" != "-1" ]; then
        typecode=$ptype
        [ "$pcode" != "-1" ] && typecode=$ptype/$pcode
        args="$args --icmp-type $typecode"
    fi
    emit "$chain" "-p icmp $args -j RETURN"
}

sec_data=$(cat)
i=0
len=$(jq length <<< $sec_data)
while [ $i -lt $len ]; do
    # Read the 5 fields into an array so empty fields (e.g. blank remote_ip)
    # keep their position instead of collapsing and shifting later fields.
    mapfile -t fields < <(jq -r ".[$i] | .direction, .remote_ip, .protocol, .port_min, .port_max" <<<$sec_data)
    direction=${fields[0]}
    remote_ip=${fields[1]}
    protocol=${fields[2]}
    port_min=${fields[3]}
    port_max=${fields[4]}
    chain=$chain_in
    [ "$direction" = "egress" ] && chain=$chain_out
    args=""
    if [ -n "$remote_ip" ]; then
        [ "$direction" = "ingress" ] && args="-s $remote_ip"
        [ "$direction" = "egress" ] && args="-d $remote_ip"
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
            emit "$chain" "-p $protocol $args -j RETURN"
            ;;
    esac
    let i=$i+1
done

# Apply all collected rules atomically in one transaction (chains already
# exist, so use --noflush to only append/delete without touching other chains).
if [ -n "$rules" ]; then
    printf '*filter\n%sCOMMIT\n' "$rules" | iptables-restore --noflush
fi
