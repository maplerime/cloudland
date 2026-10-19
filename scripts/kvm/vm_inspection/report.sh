#!/bin/bash
# Structured observations, dispositions and cleanup references share async_exec output.
# Debug only: set false before committing to resume collected-report deletion.
inspection_debug_keep_results=true
inspection_max_result_bytes=262144
inspection_max_result_chunks=512
read_vm_isolation() {
    isolation_status=not_requested isolation_cpus='' operation_error=''
    if ! isolation_cpus=$(python3 "$inspection_dir/common/cpu_affinity.py" status "$domain" 2>"$work/isolation.stderr"); then
        isolation_status='' isolation_cpus=''
        operation_error="CPU affinity query failed: $(tail -c 400 "$work/isolation.stderr")"
    elif [ -n "$isolation_cpus" ]; then
        isolation_status=isolated
    fi
}

build_vm_infection_summary() {
    local now
    now=$(date -u +%Y-%m-%dT%H:%M:%S.%6NZ)
    jq -cn --slurpfile opts "$work/action.json" \
      --arg now "$now" --arg status "$isolation_status" \
      --arg cpus "${isolation_cpus:-}" --arg error "${operation_error:-}" '
      $opts[0] as $o | ($o.evaluation // {}) as $e |
      {schema_version:1,instance_uuid:$o.vm_uuid,domain:$o.domain,source_hyper:$o.source_hyper,
       region_id:($o.region_id // ""),
       verdict:($e.verdict // ""),
       ruleset_version:($e.ruleset_version // ""),match_rule_ids:($e.match_rule_ids // []),
       match_summary:($e.match_summary // ""),
       isolation_source:(if $o.action=="record" then (if $status=="isolated" then "existing" else "" end) elif $status=="" then "" else $o.action_source end),
       action:$o.action,action_source:$o.action_source,operation_at:$now,
       collected_at:($o.collected_at // null),inspection_result:($o.inspection_result // null),
       isolation_status:$status,isolation_cpus:$cpus,scan_id:($o.scan_id // ""),
       error_message:($error|.[0:512])}' > "$work/summary.json"
}

record_vm_inspection() {
    build_vm_infection_summary || return 1
    emit_vm_infection_result "$work/summary.json"
}

emit_vm_infection_result() {
    local file=$1 size hash encoded total seq chunk line vm
    [ -s "$file" ] || return 1
    size=$(wc -c < "$file");[ "$size" -le "$inspection_max_result_bytes" ] || return 1
    vm=$(jq -er .instance_uuid "$file") || return 1
    [[ "$vm" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
    hash=$(sha256sum "$file");hash=${hash%% *}
    encoded=$(base64 < "$file" | tr -d '\n')
    total=$(((${#encoded}+767)/768));[ "$total" -le "$inspection_max_result_chunks" ] || return 1
    for ((seq=1;seq<=total;seq++)); do
        chunk=${encoded:$(((seq-1)*768)):768}
        printf -v line "|:-COMMAND-:| vm_infection_result.sh '%s' '%s' '%s' '%s' '%s'" "$vm" "$seq" "$total" "$hash" "$chunk"
        [ "${#line}" -le 1022 ] || return 1
        printf '%s\n' "$line"
    done
}
emit_inspection_report_reference() {
    [ -n "$2" ] || return 0
    printf '|:-VM-INSPECTION-REPORT-:| %s %s\n' "$1" "$2"
}

collect_vm_inspection_job() {
    local file=$1 marker vm scan extra cleanup_pending=false
    sed '/^|:-VM-INSPECTION-REPORT-:| /d' "$file" || return 1
    while read -r marker vm scan extra; do
        [ "$marker" = '|:-VM-INSPECTION-REPORT-:|' ] && [ -z "$extra" ] || continue
        remove_collected_inspection_report "$vm" "$scan" >&2 || cleanup_pending=true
    done < "$file"
    # Retry on the next report_rc cycle while busy or retained for debugging.
    [ "$cleanup_pending" = true ] || [ "$inspection_debug_keep_results" = true ] || rm -f -- "$file"
}

remove_collected_inspection_report() {
    local vm=$1 scan=$2 stamp
    [[ "$vm" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
    stamp=${scan#"${vm}_"}
    [[ "$scan" = "${vm}_$stamp" && "$stamp" =~ ^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z$ ]] || return 1
    (
        # Do not block resource reporting while a scan or disposition is running.
        flock -n 6 || exit 1
        flock -x 7 || exit 1
        # A previous .done must never remove a newer report.
        if [ "$inspection_debug_keep_results" != true ]; then
            rm -f -- "$inspection_root/reports/$vm/$stamp.json.gz" || exit 1
        fi
        # This only marks collection. A later report_rc pass retires the metric.
        source "$(dirname "${BASH_SOURCE[0]}")/metrics.sh"
        mark_inspection_metrics_collected "$vm" "$scan"
    ) 6>"$inspection_run/$vm.work.lock" 7>"$inspection_run/$vm.report.lock"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    source "$(dirname "$0")/../../cloudrc"
    source "$(dirname "$0")/store.sh"
    inspection_init || exit 1
    case "${1:-}" in
        --collect-job)
            [ "$#" -eq 2 ] || exit 1
            collect_vm_inspection_job "$2";;
        --collected)
            [ "$#" -eq 3 ] || exit 1
            remove_collected_inspection_report "$2" "$3";;
        --cleanup-metrics)
            [ "$#" -eq 1 ] || exit 1
            source "$(dirname "$0")/metrics.sh"
            cleanup_collected_inspection_metrics;;
        *) exit 1;;
    esac
fi
