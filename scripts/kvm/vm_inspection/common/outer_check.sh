#!/bin/bash
vm_id=$1
inner_b64=$2
RPC_TIMEOUT=${RPC_TIMEOUT:-5}
GUEST_EXEC_TIMEOUT=${GUEST_EXEC_TIMEOUT:-20}
guest_cmd='printf "GUEST_STARTED|%s\n" "$(date +%s)"; echo '
guest_cmd+="$inner_b64"
guest_cmd+=' | base64 -d | /bin/sh; rc=$?; printf "GUEST_FINISHED|%s\n" "$(date +%s)"; exit "$rc"'
request=$(jq -cn --arg cmd "$guest_cmd" '{execute:"guest-exec",arguments:{path:"/bin/sh",arg:["-c",$cmd],"capture-output":true}}')
exec_resp=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_id" "$request" 2>&1)
rc=$?
if [ "$rc" -ne 0 ]; then
    [ "$rc" -ne 124 ] || exec_resp="timed out after ${RPC_TIMEOUT}s"
    echo "ERROR:$vm_id: guest-exec failed (exit=$rc): ${exec_resp:-empty response}"; exit 1
fi
pid=$(echo "$exec_resp" | jq -r -s 'if length==1 then .[0].return.pid | select(type=="number" and .>0 and .==floor) else empty end' 2>/dev/null)
if [ -z "$pid" ]; then echo "ERROR:$vm_id: guest-exec returned an invalid response or missing PID: ${exec_resp:-empty response}"; exit 1; fi
exited=false
for i in $(seq 1 "$GUEST_EXEC_TIMEOUT"); do
    status_resp=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_id" '{"execute":"guest-exec-status","arguments":{"pid":'"$pid"'}}' 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        [ "$rc" -ne 124 ] || status_resp="timed out after ${RPC_TIMEOUT}s"
        echo "ERROR:$vm_id: guest-exec-status failed (exit=$rc): ${status_resp:-empty response}"; exit 1
    fi
    if ! echo "$status_resp" | jq -e -s 'length==1 and (.[0].return|type=="object") and (.[0].return.exited|type=="boolean")' >/dev/null 2>&1; then
        echo "ERROR:$vm_id: guest-exec-status returned an invalid response or missing exited flag: ${status_resp:-empty response}"; exit 1
    fi
    echo "$status_resp" | jq -e '.return.exited==true' >/dev/null 2>&1 && { exited=true; break; }
    sleep 1
done
if [ "$exited" != "true" ]; then echo "ERROR:$vm_id: guest command did not finish after $GUEST_EXEC_TIMEOUT status checks (pid=$pid)"; exit 1; fi
echo "$status_resp" | jq -r '.return."out-data" // ""' 2>/dev/null | base64 -d || exit 1
if ! echo "$status_resp" | jq -e '.return.exitcode==0 and (.return."out-truncated"!=true)' >/dev/null 2>&1; then
    echo "ERROR:$vm_id: guest output incomplete (exitcode=$(echo "$status_resp" | jq -r .return.exitcode), truncated=$(echo "$status_resp" | jq -r '.return["out-truncated"] // false'))" >&2; exit 1
fi
