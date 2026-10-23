#!/bin/bash
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
         collected_at:($checks.cputop_processes.collected_at|utc),cpu_basis:"process_lifetime_average_percent",top_n:$top_n,items:$processes,
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
