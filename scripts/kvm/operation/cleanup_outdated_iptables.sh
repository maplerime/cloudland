#!/bin/bash

cd `dirname $0`
source ../../cloudrc

secgroup_taps=$(iptables -S | grep 'A secgroup-in-tap\|A secgroup-as-tap\|A secgroup-out-tap' | cut -d' ' -f2 | cut -d- -f3 | sort -u)
instances=$(virsh list --all | grep inst- | awk '{print $2}')
for instance in $instances; do
    instance_taps="$instance_taps "$(virsh dumpxml $instance | grep tap | cut -d= -f2 | tr -d "'/>")
done
for sg_tap in $secgroup_taps; do
    echo $instance_taps | grep $sg_tap
    if [ $? -ne 0 ]; then
        echo "cleanup security group for non-existing device $sg_tap"
        ../clear_sg_chain.sh $sg_tap
    fi
done
