#!/bin/bash
# List VMs on this hypervisor whose qemu-ga has guest-exec-status disabled,
# so vm_top_cpu_procs.sh can never read command output from them. Read-only -
# just reports, doesn't touch guest config. Linux guests only (same scope as
# vm_top_cpu_procs.sh); Windows guests are skipped via guest-get-osinfo.

source /opt/cloudland/scripts/cloudrc

CSV=${CSV:-0} # 1 = emit "hostname,vm_id,status" rows (status: exec_status_disabled|agent_unreachable) instead of pretty text
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

is_windows_vm() {
    local osinfo
    osinfo=$(virsh qemu-agent-command "$1" '{"execute":"guest-get-osinfo"}' 2>/dev/null)
    jq -e '(.return.id // "") | test("mswindows"; "i")' <<<"$osinfo" >/dev/null 2>&1
}

[ "$CSV" = "1" ] && echo "hostname,vm_id,status"

checked_count=0
locked_count=0

for vm_ID in $vm_IDs; do
    is_windows_vm "$vm_ID" && continue

    info=$(virsh qemu-agent-command "$vm_ID" '{"execute":"guest-info"}' 2>&1)
    if [[ "$info" == error:* ]]; then
        if [ "$CSV" = "1" ]; then
            echo "$hostname,$vm_ID,agent_unreachable"
        else
            echo "== $vm_ID: agent unreachable - $info" >&2
        fi
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

if [ "$CSV" != "1" ]; then
    echo >&2
    echo "Checked $checked_count VM(s), $locked_count with guest-exec-status disabled." >&2
fi
