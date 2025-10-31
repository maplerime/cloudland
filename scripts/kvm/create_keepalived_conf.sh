#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 8 ] && die "$0 <router> <vrrp_ID> <vrrp_vlan> <local_ip> <local_mac> <peer_ip> <peer_mac> <role>"

ID=$1
router=router-$ID
vrrp_ID=$2
vrrp_vlan=$3
local_ip=$4
local_mac=$5
peer_ip=$6
peer_mac=$7
role=$8

vrrp_dir=$router_dir/$router
vips=$(cat)
nvip=$(jq length <<< $vips)
if [ $nvip -eq 0 ]; then
    keepalived_pid=$(cat $vrrp_dir/keepalived.pid)
    [ $keepalived_pid -gt 0 ] && ip netns exec $router kill $keepalived_pid
    exit 0
fi

ip netns exec $router ip link show ns-$vrrp_vlan | grep $local_mac 
[ $? -ne 0 ] && ./set_vrrp_ip.sh $@

[ ! -d "$vrrp_dir" ] && mkdir -p $vrrp_dir
cat >$vrrp_dir/keepalived.conf <<EOF
vrrp_instance load_balancer_${vrrp_ID} {
    state $role
    interface ns-$vrrp_vlan
    virtual_router_id ${vrrp_ID}
    priority 10
    advert_int 1
    nopreempt

    unicast_src_ip ${local_ip%/*}
    unicast_peer {
        ${peer_ip%/*}
    }

    authentication {
        auth_type PASS
        auth_pass 123456
    }

    virtual_ipaddress {
EOF
i=0
rm -f $vrrp_dir/routes
while [ $i -lt $nvip ]; do
    read -d'\n' -r virtual_ip ext_vlan ext_gw mark_id inbound outbound< <(jq -r ".[$i].address, .[$i].vlan, .[$i].gateway, .[$i].mark_id, .[$i].inbound, .[$i].outbound" <<<$vips)
    suffix=${ID}-${ext_vlan}
    ext_dev=te-$suffix
    ./create_veth.sh $router ext-$suffix te-$suffix
    ./create_lb_floating.sh $ID $virtual_ip $ext_gw $ext_vlan $mark_id $inbound $outbound
    cat >>$vrrp_dir/keepalived.conf <<EOF
        $virtual_ip dev $ext_dev
EOF
    let i=$i+1
done
cat >>$vrrp_dir/keepalived.conf <<EOF
    }
    notify_master $PWD/set_route_table.sh
}
EOF

export ROUTES_FILE=$vrrp_dir/routes
keepalived_pid=$(cat $vrrp_dir/keepalived.pid)
[ $keepalived_pid -gt 0 ] && kill -HUP $keepalived_pid
[ $? -ne 0 ] && ip netns exec $router keepalived -p $vrrp_dir/keepalived.pid -f $vrrp_dir/keepalived.conf
exit 0
