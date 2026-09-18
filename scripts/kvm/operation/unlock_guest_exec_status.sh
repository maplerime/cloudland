#!/bin/bash
# Unlock guest-exec-status (and guest-file-*) on VMs whose qemu-ga has them
# disabled, by patching --allow-rpcs= in the guest and restarting its agent.
# This reverses what looks like a deliberate anti-exfiltration boundary on
# the guest - run it deliberately, per host/VM, not blindly fleet-wide.
#
# Usage: unlock_guest_exec_status.sh [inst-ID ...]
#   No args: unlock every running (non-Windows) VM on this hypervisor whose
#            guest-exec-status is currently disabled.
#   With args: unlock exactly the given VMs (skips the disabled-check, fires
#              the patch unconditionally).

source /opt/cloudland/scripts/cloudrc

is_windows_vm() {
    local osinfo
    osinfo=$(virsh qemu-agent-command "$1" '{"execute":"guest-get-osinfo"}' 2>/dev/null)
    jq -e '(.return.id // "") | test("mswindows"; "i")' <<<"$osinfo" >/dev/null 2>&1
}

is_locked() {
    local info
    info=$(virsh qemu-agent-command "$1" '{"execute":"guest-info"}' 2>/dev/null)
    [ -z "$info" ] && return 1
    [ "$(jq -r '[.return.supported_commands[] | select(.name=="guest-exec-status" and .enabled==false)] | length' <<<"$info" 2>/dev/null)" = "1" ]
}

unlock_one() {
    local vm_ID=$1
    virsh qemu-agent-command "$vm_ID" \
        '{"execute":"guest-exec","arguments":{"path":"/bin/sh","arg":["-c","sed -i '"'"'s/--allow-rpcs=/--allow-rpcs=guest-exec-status,guest-file-open,guest-file-read,guest-file-close,/'"'"' /etc/sysconfig/qemu-ga && systemctl restart qemu-guest-agent"]}}' \
        >/dev/null 2>&1
}

if [ $# -gt 0 ]; then
    vm_IDs="$*"
else
    vm_IDs=$(virsh list --name | grep '^inst-')
fi

ok=0
fail=0
skipped=0

for vm_ID in $vm_IDs; do
    is_windows_vm "$vm_ID" && { skipped=$((skipped + 1)); continue; }

    if [ $# -eq 0 ] && ! is_locked "$vm_ID"; then
        continue # already unlocked or agent unreachable - nothing to do
    fi

    unlock_one "$vm_ID"
    sleep 5

    if is_locked "$vm_ID"; then
        echo "== $vm_ID: still locked after unlock attempt" >&2
        fail=$((fail + 1))
    else
        echo "== $vm_ID: unlocked"
        ok=$((ok + 1))
    fi
done

echo >&2
echo "Unlocked $ok, failed $fail, skipped $skipped (Windows)." >&2
