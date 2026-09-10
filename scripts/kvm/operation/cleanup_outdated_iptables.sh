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
ln=$(iptables -n -L FORWARD --line-numbers | grep 'state RELATED,ESTABLISHED' | tail -1 | cut -d' ' -f1)
[ "$ln" != 1 ] && iptables -I FORWARD -m state --state RELATED,ESTABLISHED -j ACCEPT
for i in {1..10}; do
    ln=$(iptables -n -L FORWARD --line-numbers | grep 'state RELATED,ESTABLISHED' | tail -1 | cut -d' ' -f1)
    [ "$ln" = 1 ] && break
    [ -n "$ln" ] && iptables -D FORWARD $ln
done
iptables -N secgroup-chain && iptables -A secgroup-chain -j ACCEPT
ln=$(iptables -n -L secgroup-chain --line-numbers | grep 'ACCEPT' | head -1 | cut -d' ' -f1)
if [ "$ln" = 1 ]; then
    iptables -A secgroup-chain -j ACCEPT && iptables -D secgroup-chain -j ACCEPT
fi

# Ensure the internal-network allow rule sits at the very top of INPUT.
# Same approach as the FORWARD loop above: only act when it is not already at
# line 1 - delete the misplaced/duplicate copy and re-insert it at the head.
ln=$(iptables -n -L INPUT --line-numbers | grep 'ACCEPT .*10.0.0.0/8' | tail -1 | cut -d' ' -f1)
[ "$ln" != 1 ] && iptables -I INPUT -s 10.0.0.0/8 -j ACCEPT
for i in {1..10}; do
    ln=$(iptables -n -L INPUT --line-numbers | grep 'ACCEPT .*10.0.0.0/8' | tail -1 | cut -d' ' -f1)
    [ "$ln" = 1 ] && break
    [ -n "$ln" ] && iptables -D INPUT $ln
done

# Ensure the default reject rule is always the very last line of FORWARD.
# Only act when the last line is not already this rule: remove every existing
# copy, then append it once so it ends up at the tail.
last=$(iptables -S FORWARD | tail -1)
if [ "$last" != "-A FORWARD -j REJECT --reject-with icmp-host-prohibited" ]; then
    while iptables -C FORWARD -j REJECT --reject-with icmp-host-prohibited 2>/dev/null; do
        iptables -D FORWARD -j REJECT --reject-with icmp-host-prohibited
    done
    iptables -A FORWARD -j REJECT --reject-with icmp-host-prohibited
fi
flock -u 200
