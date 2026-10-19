#!/bin/bash
# List VMs on this hypervisor whose qemu-ga has guest-exec-status disabled,
# so vm_top_cpu_procs.sh can never read command output from them. Read-only -
# just reports, doesn't touch guest config. Linux guests only (same scope as
# vm_top_cpu_procs.sh); Windows guests are skipped via guest-get-osinfo.

source "${CLOUDRC:-/opt/cloudland/scripts/cloudrc}"
RPC_TIMEOUT=${RPC_TIMEOUT:-5}

CSV=${CSV:-0} # 1 = emit "hostname,vm_id,status" rows (status: exec_status_disabled|agent_unreachable|os_detection_failed) instead of pretty text
JSON=${JSON:-0} # Cloudland integration: report capabilities for the specified VM.
hostname=$(hostname -s)

usage() {
    echo "Usage: $0 [inst-ID ...]"
    echo "  No args: check all running VMs on this hypervisor."
    echo "  With args: check exactly the given VMs."
    echo "  CSV=1 env var emits 'hostname,vm_id,status' rows instead of pretty text."
}
[ "$1" = "-h" ] && usage && exit 0

if [ $# -gt 0 ]; then
    vm_IDs="$*"
else
    vm_IDs=$(virsh list --name | grep '^inst-')
fi

if [ -z "$vm_IDs" ]; then
    echo "No running VMs found" >&2
    exit 0
fi

# Return 0 for Windows, 1 for another identified OS, 2 if identification fails.
is_windows_vm() {
    local osinfo
    guest_os=''
    osinfo=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$1" '{"execute":"guest-get-osinfo"}' 2>/dev/null) || return 2
    guest_os=$(jq -r '.return.id | select(type=="string" and length>0)' <<<"$osinfo" 2>/dev/null) || return 2
    [ -n "$guest_os" ] || return 2
    jq -e '(.return.id // "") | test("mswindows"; "i")' <<<"$osinfo" >/dev/null 2>&1
}

report_json() {
    jq -cn --arg state "$1" --arg code "${2:-}" --arg os "${guest_os:-}" --argjson info "${info:-null}" '
      {status:$state,error_code:$code,guest_os:$os,reachable:($state=="skipped" or $info.return!=null),
       guest_exec_enabled:any($info.return.supported_commands[]?; .name=="guest-exec" and .enabled==true),
       guest_exec_status_enabled:any($info.return.supported_commands[]?; .name=="guest-exec-status" and .enabled==true)}'
}

[ "$CSV" = "1" ] && [ "$JSON" != "1" ] && echo "hostname,vm_id,status"

checked_count=0
locked_count=0

for vm_ID in $vm_IDs; do
    info=null
    is_windows_vm "$vm_ID"
    os_status=$?
    if [ "$os_status" -eq 0 ]; then
        [ "$JSON" != "1" ] || report_json skipped windows_unsupported
        continue
    fi

    if ! info=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_ID" '{"execute":"guest-info"}' 2>&1) ||
       [ -z "$info" ] || ! jq -e '.return.supported_commands | type=="array"' <<<"$info" >/dev/null 2>&1; then
        if [ "$JSON" = "1" ]; then
            info=null
            report_json failed agent_unreachable
        elif [ "$CSV" = "1" ]; then
            echo "$hostname,$vm_ID,agent_unreachable"
        else
            echo "== $vm_ID: agent unreachable - $info" >&2
        fi
        continue
    fi

    if [ "$os_status" -eq 2 ]; then
        if [ "$JSON" = "1" ]; then
            report_json failed os_detection_failed
        elif [ "$CSV" = "1" ]; then
            echo "$hostname,$vm_ID,os_detection_failed"
        else
            echo "== $vm_ID: OS detection failed" >&2
        fi
        continue
    fi

    if [ "$JSON" = "1" ]; then
        report_json ok
        continue
    fi

    checked_count=$((checked_count + 1))
    disabled=$(jq -r '[.return.supported_commands[] | select(.name=="guest-exec-status" and .enabled==false)] | length' <<<"$info" 2>/dev/null)
    if [ "$disabled" = "1" ]; then
        locked_count=$((locked_count + 1))
        if [ "$CSV" = "1" ]; then
            echo "$hostname,$vm_ID,exec_status_disabled"
        else
            echo "$vm_ID"
        fi
    fi
done

if [ "$CSV" != "1" ] && [ "$JSON" != "1" ]; then
    echo >&2
    echo "Checked $checked_count VM(s), $locked_count with guest-exec-status disabled." >&2
fi
