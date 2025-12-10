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

vrrp_dir=$router_dir/$router/vrrp-$vrrp_ID
mkdir -p $vrrp_dir
content=$(cat)
vips=$(jq -r .floating_ips <<< $content)
nvip=$(jq length <<< $vips)
if [ $nvip -eq 0 ]; then
    keepalived_pid=$(cat $vrrp_dir/keepalived.pid)
    [ $keepalived_pid -gt 0 ] && ip netns exec $router kill $keepalived_pid
    rm -rf $vrrp_dir
    exit 0
fi
ports=$(jq -r .ports <<< $content)
nport=$(jq length <<< $ports)
i=0
while [ $i -lt $nvip ]; do
    vip=$(jq -r .[$i].address <<< $vips)
    ext_ip=${vip%/*}
    for num in $(ip netns exec $router iptables -n -L --line-numbers | grep "\<$ext_ip\>" | awk '{print $1}' | sort -nr); do
        ip netns exec $router iptables -D INPUT $num
    done
    j=0
    while [ $j -lt $nport ]; do
        port=$(jq -r .[$j] <<< $ports)
        ip netns exec $router iptables -C INPUT -p tcp -m tcp -d $ext_ip --dport $port -m conntrack --ctstate NEW -j ACCEPT
        [ $? -ne 0 ] && ip netns exec $router iptables -A INPUT -p tcp -m tcp -d $ext_ip --dport $port -m conntrack --ctstate NEW -j ACCEPT
        let j=$j+1
    done
    let i=$i+1
done

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
export ROUTES_FILE=$vrrp_dir/routes
rm -f $ROUTES_FILE
i=0
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

keepalived_pid=$(cat $vrrp_dir/keepalived.pid)
[ $keepalived_pid -gt 0 ] && kill -HUP $keepalived_pid
[ $? -ne 0 ] && ip netns exec $router keepalived -p $vrrp_dir/keepalived.pid -r $vrrp_dir/vrrp.pid -f $vrrp_dir/keepalived.conf
exit 0
