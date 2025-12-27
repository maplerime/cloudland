#!/bin/bash

cd `dirname $0`
source ../../cloudrc
[ $# -lt 2 ] && echo "$0 <ID> <rdp_port>" && exit -1

ID=$1
vm_ID=inst-$ID
rdp_port=$2

log_debug $vm_ID "Waiting for Windows VM '$vm_ID' to boot..."
wait_qemu_ping $ID 10

PS_SCRIPT='Set-ItemProperty -Path \"HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Terminal Server\\WinStations\\RDP-Tcp\" -Name PortNumber -Value '${rdp_port}'; Restart-Service -Name \"TermService\" -Force; New-NetFirewallRule -DisplayName \"RDP-TCP-'${rdp_port}'\" -Action Allow -Protocol TCP -LocalPort '${rdp_port}

OUTPUT=$(virsh qemu-agent-command "$vm_ID" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe","arg":["-Command","'"$PS_SCRIPT"'"],"capture-output":true}}')
log_debug $vm_ID "$vm_ID exec powershell: $OUTPUT"
