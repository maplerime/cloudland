#!/bin/bash
vm_id=$1
inner_b64=$2
RPC_TIMEOUT=${RPC_TIMEOUT:-5}
GUEST_EXEC_TIMEOUT=${GUEST_EXEC_TIMEOUT:-20}
guest_cmd='printf "GUEST_STARTED|%s\n" "$(date +%s)"; echo '
guest_cmd+="$inner_b64"
guest_cmd+=' | base64 -d | /bin/sh; rc=$?; printf "GUEST_FINISHED|%s\n" "$(date +%s)"; exit "$rc"'
request=$(jq -cn --arg cmd "$guest_cmd" '{execute:"guest-exec",arguments:{path:"/bin/sh",arg:["-c",$cmd],"capture-output":true}}')
exec_resp=$(timeout "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_id" "$request" 2>&1)
pid=$(echo "$exec_resp" | jq -r '.return.pid // empty' 2>/dev/null)
if [ -z "$pid" ]; then echo "ERROR:$exec_resp"; exit 1; fi
exited=false
for i in $(seq 1 "$GUEST_EXEC_TIMEOUT"); do
    status_resp=$(timeout "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_id" '{"execute":"guest-exec-status","arguments":{"pid":'"$pid"'}}' 2>&1)
    if ! echo "$status_resp" | jq -e 'has("return")' >/dev/null 2>&1; then echo "ERROR:$status_resp"; exit 1; fi
    echo "$status_resp" | jq -e '.return.exited==true' >/dev/null 2>&1 && { exited=true; break; }
    sleep 1
done
if [ "$exited" != "true" ]; then echo "ERROR:timeout_or_disabled:$status_resp"; exit 1; fi
echo "$status_resp" | jq -r '.return."out-data" // ""' 2>/dev/null | base64 -d || exit 1
if ! echo "$status_resp" | jq -e '.return.exitcode==0 and (.return."out-truncated"!=true)' >/dev/null 2>&1; then
    echo "ERROR:guest_output_incomplete" >&2; exit 1
fi
