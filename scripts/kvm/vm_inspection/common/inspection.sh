#!/bin/bash
# Host-side read-only checks; guest execution and polling are shared by all checks.
set -o pipefail
inspection_common_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd) || exit 1
RPC_TIMEOUT=${RPC_TIMEOUT:-5}
GUEST_EXEC_TIMEOUT=${GUEST_EXEC_TIMEOUT:-30}
guest_agent_command() {
    timeout --foreground "$RPC_TIMEOUT" virsh qemu-agent-command "$1" "$2"
}

is_windows_vm() {
    local osinfo rc
    guest_os=''
    osinfo=$(guest_agent_command "$1" '{"execute":"guest-get-osinfo"}' 2>&1)
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

run_guest_script() (
vm_id=$1
inner_b64=$2
RPC_TIMEOUT=${RPC_TIMEOUT:-5}
GUEST_EXEC_TIMEOUT=${GUEST_EXEC_TIMEOUT:-20}
guest_cmd='printf "GUEST_STARTED|%s\n" "$(date +%s)"; echo '
guest_cmd+="$inner_b64"
guest_cmd+=' | base64 -d | /bin/sh; rc=$?; printf "GUEST_FINISHED|%s\n" "$(date +%s)"; exit "$rc"'
request=$(jq -cn --arg cmd "$guest_cmd" '{execute:"guest-exec",arguments:{path:"/bin/sh",arg:["-c",$cmd],"capture-output":true}}')
exec_resp=$(guest_agent_command "$vm_id" "$request" 2>&1)
rc=$?
if [ "$rc" -ne 0 ]; then
    [ "$rc" -ne 124 ] || exec_resp="timed out after ${RPC_TIMEOUT}s"
    echo "ERROR:$vm_id: guest-exec failed (exit=$rc): ${exec_resp:-empty response}"; exit 1
fi
pid=$(echo "$exec_resp" | jq -r -s 'if length==1 then .[0].return.pid | select(type=="number" and .>0 and .==floor) else empty end' 2>/dev/null)
if [ -z "$pid" ]; then echo "ERROR:$vm_id: guest-exec returned an invalid response or missing PID: ${exec_resp:-empty response}"; exit 1; fi
exited=false
for i in $(seq 1 "$GUEST_EXEC_TIMEOUT"); do
    status_resp=$(guest_agent_command "$vm_id" '{"execute":"guest-exec-status","arguments":{"pid":'"$pid"'}}' 2>&1)
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
)

inspect_guest_rpc() (
CSV=${CSV:-0} # 1 = emit "hostname,vm_id,status" rows (status: exec_status_disabled|agent_unreachable|os_detection_failed) instead of pretty text
JSON=${JSON:-0} # Cloudland integration: report capabilities for the specified VM.
hostname=$(hostname -s)

if [ $# -gt 0 ]; then
    vm_IDs="$*"
else
    vm_IDs=$(virsh list --name | grep '^inst-')
fi

if [ -z "$vm_IDs" ]; then
    echo "No running VMs found" >&2
    exit 0
fi

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

    if ! info=$(guest_agent_command "$vm_ID" '{"execute":"guest-info"}' 2>&1) ||
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
)

inspect_processes() (
TOPN=${TOPN:-10}
GUEST_EXEC_TIMEOUT=${GUEST_EXEC_TIMEOUT:-30} # seconds to wait for guest-exec-status to report exited - busy guests are slow to respond, which is exactly what we're targeting
CPU_THRESHOLD=${CPU_THRESHOLD:-50} # host-side qemu %cpu floor to bother checking a VM (skipped when vm_IDs given explicitly)
CSV=${CSV:-0} # 1 = emit "hostname,vm_id,user,pid,proc_pcpu,comm" rows instead of pretty text, for cross-host aggregation
hostname=$(hostname -s)

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

payload=$(printf '%s' "ps -eo user:32,pid,pcpu,comm --sort=-pcpu --no-headers | awk '\$4 != \"ps\"'" | base64 | tr -d '\n')
[ "$CSV" = "1" ] && echo "hostname,vm_id,host_qemu_pcpu,user,pid,proc_pcpu,comm"
for vm_ID in $vm_IDs; do
    host_pcpu=$(host_pcpu_of "$vm_ID")
    raw_out=$(run_guest_script "$vm_ID" "$payload")
    rc=$?
    [ "$CSV" != "1" ] || printf '%s\n' "$raw_out" | sed -n '/^GUEST_STARTED|/p; /^GUEST_FINISHED|/p'
    if [ "$rc" -ne 0 ]; then
        echo "== $vm_ID: process output unavailable: $raw_out" >&2
        continue
    fi
    if [ "$CSV" = "1" ]; then
        printf '%s\n' "$raw_out" | awk -v h="$hostname" -v vm="$vm_ID" -v hp="$host_pcpu" 'NF>=4 {print h","vm","hp","$1","$2","$3","$4}'
    else
        echo "== $vm_ID (host qemu ${host_pcpu}% CPU): USER PID %CPU COMMAND =="
        printf '%s\n' "$raw_out"
    fi
done
)

inspect_wds() (
set -o pipefail
# Preserve the PoC WDS query; Python integers avoid the original 64-bit mask limit.
if [ -z "${wds_address:-}" ]; then exit 0; fi
wds_curl GET 'api/v2/wds/uss' | python3 -c '
import json,sys
host=sys.argv[1]
data=json.load(sys.stdin)
gateways=data.get("uss_gateways")
if not isinstance(gateways,list): raise SystemExit("invalid WDS gateway response")
matched=[g for g in gateways if g.get("server_name")==host]
if not matched: raise SystemExit("no confirmed WDS binding for this node")
for g in matched:
    raw=g.get("core")
    if isinstance(raw,bool) or raw is None: raise SystemExit("missing WDS CPU mask")
    mask=int(raw,0) if isinstance(raw,str) else int(raw)
    if mask<=0: raise SystemExit("invalid or empty WDS CPU mask")
    out={k:g.get(k) for k in ("name","server_name","server_id","core","status")}
    out["core_cpus"]=",".join(str(i) for i in range(mask.bit_length()) if mask & (1<<i))
    print(json.dumps(out))
' "${1:-$(hostname -s)}"
)

inspect_guest_evidence() {
    local domain=$1 check=$2 pids=${3:-} call payload
    case "$check" in
        process)
            [[ "$pids" =~ ^[0-9\ ]+$ ]] || return 1
            call="inspect_process_details '$pids'";;
        cupsd) call=inspect_cups;;
        timestamps) call=inspect_timestamps;;
        *) return 1;;
    esac
    payload=$({ cat "$inspection_common_dir/guest_inspection.sh"; printf '\n%s\n' "$call"; } | base64 | tr -d '\n') || return 1
    run_guest_script "$domain" "$payload"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    source "${CLOUDRC:-/opt/cloudland/scripts/cloudrc}"
    action=${1:-}; shift || exit 1
    case "$action" in
        rpc) inspect_guest_rpc "$@";;
        processes) inspect_processes "$@";;
        wds) inspect_wds "$@";;
        process|cupsd|timestamps) inspect_guest_evidence "$1" "$action" "${2:-}";;
        *) echo "Usage: $0 {rpc|processes|process|cupsd|timestamps|wds} [domain] [pids]" >&2; exit 1;;
    esac
fi
