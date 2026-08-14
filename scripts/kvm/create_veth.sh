#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <router> <veth_name> <peer_name>" && exit -1
router=$1
device=$2
peerdev=$3

# If the veth is already fully in place (host side present and peer already in
# the router namespace), there is nothing to do - exit successfully. Otherwise
# tear down any stale half and rebuild the pair, then continue configuring it.
ip link show $device &>/dev/null
host_ok=$?
ip netns exec $router ip link show $peerdev &>/dev/null
peer_ok=$?
if [ $host_ok -eq 0 -a $peer_ok -eq 0 ]; then
    exit 0
fi
ip link del $device 2>/dev/null
ip netns exec $router ip link del $peerdev 2>/dev/null
ip link add $device type veth peer name $peerdev
ip link set $device up
ip link set $peerdev netns $router
ip netns exec $router ip link set $peerdev mtu 1450 up
vlan=${device##*-}
prefix=${device%%-*}
if [ "$prefix" == "ext" ]; then
    ./create_link.sh $vlan
fi
bridge=br$vlan
ip link set dev $device master $bridge
