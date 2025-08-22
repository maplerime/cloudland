#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ip> <vm_mac> <allow_spoofing> [nic_name]" && exit -1

vm_ip=${1%%/*}
vm_mac=$2
allow_spoofing=$3
nic_name=$4

[ -z "$nic_name" ] && nic_name=tap$(echo $vm_mac | cut -d: -f4- | tr -d :)
./clear_sg_chain.sh "$nic_name" "true"
vlan_info=$(cat)
lock_file="$run_dir/iptables.lock"
exec 200>>"$lock_file"
flock -x 200
jq -r .more_addresses <<<$vlan_info | ../create_sg_chain.sh "$nic_name" "$vm_ip" "$vm_mac" "$allow_spoofing"
jq -r .security <<<$vlan_info | ../apply_sg_rule.sh "$nic_name"
flock -u 200
touch $async_job_dir/$nic_name
