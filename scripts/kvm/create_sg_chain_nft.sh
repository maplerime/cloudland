#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Enable nftables mode
USE_NFTABLES=true

[ $# -lt 3 ] && echo "$0 <interface> <ip> <mac> <allow_spoofing>" && exit -1

vnic=$1
ip=${2%%/*}
mac=$3
allow_spoofing=$4

# Initialize nftables table and base chain if they don't exist
nft list table inet filter 2>/dev/null || nft add table inet filter
nft list chain inet filter secgroup-chain 2>/dev/null || nft add chain inet filter secgroup-chain

# Add rules to forward traffic through secgroup-chain
apply_fw -I FORWARD iifname "$vnic" bridge/physdev_is_bridged jump secgroup-chain
apply_fw -I FORWARD oifname "$vnic" bridge/physdev_is_bridged jump secgroup-chain

chain_in=secgroup-in-$vnic
chain_out=secgroup-out-$vnic
chain_as=secgroup-as-$vnic

# Create and flush input chain
apply_fw -N $chain_in
# Note: Flushing is handled by creating a new chain in nftables

# Add rule to secgroup-chain to jump to input chain
apply_fw -I secgroup-chain iifname "$vnic" bridge/physdev_is_bridged jump $chain_in

# Add default rules to input chain
apply_fw -A $chain_in ct state { established, related } return
apply_fw -A $chain_in ct state invalid drop
apply_fw -A $chain_in drop

# Create and flush anti-spoofing chain
apply_fw -N $chain_as
# Note: Flushing is handled by creating a new chain in nftables

if [ "$allow_spoofing" = true ]; then
    apply_fw -I $chain_as return
else
    apply_fw -A $chain_as ip saddr $ip/32 ether saddr $mac return
    apply_fw -A $chain_as drop
    more_addresses=$(cat)
    naddrs=$(jq length <<< $more_addresses)
    if [ $naddrs -gt 0 ]; then
        for i in {1..300}; do
            bridge=$(readlink /sys/class/net/$vnic/master | xargs basename)
            [ -n "$bridge" ] && break
            sleep 2
        done
        i=0
        while [ $i -lt $naddrs ]; do
            read -d'\n' -r address < <(jq -r ".[$i]" <<<$more_addresses)
            read -d'\n' -r ip netmask < <(ipcalc -nb $address | awk '/Address/ {print $2} /Netmask/ {print $2}')
            apply_fw -I $chain_as ip saddr $ip/32 ether saddr $mac return
            ./send_spoof_arp.py $bridge $ip $mac &
            let i=$i+1
        done
    fi
fi

# Create and flush output chain
apply_fw -N $chain_out
# Note: Flushing is handled by creating a new chain in nftables

# Add rule to secgroup-chain to jump to output chain
apply_fw -I secgroup-chain oifname "$vnic" bridge/physdev_is_bridged jump $chain_out

# Add rule to INPUT chain to jump to output chain
apply_fw -I INPUT oifname "$vnic" bridge/physdev_is_bridged jump $chain_out

# Add default rules to output chain
apply_fw -A $chain_out jump $chain_as
apply_fw -A $chain_out ct state { established, related } return
apply_fw -A $chain_out ct state invalid drop
apply_fw -A $chain_out drop