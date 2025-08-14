#!/bin/bash

cd `dirname $0`
source ../../cloudrc

# Enable nftables mode
USE_NFTABLES=true

[ $# -lt 1 ] && echo "$0 <interface> [force]" && exit -1

vnic=$1
force=$2
chain_in=secgroup-in-$vnic
chain_out=secgroup-out-$vnic
chain_as=secgroup-as-$vnic

if [ "$force" != "true" ]; then 
    for i in {1..35}; do
        [ -f $async_job_dir/$vnic ] && break
        sleep 1
    done
fi

rm -f $async_job_dir/$vnic

# Delete rules from FORWARD chain
apply_fw -D FORWARD iifname "$vnic" bridge/physdev_is_bridged jump secgroup-chain
apply_fw -D FORWARD oifname "$vnic" bridge/physdev_is_bridged jump secgroup-chain

# Delete rules from secgroup-chain
apply_fw -D secgroup-chain iifname "$vnic" bridge/physdev_is_bridged jump $chain_in
apply_fw -D secgroup-chain oifname "$vnic" bridge/physdev_is_bridged jump $chain_out

# Delete rule from INPUT chain
apply_fw -D INPUT oifname "$vnic" bridge/physdev_is_bridged jump $chain_out

# Flush and delete chains
# In nftables, we delete the chains directly
nft delete chain inet filter $chain_in 2>/dev/null
nft delete chain inet filter $chain_as 2>/dev/null
nft delete chain inet filter $chain_out 2>/dev/null