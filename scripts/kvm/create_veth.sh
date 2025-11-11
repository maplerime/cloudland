#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <router> <veth_name> <peer_name>" && exit -1
router=$1
device=$2
peerdev=$3

ip link add $device type veth peer name $peerdev
ip link set $device up
ip link set $peerdev netns $router
ip netns exec $router ip link set $peerdev mtu 1450 up
ip netns exec $router iptables -C FORWARD -i $peerdev -j ACCEPT
[ $? -ne 0 ] && ip netns exec $router iptables -A FORWARD -i $peerdev -j ACCEPT
ip netns exec $router iptables -C FORWARD -o $peerdev -j ACCEPT
[ $? -ne 0 ] && ip netns exec $router iptables -A FORWARD -o $peerdev -j ACCEPT
vlan=${device##*-}
prefix=${device%%-*}
if [ "$prefix" == "ext" ]; then
    ./create_link.sh $vlan
fi
bridge=br$vlan
ip link set dev $device master $bridge
