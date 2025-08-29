#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <hostname> <os_code> <update_meta>" && exit -1

ID=$1
vm_name=$2
os_code=$3
update_meta=$4
[ -z "$update_meta" ] && update_meta=false
vlans=$(cat)
nvlan=$(jq length <<< $vlans)
i=0
while [ $i -lt $nvlan ]; do
    jq -r .[$i] <<< $vlans | ./attach_vm_nic.sh "$ID" "$vm_name" "$os_code" "$update_meta"
    let i=$i+1
done
