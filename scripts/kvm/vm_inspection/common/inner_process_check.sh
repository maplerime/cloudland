#!/bin/sh
# PIDS and HASH_FILES are supplied by the runner from this scan's TopN list.
# Values are base64 records so command lines cannot corrupt the result protocol.
emit() { printf 'PROCESS|%s|%s|%s|' "$pid" "$1" "$2"; printf '%s' "$3" | base64 | tr -d '\n'; printf '\n'; }
read_field() {
    field=$1; shift
    if value=$("$@" 2>/dev/null); then emit "$field" ok "$value"; else emit "$field" failed read_failed; fi
}
connections=$(ss -H -ntup 2>/dev/null); ss_rc=$?
for pid in $PIDS; do
    case "$pid" in ''|*[!0-9]*) continue;; esac
    if ! before=$(cat "/proc/$pid/stat" 2>/dev/null); then emit _process failed process_exited; continue; fi
    # Strip the parenthesized comm (which may contain spaces).
    fields=${before##*) }; start=$(printf '%s\n' "$fields" | awk '{print $20}')
    flags=$(printf '%s\n' "$fields" | awk '{print $7}')
    kernel=false
    if [ -n "$flags" ] && [ "$((flags & 2097152))" -ne 0 ]; then kernel=true; fi
    if [ "$kernel" = true ]; then
        emit exe ok null; emit cmdline ok ''; emit parent_exe ok null; emit exe_deleted ok false
        emit exe_size ok null; emit exe_sha256 ok null
    else
        if exe=$(readlink "/proc/$pid/exe" 2>/dev/null); then
            deleted=false
            case "$exe" in *' (deleted)') deleted=true; exe=${exe%' (deleted)'};; esac
            emit exe ok "$exe"; emit exe_deleted ok "$deleted"
        else
            emit exe failed read_failed; emit exe_deleted failed read_failed
        fi
        # Preserve argv NUL separators until the host joins them with one space.
        if argv=$(base64 < "/proc/$pid/cmdline" 2>/dev/null); then
            printf 'PROCESS|%s|argv|ok|' "$pid"; printf '%s' "$argv" | tr -d '\n'; printf '\n'
        else emit argv failed read_failed; fi
        parent=$(awk '/^PPid:/{print $2}' "/proc/$pid/status" 2>/dev/null)
        if [ "$parent" = 0 ]; then emit parent_exe ok null; else read_field parent_exe readlink "/proc/$parent/exe"; fi
        read_field exe_size stat -Lc %s "/proc/$pid/exe"
        if [ "$HASH_FILES" = true ]; then
            if hash=$(sha256sum "/proc/$pid/exe" 2>/dev/null); then emit exe_sha256 ok "${hash%% *}"; else emit exe_sha256 failed read_failed; fi
        fi
    fi
    uid=$(awk '/^Uid:/{print $3}' "/proc/$pid/status" 2>/dev/null)
    if [ -n "$uid" ]; then emit uid ok "$uid"; else emit uid failed read_failed; fi
    if [ "$ss_rc" -ne 0 ]; then emit connections failed ss_unavailable; else
        emit connections ok ''
        printf '%s\n' "$connections" | awk -v p="pid=$pid," 'index($0,p){print $1 "|" $6}' | while IFS='|' read -r proto peer; do
            emit connection ok "$proto|$peer"
        done
    fi
    after=$(cat "/proc/$pid/stat" 2>/dev/null); fields=${after##*) }
    ended=$(printf '%s\n' "$fields" | awk '{print $20}')
    if [ -z "$ended" ] || [ "$start" != "$ended" ]; then emit _process failed process_exited_or_reused; fi
done
