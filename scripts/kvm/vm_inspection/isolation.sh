#!/bin/bash
is_isolation_enabled() { jq -e '.isolation_enabled==true' "$1" >/dev/null; }
apply_cpu_affinity() { python3 "$inspection_dir/common/cpu_affinity.py" apply "$domain" "$isolation_cpus"; }
release_cpu_affinity() { python3 "$inspection_dir/common/cpu_affinity.py" release "$domain"; }
verify_cpu_affinity() { python3 "$inspection_dir/common/cpu_affinity.py" verify "$domain" "$isolation_cpus"; }
