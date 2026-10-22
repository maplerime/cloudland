#!/bin/bash
# Reports wait for report_rc; operation state belongs to the controller database.
inspection_top_n=10
inspection_concurrency=6
inspection_rpc_timeout=5
inspection_guest_timeout=45
inspection_wds_timeout=10
inspection_scan_timeout=90
inspection_max_report_bytes=65536
inspection_init() {
    umask 077
    export cache_dir=${cache_dir:-/opt/cloudland/cache} run_dir=${run_dir:-/opt/cloudland/run}
    inspection_root=$cache_dir/vm_inspection
    inspection_run=$run_dir/vm_inspection
    mkdir -p "$inspection_root/reports" "$inspection_run" || return 1
}
get_inspection_vm_uuid() {
    [[ "$1" =~ ^[A-Za-z0-9_.-]{1,128}$ ]] || return 1
    python3 - "$1" <<'CHECK'
import subprocess,sys,uuid,xml.etree.ElementTree as ET
p=subprocess.run(['virsh','dumpxml',sys.argv[1]],check=True,capture_output=True,text=True,timeout=15)
values=[e.text for e in ET.fromstring(p.stdout).iter() if e.tag.rsplit('}',1)[-1]=='instance_id']
if len(values)!=1: raise SystemExit('VM instance_id missing or ambiguous')
print(uuid.UUID(values[0]))
CHECK
}
validate_inspection_target() {
    [ "$(get_inspection_vm_uuid "$2")" = "$1" ]
}
lock_inspection_vm() {
    exec 8>"$inspection_run/$domain.affinity.lock" || return 1
    flock -w 120 8
}
lock_inspection_work() {
    # Separate from the affinity lock, which is also used by CPU quota adjustment.
    exec 6>"$inspection_run/$1.work.lock" || return 1
    flock -w 120 6
}
# Each compute node has six slots. Wait for any slot before starting the scan budget.
acquire_inspection_slot() {
    local slot
    while :; do
        for ((slot=1;slot<=inspection_concurrency;slot++)); do
            exec 9>"$inspection_run/slot.$slot" || return 1
            if flock -n 9; then return 0; fi
            exec 9>&-
        done
        sleep 1 || return 1
    done
}
save_inspection_report() {
    local stamp target tmp size
    stamp=${scan_stamp:-$(date -u +%Y%m%dT%H%M%S.%NZ)}
    scan_id="${vm}_${stamp}"
    mkdir -p "$inspection_root/reports/$vm" || return 1
    target="$inspection_root/reports/$vm/$stamp.json.gz"
    jq --arg scan "$scan_id" --slurpfile evaluation "$work/evaluation.json" \
        '. + {scan_id:$scan,evaluation:$evaluation[0]}' "$work/result.json" > "$work/report.json" || return 1
    size=$(wc -c < "$work/report.json")
    [ "$size" -le "$inspection_max_report_bytes" ] || { echo 'report exceeds size limit' >&2; return 1; }
    tmp=$(mktemp "$inspection_root/reports/$vm/.report.XXXXXXXX") || return 1
    gzip -c "$work/report.json" > "$tmp" || { rm -f -- "$tmp"; return 1; }
    (
        flock -x 7 || exit 1
        # Concurrent scans may finish out of order. Keep the newest scan only.
        for report in "$inspection_root/reports/$vm/"*.json.gz; do
            [ -f "$report" ] || continue
            if [[ "$report" > "$target" ]]; then rm -f -- "$tmp"; exit 0; fi
        done
        mv -- "$tmp" "$target" || exit 1
        for report in "$inspection_root/reports/$vm/"*.json.gz; do
            [ "$report" = "$target" ] || rm -f -- "$report" || exit 1
        done
    ) 7>"$inspection_run/$vm.report.lock"
    local rc=$?
    [ "$rc" -eq 0 ] || rm -f -- "$tmp"
    return "$rc"
}
