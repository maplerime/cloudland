#!/bin/bash

cd `dirname $0`
source ../../cloudrc
[ $# -lt 2 ] && echo "$0 <vm_ID> <rdp_port>" && exit -1

ID=$1
vm_ID=inst-$ID
rdp_port=$2


TIMEOUT=600 # 10 minutes
EXEC_TIMEOUT=60 # 1 minutes
WAIT_TIME=5
ELAPSED_TIME=0


# wait for the windows guest agent to start
log_debug $vm_ID "Waiting for Windows VM '$vm_ID' to start the guest agent..."
wait_qemu_ping $ID 10

PS_SCRIPT='Set-ItemProperty -Path \"HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Terminal Server\\WinStations\\RDP-Tcp\" -Name PortNumber -Value '${rdp_port}'; Restart-Service -Name \"TermService\" -Force; New-NetFirewallRule -DisplayName \"RDP-TCP-'${rdp_port}'\" -Action Allow -Protocol TCP -LocalPort '${rdp_port}

log_debug ${vm_ID} "Executing PowerShell script to change RDP port..."
while true; do
    OUTPUT=$(virsh qemu-agent-command "${vm_ID}" '{"execute":"guest-exec","arguments":{"path":"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe","arg":["-Command","'"${PS_SCRIPT}"'"],"capture-output":true}}')
    if [ -n "${OUTPUT}" ]; then
        log_debug ${vm_ID} "guest-exec: ${OUTPUT}"
        PID=$(jq -r '.return.pid' <<< ${OUTPUT})
        if [ -n "${PID}" ]; then
            log_debug ${vm_ID} "guest-exec succeed, PID: ${PID}"
            # wait for the powershell to finish
            while true; do
                if [ $ELAPSED_TIME -ge $EXEC_TIMEOUT ]; then
                    log_debug ${vm_ID} "timeout while waiting powershell to finish"
                    break
                fi
                OUTPUT=$(virsh qemu-agent-command "${vm_ID}" '{"execute":"guest-exec-status","arguments":{"pid":'"${PID}"'}}')
                ELAPSED_TIME=$((ELAPSED_TIME + WAIT_TIME))
                log_debug ${vm_ID} "guest-exec-status: ${OUTPUT}"
                if [ -n "${OUTPUT}" ]; then
                    # {"return":{"exited":false}}
                    if [ $(jq -r '.return.exited' <<< ${OUTPUT}) == "false" ]; then
                        log_debug ${vm_ID} "guest-exec-status not finished"
                        sleep $WAIT_TIME
                        continue
                    fi
                    
                    EXEC_OUT_PUT=$(jq -r '.return.out-data' <<< ${OUTPUT})
                    EXIT_CODE=$(jq -r '.return.exitcode' <<< ${OUTPUT})
                    log_debug ${vm_ID} "guest-exec-status finished, exitcode: ${EXIT_CODE}, output: ${EXEC_OUT_PUT}"
                    if [ $EXIT_CODE -eq 0 ]; then
                        log_debug ${vm_ID} "guest-exec-status succeed, script executed successfully, exit!"
                        exit 0
                    else
                        log_debug ${vm_ID} "guest-exec-status failed, will try again later"
                        break
                    fi
                else
                    log_debug ${vm_ID} "guest-exec-status failed, will try again later"
                    break
                fi
            done
        else
            log_debug ${vm_ID} "Failed to execute guest-exec, will try again later"
        fi
    fi
    if [ $ELAPSED_TIME -ge $TIMEOUT ]; then
        log_debug ${vm_ID} "timeout while waiting guest agent ready"
        die "Timeout waiting for Windows VM '${vm_ID}' to execute PowerShell script after ${TIMEOUT} seconds."
    fi
    sleep $WAIT_TIME
    ELAPSED_TIME=$((ELAPSED_TIME + WAIT_TIME))
done
