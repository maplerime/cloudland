#!/bin/bash
# Structured observations, dispositions and cleanup references share async_exec output.
# Debug only: set false before committing to resume collected-report deletion.
inspection_debug_keep_results=true
inspection_max_result_bytes=262144
inspection_max_result_chunks=512
read_vm_isolation() {
    isolation_status=not_requested isolation_cpus='' operation_error=''
    if ! isolation_cpus=$(python3 "$inspection_dir/common/configuration.py" status "$domain" 2>"$work/isolation.stderr"); then
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
       isolation_skip_reason:(if $o.action!="record" then ""
         elif $status=="" then "affinity_query_failed"
         elif $status!="not_requested" and $status!="disabled" then ""
         elif $e.verdict=="no_match" then "no_match"
         elif $e.verdict=="not_evaluated" then "not_evaluated"
         elif ($o.isolation_skip_reason // "")!="" then $o.isolation_skip_reason
         elif $o.isolation_enabled==false then "configuration_disabled"
         elif $e.pin_requested==false then "match_policy"
         else "" end),
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
    # Keep debug evidence, but retry this queue item only while cleanup is busy.
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
        # A previous .done must never remove a newer report.
        if [ "$inspection_debug_keep_results" != true ]; then
            rm -f -- "$inspection_root/reports/$vm/$stamp.json.gz" || exit 1
        fi
        # This only marks collection. A later report_rc pass retires the metric.
        mark_inspection_metrics_collected "$vm" "$scan"
    ) 6>"$inspection_run/$vm.work.lock" 7>"$inspection_run/$vm.report.lock"
}

# Parse the original PoC script outputs into the complete node-local report.
build_inspection_result() {
    local finished part rc collected ended code
    finished=$(date +%s)
    [ -s "$work/agent.json" ] || printf '%s\n' '{"status":"failed","error_code":"scan_incomplete","reachable":false,"guest_exec_enabled":false,"guest_exec_status_enabled":false,"unlock_attempted":false,"unlock_status":"not_attempted"}' > "$work/agent.json"
    for part in cputop_processes cupsd timestamps wds; do
        [ -f "$work/$part.out" ] || : > "$work/$part.out"
        rc=$(cat "$work/$part.rc" 2>/dev/null)
        collected=$(cat "$work/$part.collected_at" 2>/dev/null)
        ended=$(cat "$work/$part.finished_at" 2>/dev/null)
        code=command_failed
        case "$rc" in 0) code='';; 124|137) code=command_timeout;; '') code=not_collected;; esac
        if [ -z "$rc" ] && [ -n "$collected" ] && [ "${failure_reason:-}" = scan_timeout ]; then code=command_timeout; fi
        jq -cn --arg name "$part" --arg code "$code" --arg collected "$collected" --arg ended "$ended" --rawfile output "$work/$part.out" '
          def number: try tonumber catch null;
          def stamp($name): [$output|split("\n")[]|split("|")|select(length==2 and .[0]==$name)|.[1]|number]|.[0];
          ($collected|number) as $start | ($ended|number) as $end_time |
          stamp("GUEST_STARTED") as $gs | stamp("GUEST_FINISHED") as $ge |
          {key:$name,value:{status:(if $code=="" then "ok" else "failed" end),error_code:$code,collected_at:$start,
           started_at:$start,finished_at:$end_time,duration_ms:(if $start!=null and $end_time!=null then ($end_time-$start)*1000 else null end),
           guest_started_at:$gs,guest_finished_at:$ge,guest_duration_ms:(if $gs!=null and $ge!=null then ($ge-$gs)*1000 else null end)}}'
    done | jq -s 'from_entries' > "$work/steps.json" || return 1
    # Preserve the PoC gateway/core query and convert only its output representation.
    if [ "$(cat "$work/wds.rc" 2>/dev/null)" = 0 ] && jq -cs '
      map(.core_cpus = ((.core_cpus // "") | split(",") | map(select(length>0)|tonumber)))
      ' "$work/wds.out" > "$work/wds.parsed.json"; then :
    else
        printf '%s\n' '[]' > "$work/wds.parsed.json"
        jq '.wds.status="failed" | .wds.error_code="wds_query_failed"' "$work/steps.json" > "$work/steps.tmp" && mv "$work/steps.tmp" "$work/steps.json"
    fi
    jq -cn --slurpfile target "$work/target.json" --slurpfile agent "$work/agent.json" \
      --slurpfile steps "$work/steps.json" --slurpfile wds "$work/wds.parsed.json" \
      --rawfile cpu "$work/cputop_processes.out" --rawfile features "$work/cupsd.out" --rawfile times "$work/timestamps.out" \
      --argjson top_n "$inspection_top_n" --arg host "$(hostname -s)" --argjson started "$started" --argjson finished "$finished" --arg reason "${failure_reason:-}" '
      def number: try tonumber catch null;
      def utc: if .==null then null else strftime("%Y-%m-%dT%H:%M:%SZ") end;
      def value($k): .[$k] | if .status=="ok" then .value else null end;
      def numeric($k): value($k)|number;
      def boolean($k): value($k)|if .==null or .=="null" then null else .=="true" end;
      ($steps[0]) as $s |
      ([($features+"\n"+$times)|split("\n")[]|split("|")|select(length==6 and .[0]=="CHECK")|
         {key:.[1],value:{status:.[2],error_code:.[3],error_message:(.[4]|@base64d),value:(.[5]|@base64d)}}]|from_entries) as $reported |
      (["package_status","binary_exists","binary_size_bytes","keepalive_exists","hidden_dir_exists","connection_peer","binary_mtime","keepalive_mtime"]|
         map(. as $k|{key:$k,value:($reported[$k] // {status:"failed",error_code:"not_collected",value:null})})|from_entries) as $items |
      (if $s.cputop_processes.status=="ok" then
         [$cpu|split("\n")[]|split(",")|select(length>=7 and .[1]==$target[0].domain)|
           {user:.[3],pid:(.[4]|number),cpu_percent:(.[5]|number),name:(.[6:]|join(","))}]
       else [] end) as $processes |
      (if $s.cupsd.status=="ok" then
         ([$features|capture("(?m)^RESULT:(?<pkg>yes|no):(?<size>[0-9]+):(?<cron>yes|no):(?<hidden>yes|no):(?<conn>[^\n]*)")]|.[0])
       else null end) as $cups |
      (if $s.timestamps.status=="ok" then
         ([$times|split("\n")[]|select(startswith("STAT|"))|split("|")]|.[0])
       else null end) as $stat |
      ($s | .cputop_processes.status=(if .cputop_processes.status=="ok" and ($processes|length)>0 then "ok" else "failed" end) |
         .cupsd.status=(if $cups!=null and ([$items|to_entries[]|select(.key!="binary_mtime" and .key!="keepalive_mtime")]|all(.value.status=="ok")) then "ok" else "failed" end) |
         .timestamps.status=(if $stat!=null and ($stat|length)==3 and $items.binary_mtime.status=="ok" and $items.keepalive_mtime.status=="ok" then "ok" else "failed" end) |
         with_entries(if .value.status=="failed" and .value.error_code=="" then .value.error_code="collection_incomplete" else . end)) as $checks |
      {schema_version:1,meta:$target[0],node_hostname:$host,
       started_at:($started|utc),finished_at:($finished|utc),duration_ms:(($finished-$started)*1000),
       agent:($agent[0] | .error_message=(.error_message // .error_code // "") | del(.error_code)),
       cputop_processes:{status:$checks.cputop_processes.status,
         collected_at:($checks.cputop_processes.collected_at|utc),cpu_basis:"process_lifetime_average_percent",top_n:$top_n,process_count:($processes|length),items:($processes[:$top_n]),
         error_message:(if $checks.cputop_processes.status=="ok" then "" elif $reason!="" then $reason else $checks.cputop_processes.error_code end)},
       required_check:{cupsd:{
         status:(if $checks.cupsd.status=="ok" and $checks.timestamps.status=="ok" then "ok" else "failed" end),
         package_status:($items|value("package_status")),
         binary_exists:($items|boolean("binary_exists")),binary_size_bytes:($items|numeric("binary_size_bytes")),binary_mtime:($items|numeric("binary_mtime")|utc),
         keepalive_exists:($items|boolean("keepalive_exists")),keepalive_mtime:($items|numeric("keepalive_mtime")|utc),
         hidden_dir_exists:($items|boolean("hidden_dir_exists")),connection_peers:([($items|value("connection_peer"))]|map(select(.!=null and .!=""))),
         error_message:([
           ($items|to_entries[]|select(.value.status!="ok")|.key+": "+(.value.error_message // .value.error_code // "not_collected")),
           (if $checks.cupsd.status!="ok" then "cupsd: "+$checks.cupsd.error_code else empty end),
           (if $checks.timestamps.status!="ok" then "timestamps: "+$checks.timestamps.error_code else empty end)
         ]|join("; ")),
         raw_output:$features,timestamp_output:$times}},
       wds:{status:$checks.wds.status,gateways:$wds[0],error_message:$checks.wds.error_code}} |
      .collection_status=(if $reason=="vm_identity_mismatch" then "failed"
        elif .agent.status=="ok" and ($checks|all(.status=="ok")) and $reason=="" then "complete"
        elif any($checks[]; .status=="ok") then "partial" else "failed" end)
      ' > "$work/result.json" || return 1
    python3 "$inspection_dir/common/match_inspection.py" merge "$work"
}

# Return structured observations without duplicating raw command output in the DB.
build_vm_inspection_data() {
    jq '{schema_version,collection_status,started_at,finished_at,duration_ms,
         agent,cputop_processes,required_check:(.required_check | .cupsd |= del(.raw_output,.timestamp_output)),
         wds,hive,match_policy}' "$work/report.json" > "$work/inspection.json"
}

# Metric commands share the scan lifecycle implementation.
inspection_metrics_script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/metrics.py"
export_infection_metrics() {
    python3 "$inspection_metrics_script" export "$inspection_run" "$1" "$2" "$3"
}
mark_inspection_metrics_collected() {
    python3 "$inspection_metrics_script" collected "$inspection_run" "$1" "$2"
}
cleanup_collected_inspection_metrics() {
    python3 "$inspection_metrics_script" cleanup "$inspection_run"
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
            cleanup_collected_inspection_metrics;;
        *) exit 1;;
    esac
fi
