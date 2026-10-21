#!/bin/bash
evaluate_saved_inspection() {
    local report=$1 library_url=${2:-}
    [ -s "$report" ] || return 1
    if [ -n "${library_url//[[:space:]]/}" ]; then
        bash "$inspection_dir/common/evaluate_inspection.sh" "$report" "$library_url" > "$work/evaluation.json" || return 1
    else
        printf '%s\n' '{"verdict":"not_evaluated","ruleset_version":"","match_rule_ids":[],"match_summary":""}' > "$work/evaluation.json" || return 1
    fi
    jq -e -s '
      length==1 and (.[0] |
      (.verdict=="confirmed" or .verdict=="no_match" or .verdict=="not_evaluated") and
      (.ruleset_version|type=="string" and length<=128) and
      (.match_rule_ids|type=="array" and length<=16 and all(type=="string" and length>0 and length<=128)) and
      (.match_rule_ids|tojson|utf8bytelength<=1024) and
      (.match_summary|type=="string" and length<=256)
      )
    ' "$work/evaluation.json" >/dev/null
}
