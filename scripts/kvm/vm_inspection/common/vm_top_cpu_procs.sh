#!/bin/bash
# Report the top CPU-consuming processes (with owning user) inside each VM
# on this hypervisor, via QEMU guest agent guest-exec. Linux guests only -
# Windows VMs are detected via guest-get-osinfo and skipped.

source "${CLOUDRC:-/opt/cloudland/scripts/cloudrc}"
RPC_TIMEOUT=${RPC_TIMEOUT:-5}

TOPN=${TOPN:-10}
GUEST_EXEC_TIMEOUT=${GUEST_EXEC_TIMEOUT:-30} # seconds to wait for guest-exec-status to report exited - busy guests are slow to respond, which is exactly what we're targeting
CPU_THRESHOLD=${CPU_THRESHOLD:-50} # host-side qemu %cpu floor to bother checking a VM (skipped when vm_IDs given explicitly)
CSV=${CSV:-0} # 1 = emit "hostname,vm_id,user,pid,proc_pcpu,comm" rows instead of pretty text, for cross-host aggregation
hostname=$(hostname -s)

usage() {
    echo "Usage: $0 [inst-ID ...]"
    echo "  No args: pre-filter by host-side qemu process %CPU (>= \$CPU_THRESHOLD, default 50), then scan only those VMs."
    echo "  With args: scan exactly the given VMs, no pre-filter."
    echo "  TOPN=<n> env var overrides top-process count (default 10)."
    echo "  CPU_THRESHOLD=<n> env var overrides the host-side qemu %cpu floor (default 50)."
    echo "  GUEST_EXEC_TIMEOUT=<n> env var overrides seconds to wait per VM for ps output (default 30)."
    echo "  CSV=1 env var emits 'hostname,vm_id,host_qemu_pcpu,user,pid,proc_pcpu,comm' rows instead of pretty text."
    echo "  Windows guests are auto-detected (guest-get-osinfo) and skipped."
}
[ "$1" = "-h" ] && usage && exit 0

# Host-side qemu %cpu per VM (one ps call), keyed by vm_id - used both to
# pre-filter which VMs are worth guest-exec'ing into, and as a CSV/report column.
host_pcpu_map=$(ps -eo pcpu,args --no-headers \
    | awk '
        /guest=inst-/ {
            name = ""
            for (i = 1; i <= NF; i++) {
                if ($i ~ /^guest=inst-/) { split($i, a, "="); split(a[2], b, ","); name = b[1] }
            }
            if (name != "") print name, $1
        }')
host_pcpu_of() {
    awk -v vm="$1" '$1==vm{print $2; exit}' <<<"$host_pcpu_map"
}

if [ $# -gt 0 ]; then
    vm_IDs="$*"
else
    vm_IDs=$(awk -v t="$CPU_THRESHOLD" '$2+0>=t{print $2, $1}' <<<"$host_pcpu_map" | sort -rn)
    if [ -z "$vm_IDs" ]; then
        echo "No VM qemu process >= ${CPU_THRESHOLD}% CPU on this host" >&2
        exit 0
    fi
    echo "Host-side hot VMs (qemu %CPU >= ${CPU_THRESHOLD}):" >&2
    echo "$vm_IDs" | awk '{printf "  %-20s %s%%\n", $2, $1}' >&2
    vm_IDs=$(echo "$vm_IDs" | awk '{print $2}')
fi

if [ -z "$vm_IDs" ]; then
    echo "No running VMs found" >&2
    exit 0
fi

# Windows guests don't have /bin/sh; skip them via a cheap guest-get-osinfo
# probe (single request, no exec-status polling) instead of failing later.
# Return 0 for Windows, 1 for another identified OS, 2 if identification fails.
is_windows_vm() {
    local osinfo rc
    guest_os=''
    osinfo=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$1" '{"execute":"guest-get-osinfo"}' 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        if [ "$rc" -eq 124 ]; then
            echo "== $1: OS detection failed: guest-get-osinfo timed out after ${RPC_TIMEOUT}s (exit=$rc); process collection not started" >&2
        else
            echo "== $1: OS detection failed: guest-get-osinfo failed (exit=$rc): ${osinfo:-empty response}; process collection not started" >&2
        fi
        return 2
    fi
    if ! guest_os=$(jq -r '.return.id | select(type=="string" and length>0)' <<<"$osinfo" 2>/dev/null) || [ -z "$guest_os" ]; then
        echo "== $1: OS detection failed: guest-get-osinfo returned an invalid response or missing OS id: ${osinfo:-empty response}; process collection not started" >&2
        return 2
    fi
    jq -e '(.return.id // "") | test("mswindows"; "i")' <<<"$osinfo" >/dev/null 2>&1
}

linux_IDs=""
for vm_ID in $vm_IDs; do
    is_windows_vm "$vm_ID"
    os_status=$?
    if [ "$os_status" -eq 2 ]; then
        continue
    fi
    if [ "$os_status" -eq 0 ]; then
        echo "== $vm_ID: skipped (Windows guest)" >&2
        continue
    fi
    linux_IDs="$linux_IDs $vm_ID"
done
vm_IDs=$linux_IDs

if [ -z "$vm_IDs" ]; then
    echo "No VM process data collected. See per-VM failure or skip reasons above." >&2
    exit 0
fi

ps_cmd='printf "GUEST_STARTED|%s\n" "$(date +%s)"; ps -eo user:32,pid,pcpu,comm --sort=-pcpu --no-headers | head -n '"$TOPN"'; rc=$?; printf "GUEST_FINISHED|%s\n" "$(date +%s)"; exit "$rc"'

[ "$CSV" = "1" ] && echo "hostname,vm_id,host_qemu_pcpu,user,pid,proc_pcpu,comm"

for vm_ID in $vm_IDs; do
    host_pcpu=$(host_pcpu_of "$vm_ID")
    request=$(jq -cn --arg cmd "$ps_cmd" '{execute:"guest-exec",arguments:{path:"/bin/sh",arg:["-c",$cmd],"capture-output":true}}')
    exec_resp=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_ID" "$request" 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        [ "$rc" -ne 124 ] || exec_resp="timed out after ${RPC_TIMEOUT}s"
        echo "== $vm_ID: guest-exec failed (exit=$rc): ${exec_resp:-empty response}; process output unavailable" >&2
        continue
    fi
    pid=$(jq -r -s 'if length==1 then .[0].return.pid | select(type=="number" and .>0 and .==floor) else empty end' <<<"$exec_resp" 2>/dev/null)
    if [ -z "$pid" ]; then
        echo "== $vm_ID: guest-exec returned an invalid response or missing PID: ${exec_resp:-empty response}; process output unavailable" >&2
        continue
    fi

    status_resp=""
    exited="false"
    for i in $(seq 1 $GUEST_EXEC_TIMEOUT); do
        status_resp=$(timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$vm_ID" \
            '{"execute":"guest-exec-status","arguments":{"pid":'"$pid"'}}' 2>&1)
        rc=$?
        if [ "$rc" -eq 124 ]; then
            echo "== $vm_ID: guest-exec-status timed out after ${RPC_TIMEOUT}s (exit=$rc); process output unavailable" >&2
            exited=failed
            break
        fi
        # virsh errors (e.g. RPC disabled on this guest's qemu-ga) come back as
        # plain "error: ..." text, not JSON - fail fast instead of burning the
        # whole timeout retrying a request that will never succeed.
        case "$status_resp" in
            error:*)
                echo "== $vm_ID: guest-exec-status unavailable (exit=$rc) - $status_resp" >&2
                if [[ "$status_resp" == *"has been disabled"* ]]; then
                    echo "   this guest's qemu-ga blocks reading command output (guest-exec-status/guest-file-*)." >&2
                    echo "   to unlock manually: virsh qemu-agent-command $vm_ID '{\"execute\":\"guest-exec\",\"arguments\":{\"path\":\"/bin/sh\",\"arg\":[\"-c\",\"sed -i \x27s/--allow-rpcs=/--allow-rpcs=guest-exec-status,guest-file-open,guest-file-read,guest-file-close,/\x27 /etc/sysconfig/qemu-ga && systemctl restart qemu-guest-agent\"]}}' then re-run this script." >&2
                fi
                exited="disabled"
                break
                ;;
        esac
        if [ "$rc" -ne 0 ]; then
            echo "== $vm_ID: guest-exec-status failed (exit=$rc): ${status_resp:-empty response}; process output unavailable" >&2
            exited=failed
            break
        fi
        if ! jq -e -s 'length==1 and (.[0].return|type=="object") and (.[0].return.exited|type=="boolean")' <<<"$status_resp" >/dev/null 2>&1; then
            echo "== $vm_ID: guest-exec-status returned an invalid response or missing exited flag: ${status_resp:-empty response}; process output unavailable" >&2
            exited=failed
            break
        fi
        exited=$(jq -r '.return.exited' <<<"$status_resp" 2>/dev/null)
        [ "$exited" = "true" ] && break
        sleep 1
    done

    case "$exited" in disabled|failed) continue;; esac

    if [ "$exited" != "true" ]; then
        echo "== $vm_ID: guest command did not finish after $GUEST_EXEC_TIMEOUT status checks (pid=$pid); process output unavailable" >&2
        continue
    fi

    raw_out=$(jq -r '.return."out-data" // empty' <<<"$status_resp" | base64 -d)
    if [ "$CSV" = "1" ]; then printf '%s\n' "$raw_out" | sed -n '/^GUEST_STARTED|/p; /^GUEST_FINISHED|/p'; fi
    if ! jq -e '.return.exitcode==0 and (.return."out-truncated"!=true)' <<<"$status_resp" >/dev/null 2>&1; then
        echo "== $vm_ID: guest command failed or output was truncated (exitcode=$(jq -r .return.exitcode <<<"$status_resp"), truncated=$(jq -r '.return["out-truncated"] // false' <<<"$status_resp"))" >&2
        continue
    fi
    if [ "$CSV" = "1" ]; then
        echo "$raw_out" | awk -v h="$hostname" -v vm="$vm_ID" -v hp="$host_pcpu" 'NF>=4 {print h","vm","hp","$1","$2","$3","$4}'
    else
        echo "== $vm_ID (host qemu ${host_pcpu}% CPU - top $TOPN CPU processes: USER PID %CPU COMMAND) =="
        echo "$raw_out"
        echo
    fi
done
