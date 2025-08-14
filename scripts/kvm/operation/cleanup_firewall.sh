#!/bin/bash

cd `dirname $0`
source ../../cloudrc

# Check if nftables is enabled
if [ "$USE_NFTABLES" = true ]; then
    # For nftables, we need to extract interface names from nftables rules
    # This is a simplified approach - in practice, you might want to store interface information differently
    secgroup_taps=$(nft list ruleset | grep 'iifname\|oifname' | grep tap | sed -E 's/.*"(tap[^"]+)".*/\1/' | sort -u)
else
    # Original iptables approach
    secgroup_taps=$(iptables -S | grep 'tap.*physdev-is-bridged' | awk '{print $6}' | sort -u)
fi

instances=$(virsh list --all | grep inst- | awk '{print $2}')
for instance in $instances; do
    instance_taps="$instance_taps "$(virsh dumpxml $instance | grep tap | cut -d= -f2 | tr -d "'/>")
done

for sg_tap in $secgroup_taps; do
    echo $instance_taps | grep $sg_tap
    if [ $? -ne 0 ]; then
        echo "cleanup security group for non-existing device $sg_tap"
        if [ "$USE_NFTABLES" = true ]; then
            ../clear_sg_chain_nft.sh $sg_tap
        else
            ../clear_sg_chain.sh $sg_tap
        fi
    fi
done