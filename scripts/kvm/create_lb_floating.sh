#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 7 ] && echo "$0 <router> <ext_ip> <ext_gw> <ext_vlan> <mark_id> <inbound> <outbound>" && exit -1

ID=$1
router=router-$1
ext_cidr=$2
ext_ip=${2%/*}
ext_gw=${3%/*}
ext_vlan=$4
mark_id=$(($5 % 2147483647))
inbound=$6
outbound=$7

[ -z "$router" -o "$router" = "router-0" -o  -z "$ext_ip" ] && exit 1
ip netns list | grep -q $router
[ $? -ne 0 ] && echo "Router $router does not exist" && exit -1

rtID=$(( $ext_vlan % 250 + 2 ))
table=fip-$ext_vlan
rt_file=/etc/iproute2/rt_tables
grep "^$rtID $table" $rt_file
if [ $? -ne 0 ]; then
    for i in {1..250}; do
        grep "^$rtID\s" $rt_file
	[ $? -ne 0 ] && break
        rtID=$(( ($rtID + 17) % 250 + 2 ))
    done
    echo "$rtID $table" >>$rt_file
fi
suffix=${ID}-${ext_vlan}
ext_dev=te-$suffix
./create_veth.sh $router ext-$suffix te-$suffix

ip netns exec $router ip addr add $ext_cidr dev $ext_dev
ip netns exec $router ip route replace default via $ext_gw table $table
ip netns exec $router ip -o addr | grep "ns-.* inet " | awk '{print $2, $4}' | while read ns_link ns_gw; do
    ip_net=$(ipcalc -b $ns_gw | grep Network | awk '{print $2}')
    ip netns exec $router ip route add $ip_net dev $ns_link table $table
done
ip netns exec $router ip rule add from $ext_ip lookup $table
ip netns exec $router ip rule add to $ext_ip lookup $table
async_exec ip netns exec $router arping -c 1 -A -U -I $ext_dev $ext_ip

if [ "$inbound" -gt 0 ]; then
    ip netns exec $router tc qdisc add dev $ext_dev root handle 1: htb default 10
    ip netns exec $router tc class add dev $ext_dev parent 1: classid 1:$mark_id htb rate ${inbound}mbit burst ${inbound}kbit
    ip netns exec $router tc filter add dev $ext_dev protocol ip parent 1:0 prio $mark_id u32 match ip dst $ext_ip/32 flowid 1:$mark_id
else
    ip netns exec $router tc filter del dev $ext_dev protocol ip parent 1:0 prio $mark_id u32 match ip dst $ext_ip/32 flowid 1:$mark_id
    ip netns exec $router tc class del dev $ext_dev parent 1: classid 1:$mark_id
fi
let mark_id=$mark_id+2147483647
if [ "$outbound" -gt 0 ]; then
    ip netns exec $router tc qdisc add dev $ext_dev root handle 1: htb default 10
    ip netns exec $router tc class add dev $ext_dev parent 1: classid 1:$mark_id htb rate ${outbound}mbit burst ${outbound}kbit
    ip netns exec $router tc filter add dev $ext_dev protocol ip parent 1:0 prio $mark_id u32 match ip src $ext_ip/32 flowid 1:$mark_id
else
    ip netns exec $router tc filter del dev $ext_dev protocol ip parent 1:0 prio $mark_id u32 match ip src $ext_ip/32 flowid 1:$mark_id
    ip netns exec $router tc class del dev $ext_dev parent 1: classid 1:$mark_id
fi
