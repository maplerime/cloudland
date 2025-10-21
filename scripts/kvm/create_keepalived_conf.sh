#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 6 ] && die "$0 <router> <vrrp_ID> <vrrp_vlan> <local_ip> <peer_ip> <role>"

router=$1
[ "${router/router-/}" = "$router" ] && router=router-$1
vrrp_ID=$2
vrrp_vlan=$3
local_ip=$4
peer_ip=$5
role=$6

virtual_ips=$(cat)
lb_dir=$router_dir/$router/lb-$vrrp_ID
[ ! -d "$lb_dir" ] && mkdir -p $lb_dir
cat >$lb_dir/keepalived.conf <<EOF
vrrp_instance load_balancer_${vrrp_ID} {
    state $role
    interface lb-$vrrp_vlan
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
cat >$lb_dir/keepalived.conf <<EOF
        $virtual_ip dev $ext_dev
    }
}
EOF
