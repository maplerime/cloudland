#!/bin/bash

cd `dirname $0`
source ../../cloudrc

secgroup_taps=$(iptables -S | grep 'tap.*physdev-is-bridged' | awk '{print $6}' | sort -u)
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

lock_file="$run_dir/iptables.lock"
exec 200>>"$lock_file"
iptables -I FORWARD -m state --state RELATED,ESTABLISHED -j ACCEPT
for i in {1..10}; do
    ln=$(iptables -n -L FORWARD --line-numbers | grep 'state RELATED,ESTABLISHED' | tail -1 | cut -d' ' -f1)
    [ "$ln" = 1 ] && break
    iptables -D FORWARD $ln
done
ln=$(iptables -n -L secgroup-chain --line-numbers | grep 'ACCEPT' | head -1 | cut -d' ' -f1)
if [ "$ln" = 1 ]; then
    iptables -A secgroup-chain -j ACCEPT && iptables -D secgroup-chain -j ACCEPT
fi
flock -u 200
