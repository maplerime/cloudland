#!/bin/sh
# POSIX functions sent into the guest; no host configuration or automatic execution.
export LC_ALL=C
inspection_query() {
    output=$("$@" 2>&1); rc=$?; code=''
    if [ "$rc" -ne 0 ]; then
        code=command_failed
        [ "$rc" -ne 127 ] || code=command_missing
        case "$output" in *'Permission denied'*) code=permission_denied;; esac
    fi
}
record_inspection_check() {
    state=ok; message=''
    if [ "$rc" -ne 0 ]; then state=failed; message=$output; fi
    checks="$checks$(printf 'CHECK|%s|%s|%s|' "$1" "$state" "$code"; printf '%s' "$message" | base64 | tr -d '\n'; printf '|'; printf '%s' "$2" | base64 | tr -d '\n')\n"
}

inspect_cups() (
checks=''
pkg=no; package_status=unknown; manager=''
if command -v dpkg >/dev/null 2>&1; then manager=dpkg; inspection_query dpkg -s cups-daemon
elif command -v rpm >/dev/null 2>&1; then manager=rpm; inspection_query rpm -q cups
else rc=127; code=command_missing; output='dpkg and rpm are unavailable'; fi
if [ "$rc" -eq 0 ]; then
    pkg=yes; package_status=installed
    if [ "$manager" = dpkg ]; then
        package_state=$(printf '%s\n' "$output" | awk '$1=="Status:" {print $NF; exit}')
        case "$package_state" in installed) ;; '') pkg=no; package_status=unknown; rc=1; code=invalid_output;; *) pkg=no; package_status=not_installed;; esac
    fi
else
    case "$output" in *'not installed'*) rc=0; code=''; package_status=not_installed;; esac
fi
record_inspection_check package_status "$package_status"

inspection_query stat -c%s /usr/sbin/cupsd
size=0; binary_exists=null; binary_size=null
if [ "$rc" -eq 0 ]; then size=$output; binary_size=$output; binary_exists=true
else
    case "$output" in *'No such file or directory'*) rc=0; code=''; binary_exists=false;; esac
fi
record_inspection_check binary_exists "$binary_exists"
record_inspection_check binary_size_bytes "$binary_size"

cron=no; hidden=no
for item in keepalive hidden_dir; do
    case "$item" in keepalive) path=/etc/cron.d/cupsd-keepalive;; hidden_dir) path=/var/local/.cup;; esac
    inspection_query stat -L -c %F "$path"
    exists=null
    if [ "$rc" -eq 0 ]; then
        exists=false
        case "$item:$output" in keepalive:*regular*file*) cron=yes; exists=true;; hidden_dir:directory) hidden=yes; exists=true;; esac
    else
        case "$output" in *'No such file or directory'*) rc=0; code=''; exists=false;; esac
    fi
    record_inspection_check "${item}_exists" "$exists"
done

inspection_query ss -tnp
conn=''
if [ "$rc" -eq 0 ]; then conn=$(printf '%s\n' "$output" | awk '/cupsd/ {print $5; exit}'); fi
record_inspection_check connection_peer "$conn"
printf 'RESULT:%s:%s:%s:%s:%s\n' "$pkg" "$size" "$cron" "$hidden" "$conn"
printf '%b' "$checks"
)

inspect_timestamps() (
checks=''; cupsd_mtime=''; cron_mtime=''
for item in binary keepalive; do
    case "$item" in binary) path=/usr/sbin/cupsd;; keepalive) path=/etc/cron.d/cupsd-keepalive;; esac
    inspection_query stat -c '%Y %y' "$path"
    epoch=null; raw=''
    if [ "$rc" -eq 0 ]; then raw=$output; epoch=${output%% *}
    else
        case "$output" in *'No such file or directory'*) rc=0; code='';; esac
    fi
    case "$item" in binary) cupsd_mtime=$raw;; keepalive) cron_mtime=$raw;; esac
    record_inspection_check "${item}_mtime" "$epoch"
done
printf 'STAT|%s|%s\n' "$cupsd_mtime" "$cron_mtime"
printf '%b' "$checks"
)

inspect_process_details() (
PIDS=$1 HASH_FILES=${2:-true}
emit() { printf 'PROCESS|%s|%s|%s|' "$pid" "$1" "$2"; printf '%s' "$3" | base64 | tr -d '\n'; printf '\n'; }
read_field() {
    field=$1; shift
    if value=$("$@" 2>/dev/null); then emit "$field" ok "$value"; else emit "$field" failed read_failed; fi
}
process_read_error() {
    if [ -d "/proc/$pid" ]; then emit _process failed process_read_failed
    else emit _process failed process_exited; fi
}
connections=$(ss -H -ntup 2>/dev/null); ss_rc=$?
for pid in $PIDS; do
    case "$pid" in ''|*[!0-9]*) continue;; esac
    if ! before=$(cat "/proc/$pid/stat" 2>/dev/null); then process_read_error; continue; fi
    # Strip the parenthesized comm (which may contain spaces).
    fields=${before##*) }; start=$(printf '%s\n' "$fields" | awk '{print $20}')
    if [ -z "$start" ]; then emit _process failed process_read_failed; continue; fi
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
    if ! after=$(cat "/proc/$pid/stat" 2>/dev/null); then process_read_error; continue; fi
    fields=${after##*) }
    ended=$(printf '%s\n' "$fields" | awk '{print $20}')
    if [ -z "$ended" ]; then emit _process failed process_read_failed
    elif [ "$start" != "$ended" ]; then emit _process failed process_reused; fi
done
)

