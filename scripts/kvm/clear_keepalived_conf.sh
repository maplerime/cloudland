#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 7 ] && die "$0 <router> <vrrp_ID> <vrrp_vlan> <local_mac> <local_ip> <peer_mac> <peer_ip>"

router_ID=$1
router=router-$router_ID
vrrp_ID=$2
vrrp_vlan=$3
local_mac=$4
local_ip=$5
peer_mac=$6
peer_ip=$7
role=$8
ext_dev=te-$1-$ext_link

rm -rf $router_dir/$router/lb-$vrrp_ID

# clear either local or peer IPs
bridge fdb del $local_mac dev v-$vrrp_vlan
bridge fdb del $peer_mac dev v-$vrrp_vlan
ip neighbor del ${local_ip%%/*} dev v-$vrrp_vlan
ip neighbor del ${peer_ip%%/*} dev v-$vrrp_vlan
ip netns exec $router ip addr del $local_ip dev ns-$vrrp_vlan
ip netns exec $router ip addr del $peer_ip dev ns-$vrrp_vlan
ip netns exec $router ip addr show ns-$vrrp_vlan | grep 'inet '
if [ $? -ne 0 ]; then
    ./clear_link.sh $vrrp_vlan
    ./clear_local_router.sh $router_ID
fi
