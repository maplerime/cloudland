#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <hostname> <os_code>" && exit -1

ID=$1
vm_name=$2
os_code=$3
vlans=$(cat)
nvlan=$(jq length <<< $vlans)
i=0
while [ $i -lt $nvlan ]; do
    sites_ip_info=$(jq -r .[$i].sites_ip_info <<< $vlans)
    if [ -n "$sites_ip_info" ]; then
        async_exec ./async_job/apply_sites_ip.sh "$router" "$ip" "$os_code"
	break
    fi
    let i=$i+1
done
