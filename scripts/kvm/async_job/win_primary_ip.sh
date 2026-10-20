#!/bin/bash

cd `dirname $0`
source ../../cloudrc
[ $# -lt 1 ] && echo "$0 <ID>" && exit 1

ID=$1
vm_ID=inst-$ID

log_debug $vm_ID "Waiting for Windows VM '$vm_ID' to boot..."
wait_qemu_ping $ID 10
metadata=$(cat)

# Keep Linux cloud-init and the Windows guest-agent fallback in sync.
dns_server="$dns_server" dns_server6="${dns_server6:-}" python3 ../windows_network.py "$vm_ID" --metadata <<< "$metadata"
