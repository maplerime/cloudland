#!/bin/bash
evaluate_saved_inspection() {
    bash "$inspection_dir/common/evaluate_inspection.sh" "$1" "$2" > "$work/evaluation.json" || return 1
    jq -e -s 'length==1 and (.[0] |
      (.verdict=="confirmed" or .verdict=="suspected" or .verdict=="no_match" or .verdict=="not_evaluated") and
      (.ruleset_version|type=="string" and length<=128) and
      (.match_rule_ids|type=="array" and length<=4096 and all(type=="string" and length>0 and length<=128)) and
      (.match_rule_ids|tojson|utf8bytelength<=65536) and
      (.match_summary|type=="string" and length<=256) and (.pin_requested|type=="boolean")
    )' "$work/evaluation.json" >/dev/null
}
