#!/bin/bash
is_isolation_enabled() { jq -e '.isolation_enabled==true' "$1" >/dev/null; }
save_cpu_affinity() {
    [ ! -e "$state_dir/affinity.json" ] || return 0
    local tmp
    tmp=$(mktemp "$state_dir/.affinity.XXXXXXXX") || return 1
    if python3 "$inspection_dir/common/cpu_affinity.py" capture "$domain" "$tmp" > "$tmp"; then
        mv -- "$tmp" "$state_dir/affinity.json"
    else rm -f -- "$tmp";return 1;fi
}
apply_cpu_affinity() { python3 "$inspection_dir/common/cpu_affinity.py" apply "$domain" "$state_dir/affinity.json" "$isolation_cpus"; }
restore_cpu_affinity() { python3 "$inspection_dir/common/cpu_affinity.py" restore "$domain" "$state_dir/affinity.json"; }
verify_cpu_affinity() {
    local mode=verify
    [ "${1:-}" != restored ] || mode=verify-restored
    python3 "$inspection_dir/common/cpu_affinity.py" "$mode" "$domain" "$state_dir/affinity.json" "${isolation_cpus:-}"
}
