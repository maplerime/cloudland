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

source "${CLOUDRC:-/opt/cloudland/scripts/cloudrc}"
RPC_TIMEOUT=${RPC_TIMEOUT:-5}

# Return 0 for Windows, 1 for another identified OS, 2 if identification fails.
is_windows_vm() {
    local osinfo rc
    guest_os=''
    osinfo=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$1" '{"execute":"guest-get-osinfo"}' 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        if [ "$rc" -eq 124 ]; then
            echo "== $1: OS detection failed: guest-get-osinfo timed out after ${RPC_TIMEOUT}s (exit=$rc); unlock not attempted" >&2
        else
            echo "== $1: OS detection failed: guest-get-osinfo failed (exit=$rc): ${osinfo:-empty response}; unlock not attempted" >&2
        fi
        return 2
    fi
    if ! guest_os=$(jq -r '.return.id | select(type=="string" and length>0)' <<<"$osinfo" 2>/dev/null) || [ -z "$guest_os" ]; then
        echo "== $1: OS detection failed: guest-get-osinfo returned an invalid response or missing OS id: ${osinfo:-empty response}; unlock not attempted" >&2
        return 2
    fi
    jq -e '(.return.id // "") | test("mswindows"; "i")' <<<"$osinfo" >/dev/null 2>&1
}

# Return 0 for disabled, 1 for explicitly enabled, 2 if verification fails.
is_locked() {
    local info rc enabled
    info=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$1" '{"execute":"guest-info"}' 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        [ "$rc" -ne 124 ] || info="timed out after ${RPC_TIMEOUT}s"
        echo "== $1: RPC verification failed: guest-info (exit=$rc): ${info:-empty response}" >&2
        return 2
    fi
    enabled=$(jq -r -s 'if length==1 then .[0].return.supported_commands[]? | select(.name=="guest-exec-status" and (.enabled|type=="boolean")) | .enabled else empty end' <<<"$info" 2>/dev/null)
    case "$enabled" in
        false) return 0;;
        true) return 1;;
        *) echo "== $1: RPC verification failed: guest-info returned an invalid response or missing guest-exec-status capability: ${info:-empty response}" >&2; return 2;;
    esac
}

unlock_one() {
    local vm_ID=$1
    timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_ID" \
        '{"execute":"guest-exec","arguments":{"path":"/bin/sh","arg":["-c","sed -i '"'"'s/--allow-rpcs=/--allow-rpcs=guest-exec-status,guest-file-open,guest-file-read,guest-file-close,/'"'"' /etc/sysconfig/qemu-ga && systemctl restart qemu-guest-agent"]}}' \
        2>&1
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
    is_windows_vm "$vm_ID"
    os_status=$?
    if [ "$os_status" -eq 2 ]; then
        fail=$((fail + 1))
        continue
    fi
    [ "$os_status" -ne 0 ] || { skipped=$((skipped + 1)); continue; }

    if [ $# -eq 0 ]; then
        is_locked "$vm_ID"
        lock_status=$?
        if [ "$lock_status" -eq 1 ]; then continue; fi
        if [ "$lock_status" -eq 2 ]; then fail=$((fail + 1)); continue; fi
    fi

    unlock_response=$(unlock_one "$vm_ID")
    unlock_rc=$?
    if [ "$unlock_rc" -ne 0 ]; then
        [ "$unlock_rc" -ne 124 ] || unlock_response="timed out after ${RPC_TIMEOUT}s"
        echo "== $vm_ID: unlock guest-exec failed (exit=$unlock_rc): ${unlock_response:-empty response}" >&2
        fail=$((fail + 1))
        continue
    fi
    sleep 5

    is_locked "$vm_ID"
    lock_status=$?
    case "$lock_status" in
        0) echo "== $vm_ID: still locked after unlock attempt" >&2; fail=$((fail + 1));;
        1) echo "== $vm_ID: unlocked (verified)"; ok=$((ok + 1));;
        *) echo "== $vm_ID: unlock result could not be verified" >&2; fail=$((fail + 1));;
    esac
done

echo >&2
echo "Unlocked $ok, failed $fail, skipped $skipped (Windows)." >&2
[ "$fail" -eq 0 ]
