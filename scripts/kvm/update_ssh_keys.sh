#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 4 ] && die "$0 <task_ID> <vm_ID> <add|remove|reset> <user>  (base64 keys on stdin)"

task_ID=$1
ID=$2
vm_ID=inst-$ID
action=$3
user=$4
keys_b64=$(cat)

# SCI keeps only the first line and drops an empty trailing '' argument,
# so the message is flattened to one line, stripped of quotes and never empty
function report()
{
    local msg=$(echo "${2//\'/}" | tr '\n' ' ' | sed 's/ *$//')
    echo "|:-COMMAND-:| $(basename $0) '$task_ID' '$1' '${msg:-unknown error}'"
    exit 0
}

case "$action" in
    add|remove|reset) ;;
    *) report failed "invalid action $action" ;;
esac

# runtime checks: DB state is not trusted, check the real domain
[ "$(virsh domstate $vm_ID 2>/dev/null)" != "running" ] && report failed "instance is not running"
virsh qemu-agent-command --timeout 10 $vm_ID '{"execute":"guest-ping"}' >/dev/null 2>&1 || report failed "guest agent not responding"
os_id=$(virsh qemu-agent-command --timeout 10 $vm_ID '{"execute":"guest-get-osinfo"}' 2>/dev/null | jq -r '.return.id')
[ "$os_id" == "mswindows" ] && report failed "windows is not supported"

# path A: qemu-ga >= 5.2 guest-ssh-* commands
key_file=$(mktemp)
trap "rm -f $key_file" EXIT
echo "$keys_b64" | base64 -d > $key_file 2>/dev/null
opts=""
[ "$action" == "remove" ] && opts="--remove"
[ "$action" == "reset" ] && opts="--reset"
# ponytail: reset without --file clears authorized_keys; virsh rejects an empty key file
[ -s $key_file ] && opts="$opts --file $key_file"
out=$(timeout 30 virsh set-user-sshkeys $vm_ID "$user" $opts 2>&1) && report success "ok"
log_debug $vm_ID "set-user-sshkeys failed: $out"
grep -qE "has not been found|not supported|has been disabled" <<< "$out" || report failed "$out"

# path B: guest-exec fallback for old qemu-ga
req=$(jq -cn --arg s "$(cat guest_ssh_keys.sh)" --arg a "$action" --arg u "$user" --arg in "$keys_b64" \
    '{execute:"guest-exec",arguments:{path:"/bin/sh",arg:["-c",$s,"sh",$a,$u],"input-data":$in,"capture-output":true}}')
out=$(virsh qemu-agent-command --timeout 10 $vm_ID "$req" 2>&1) || report failed "guest agent supports neither guest-ssh nor guest-exec"
pid=$(jq -r '.return.pid // empty' <<< "$out")
[ -z "$pid" ] && report failed "guest-exec returned no pid"
for i in $(seq 30); do
    sleep 2
    st=$(virsh qemu-agent-command --timeout 10 $vm_ID '{"execute":"guest-exec-status","arguments":{"pid":'$pid'}}' 2>/dev/null)
    [ "$(jq -r '.return.exited' <<< "$st")" == "true" ] || continue
    [ "$(jq -r '.return.exitcode' <<< "$st")" == "0" ] && report success "ok"
    report failed "$(jq -r '.return["err-data"] // empty' <<< "$st" | base64 -d)"
done
report failed "guest-exec timeout"
