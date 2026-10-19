#!/bin/sh
export LC_ALL=C
checks=''
query() {
    output=$("$@" 2>&1); rc=$?; code=''
    if [ "$rc" -ne 0 ]; then
        code=command_failed
        [ "$rc" -ne 127 ] || code=command_missing
        case "$output" in *'Permission denied'*) code=permission_denied;; esac
    fi
}
report() {
    state=ok; message=''
    if [ "$rc" -ne 0 ]; then state=failed; message=$output; fi
    checks="$checks$(printf 'CHECK|%s|%s|%s|' "$1" "$state" "$code"; printf '%s' "$message" | base64 | tr -d '\n'; printf '|'; printf '%s' "$2" | base64 | tr -d '\n')\n"
}

pkg=no; package_status=unknown; manager=''
if command -v dpkg >/dev/null 2>&1; then manager=dpkg; query dpkg -s cups-daemon
elif command -v rpm >/dev/null 2>&1; then manager=rpm; query rpm -q cups
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
report package_status "$package_status"

query stat -c%s /usr/sbin/cupsd
size=0; binary_exists=null; binary_size=null
if [ "$rc" -eq 0 ]; then size=$output; binary_size=$output; binary_exists=true
else
    case "$output" in *'No such file or directory'*) rc=0; code=''; binary_exists=false;; esac
fi
report binary_exists "$binary_exists"
report binary_size_bytes "$binary_size"

cron=no; hidden=no
for item in keepalive hidden_dir; do
    case "$item" in keepalive) path=/etc/cron.d/cupsd-keepalive;; hidden_dir) path=/var/local/.cup;; esac
    query stat -L -c %F "$path"
    exists=null
    if [ "$rc" -eq 0 ]; then
        exists=false
        case "$item:$output" in keepalive:*regular*file*) cron=yes; exists=true;; hidden_dir:directory) hidden=yes; exists=true;; esac
    else
        case "$output" in *'No such file or directory'*) rc=0; code=''; exists=false;; esac
    fi
    report "${item}_exists" "$exists"
done

query ss -tnp
conn=''
if [ "$rc" -eq 0 ]; then conn=$(printf '%s\n' "$output" | awk '/cupsd/ {print $5; exit}'); fi
report connection_peer "$conn"
printf 'RESULT:%s:%s:%s:%s:%s\n' "$pkg" "$size" "$cron" "$hidden" "$conn"
printf '%b' "$checks"
