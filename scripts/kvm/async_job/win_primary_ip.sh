#!/bin/bash

cd `dirname $0`
source ../../cloudrc
[ $# -lt 1 ] && echo "$0 <ID>" && exit 1

ID=$1
vm_ID=inst-$ID

log_debug $vm_ID "Waiting for Windows VM '$vm_ID' to boot..."
wait_qemu_ping $ID 10
metadata=$(cat)

dns=$(jq -r '.dns' <<< $metadata)
[ -z "$dns" ] && dns=$dns_server
read -r ip netmask gateway <<< $(jq -r '.networks[] | select(.id == "network0" and .routes[0].network == "0.0.0.0") | "\(.ip_address) \(.netmask) \(.routes[0].gateway)"' <<< $metadata)
PS_SCRIPT='C:\\Windows\\System32\\netsh.exe interface ipv4 set address name=eth0 addr='"$ip"' mask='"$netmask"' gateway='"$gateway"'; C:\\Windows\\System32\\netsh.exe interface ipv4 set dns name=eth0 static '"$dns"' register=primary'

OUTPUT=$(virsh qemu-agent-command "$vm_ID" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe","arg":["-Command","'"$PS_SCRIPT"'"],"capture-output":true}}')
log_debug $vm_ID "$vm_ID exec powershell: $OUTPUT"
