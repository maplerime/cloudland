#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ip> <vm_mac> <allow_spoofing> [nic_name]" && exit -1

create_sg_chain_sh="../create_sg_chain.sh"
apply_sg_rule_sh="../apply_sg_rule.sh"
clear_sg_chain_sh="./clear_sg_chain.sh"

if [ "$USE_NFTABLES" = true ]; then
    create_sg_chain_sh="../create_sg_chain_nft.sh"
    apply_sg_rule_sh="../apply_sg_rule_nft.sh"
    clear_sg_chain_sh="./clear_sg_chain_nft.sh"
fi

vm_ip=${1%%/*}
vm_mac=$2
allow_spoofing=$3
nic_name=$4

[ -z "$nic_name" ] && nic_name=tap$(echo $vm_mac | cut -d: -f4- | tr -d :)
$clear_sg_chain_sh "$nic_name" "true"
vlan_info=$(cat)
jq -r .more_addresses <<<$vlan_info | $create_sg_chain_sh "$nic_name" "$vm_ip" "$vm_mac" "$allow_spoofing"
jq -r .security <<<$vlan_info | $apply_sg_rule_sh "$nic_name"
touch $async_job_dir/$nic_name
