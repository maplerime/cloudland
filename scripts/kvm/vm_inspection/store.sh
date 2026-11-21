#!/bin/bash
# Reports wait for report_rc; operation state belongs to the controller database.
inspection_top_n=10
inspection_concurrency=6
inspection_rpc_timeout=5
inspection_guest_timeout=45
inspection_wds_timeout=10
inspection_scan_timeout=90
inspection_max_report_bytes=262144
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
