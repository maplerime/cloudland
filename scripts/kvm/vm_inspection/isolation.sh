#!/bin/bash
is_isolation_enabled() { jq -e '.isolation_enabled==true' "$1" >/dev/null; }
apply_cpu_affinity() { python3 "$inspection_dir/common/configuration.py" apply "$domain" "$isolation_cpus"; }
release_cpu_affinity() { python3 "$inspection_dir/common/configuration.py" release "$domain"; }
verify_cpu_affinity() { python3 "$inspection_dir/common/configuration.py" verify "$domain" "$isolation_cpus"; }

select_quarantine_cpus() (
    set -o pipefail
    # Refresh WDS bindings before isolation and reuse the normal CPU limit calculation.
    timeout -k 2 10 bash "$inspection_dir/common/inspection.sh" wds |
        python3 "$inspection_dir/common/configuration.py" select "$domain" "${wds_address:-}"
)

quarantine_vm_cpu() {
    isolation_status=disabled isolation_cpus='' operation_error=''
    if [ "$(jq -r .action_source "$work/action.json")" = manual ] || is_isolation_enabled "$work/action.json"; then
        isolation_status=failed
        if isolation_cpus=$(select_quarantine_cpus 2>"$work/isolation.stderr"); then
            if verify_cpu_affinity 2>/dev/null && [ "$(jq -r .action_source "$work/action.json")" = automatic ]; then
                # Preserve the operation time when automatic isolation already matches.
                isolation_status=isolated
            elif apply_cpu_affinity 2>>"$work/isolation.stderr" && verify_cpu_affinity 2>>"$work/isolation.stderr"; then
                isolation_status=isolated
            else
                operation_error="CPU isolation failed: $(tail -c 256 "$work/isolation.stderr")"
                if ! release_cpu_affinity 2>>"$work/isolation.stderr"; then
                    operation_error+="; unpin failed: $(tail -c 128 "$work/isolation.stderr")"
                fi
            fi
        else
            isolation_cpus=''
            operation_error=$(tail -c 512 "$work/isolation.stderr")
        fi
    fi
}

release_vm_cpu() {
    isolation_status=release_failed isolation_cpus='' operation_error=''
    if release_cpu_affinity 2>"$work/release.stderr"; then
        isolation_status=released
    else operation_error=$(tail -c 512 "$work/release.stderr"); fi
}
