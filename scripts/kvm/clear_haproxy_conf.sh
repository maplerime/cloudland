#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 2 ] && die "$0 <router> <lb_ID>"

router_ID=$1
router=router-$router_ID
lb_ID=$2

lb_dir=$router_dir/$router/lb-$lb_ID
haproxy_pid=$(cat $lb_dir/haproxy.pid)
ip netns exec $router kill $haproxy_pid
rm -rf $lb_dir
