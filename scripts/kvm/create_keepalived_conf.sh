#!/bin/bash -xv

cd `dirname $0`
source ../cloudrc

[ $# -lt 6 ] && die "$0 <router> <vrrp_ID> <vrrp_vlan> <local_ip> <peer_ip> <role>"

ID=$1
router=router-$ID
vrrp_ID=$2
vrrp_vlan=$3
local_ip=$4
peer_ip=$5
role=$6

router_dir=$router_dir/$router
[ ! -d "$router_dir" ] && mkdir -p $router_dir
cat >$router_dir/keepalived.conf <<EOF
vrrp_instance load_balancer_${vrrp_ID} {
    state $role
    interface ns-$vrrp_vlan
    virtual_router_id ${vrrp_ID}
    priority 10
    advert_int 1

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
vips=$(cat)
nvip=$(jq length <<< $vips)
i=0
while [ $i -lt $nvip ]; do
    read -d'\n' -r virtual_ip ext_vlan < <(jq -r ".[$i].address, .[$i].vlan" <<<$vips)
    suffix=${ID}-${ext_vlan}
    ext_dev=te-$suffix
    ./create_veth.sh $router ext-$suffix te-$suffix
    cat >>$router_dir/keepalived.conf <<EOF
        $virtual_ip dev $ext_dev
EOF
    let i=$i+1
done
cat >>$router_dir/keepalived.conf <<EOF
    }
}
EOF

ip netns exec $router keepalived -p $router_dir/keepalived.pid -f $router_dir/keepalived.conf
