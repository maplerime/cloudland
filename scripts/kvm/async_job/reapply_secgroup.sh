#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ip> <vm_mac> <allow_spoofing> [nic_name]" && exit -1

vm_ip=${1%%/*}
vm_mac=$2
allow_spoofing=$3
nic_name=$4
vlan=$5
router=$6

[ -z "$nic_name" ] && nic_name=tap$(echo $vm_mac | cut -d: -f4- | tr -d :)
./clear_sg_chain.sh "$nic_name" "true"
vlan_info=$(cat)
lock_file="$run_dir/iptables.lock"
exec 200>>"$lock_file"
flock -x 200
jq -r .more_addresses <<<$vlan_info | ../create_sg_chain.sh "$nic_name" "$vm_ip" "$vm_mac" "$allow_spoofing"
jq -r .security <<<$vlan_info | ../apply_sg_rule.sh "$nic_name"
flock -u 200
# Bridge FDB / flood / nftables ARP filtering are applied outside the iptables
# lock: they depend on the tap being present and may wait for it, which must
# not stall other VMs' security-group updates holding the same lock.
jq -r .more_addresses <<<$vlan_info | ../apply_arp_filter.sh "$nic_name" "$vm_ip" "$vm_mac" "$allow_spoofing"
touch $async_job_dir/$nic_name
echo "vm_ip=$vm_ip vm_br=br$vlan router=$router" >> "$async_job_dir/$nic_name"
