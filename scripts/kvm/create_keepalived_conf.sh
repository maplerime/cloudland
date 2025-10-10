#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 8 ] && die "$0 <router> <vrrp_ID> <vrrp_vlan> <local_mac> <local_ip> <peer_mac> <peer_ip> <role> <virtual_ip>"

router=$1
[ "${router/router-/}" = "$router" ] && router=router-$1
vrrp_ID=$2
vrrp_vlan=$3
local_mac=$4
local_ip=${5%/*}
peer_mac=$6
peer_ip=${7%/*}
role=$8
virtual_ip=$9
ext_dev=te-$1-$ext_link

lb_dir=$router_dir/$router/lb-$vrrp_ID
[ ! -d "$lb_dir" ] && mkdir -p $lb_dir
cat >$lb_dir/keepalived.conf <<EOF
vrrp_instance load_balancer_${vrrp_ID} {
    state $role
    interface lb-$vrrp_vlan
    virtual_router_id ${vrrp_ID}
    priority 10
    advert_int 1

    unicast_src_ip $local_ip
    unicast_peer {
        $peer_ip
    }

    authentication {
        auth_type PASS
        auth_pass 123456
    }

    virtual_ipaddress {
        $virtual_ip dev $ext_dev
    }
}
EOF

echo "|:-COMMAND-:| $(basename $0) '$vrrp_ID' '$SCI_CLIENT_ID' '$role'"
