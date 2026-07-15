#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 1 ] && echo "$0 <interface>" && exit -1

vnic=$1
# Build all rules for this nic's chains and apply them in a single
# iptables-restore transaction (noflush) instead of one fork per rule.
# Rules are inserted at the head (-I chain 1), ahead of the chain's
# terminating DROP built by create_sg_chain.sh.
chain_in=secgroup-in-$vnic
chain_out=secgroup-out-$vnic

rules=""

# emit <chain> <rest-of-rule>
function emit()
{
    chain=$1
    shift
    rules="${rules}-I ${chain} 1 $*"$'\n'
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
# exist, so use --noflush to only append without touching other chains).
# NOTE: iptables-restore is all-or-nothing - if any single rule is rejected
# the WHOLE batch is dropped, leaving the chain with only its skeleton DROP
# (i.e. all inbound allow rules gone -> VM traffic blackholed). Fail loud so
# this never happens silently: log to syslog on failure.
if [ -n "$rules" ]; then
    restore_err=$(printf '*filter\n%sCOMMIT\n' "$rules" | iptables-restore --noflush 2>&1)
    if [ $? -ne 0 ]; then
        logger -t apply_sg_rule "FAILED to apply security group rules for $vnic (whole batch rejected, chain left with skeleton only): $restore_err"
        echo "apply_sg_rule: iptables-restore failed for $vnic: $restore_err" >&2
        exit 1
    fi
fi
