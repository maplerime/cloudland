#!/bin/bash
# Structured observations, dispositions and cleanup references share async_exec output.
# Debug only: set false before committing to resume collected-report deletion.
inspection_debug_keep_results=true
inspection_max_result_bytes=65536
inspection_max_result_chunks=128
build_vm_infection_summary() {
    local now
    now=$(date -u +%Y-%m-%dT%H:%M:%S.%6NZ)
    jq -cn --slurpfile prior "$work/previous.json" --slurpfile opts "$work/action.json" \
      --arg record "$record_uuid" --arg now "$now" --arg status "$isolation_status" \
      --arg cpus "${isolation_cpus:-}" --arg error "${operation_error:-}" '
      $prior[0] as $old | $opts[0] as $o |
      ($o.evaluation // {}) as $e |
      (($e.verdict // "")!="" and $e.verdict!="not_evaluated") as $evaluated |
      {schema_version:1,record_uuid:$record,instance_uuid:$o.vm_uuid,domain:$o.domain,source_hyper:$o.source_hyper,
       region_id:($o.region_id // $old.region_id // ""),
       verdict:(if $evaluated then $e.verdict else ($old.verdict // "") end),
       ruleset_version:(if $evaluated then $e.ruleset_version else ($old.ruleset_version // "") end),
       match_rule_ids:(if $evaluated then $e.match_rule_ids else ($old.match_rule_ids // []) end),
       match_summary:(if $evaluated then $e.match_summary else ($old.match_summary // "") end),
       first_seen_at:(if $old.verdict=="confirmed" then $old.first_seen_at else (now|floor) end),last_seen_at:(now|floor),
       isolation_source:(if ($old.isolation_source // "")!="" then $old.isolation_source
                         elif $o.action=="record" then "" else $o.action_source end),
       action:$o.action,action_source:$o.action_source,operator_id:($o.operator_id // 1),
       operation_at:(if $o.action=="record" then ($old.operation_at // $now) else $now end),
       collected_at:($o.collected_at // $old.collected_at // null),
       inspection_result:($o.inspection_result // $old.inspection_result // null),
       isolation_status:$status,isolation_cpus:$cpus,
       scan_id:($o.scan_id // $old.scan_id // ""),
       remark:(if $o.action_source=="manual" then $o.reason else ($old.remark // "") end),
       error_message:($error|.[0:512])}' > "$work/summary.json"
}

record_vm_inspection() {
    action=record
    load_or_create_infection_record || return 1
    # Refresh observations independently from any prior judgment or CPU disposition.
    isolation_status=$(jq -r '.isolation_status // ""' "$work/previous.json")
    isolation_cpus=$(jq -r '.isolation_cpus // ""' "$work/previous.json")
    operation_error=$(jq -r '.error_message // ""' "$work/previous.json")
    build_vm_infection_summary && save_vm_infection_summary || return 1
    emit_vm_infection_result "$state_dir/result.json"
}

save_vm_infection_summary() {
    [ "$(wc -c < "$work/summary.json")" -le "$inspection_max_result_bytes" ] || return 1
    local tmp
    tmp=$(mktemp "$state_dir/.result.XXXXXXXX") || return 1
    if ! cp "$work/summary.json" "$tmp" || ! mv -- "$tmp" "$state_dir/result.json"; then rm -f -- "$tmp"; return 1; fi
}
emit_vm_infection_result() {
    local file=$1 size hash encoded total seq chunk line record
    [ -s "$file" ] || return 1
    size=$(wc -c < "$file");[ "$size" -le "$inspection_max_result_bytes" ] || return 1
    record=$(jq -er .record_uuid "$file") || return 1
    [[ "$record" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
    hash=$(sha256sum "$file");hash=${hash%% *}
    encoded=$(base64 < "$file" | tr -d '\n')
    total=$(((${#encoded}+767)/768));[ "$total" -le "$inspection_max_result_chunks" ] || return 1
    for ((seq=1;seq<=total;seq++)); do
        chunk=${encoded:$(((seq-1)*768)):768}
        printf -v line "|:-COMMAND-:| vm_infection_result.sh '%s' '%s' '%s' '%s' '%s'" "$record" "$seq" "$total" "$hash" "$chunk"
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
    # Keep only this VM job for retry while its report is still in use.
    [ "$cleanup_pending" = true ] || rm -f -- "$file"
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
        # A previous .done must never remove a newer report or CPU restore state.
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
