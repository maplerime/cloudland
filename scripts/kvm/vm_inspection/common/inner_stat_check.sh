#!/bin/sh
export LC_ALL=C
checks=''; cupsd_mtime=''; cron_mtime=''
for item in binary keepalive; do
    case "$item" in binary) path=/usr/sbin/cupsd;; keepalive) path=/etc/cron.d/cupsd-keepalive;; esac
    output=$(stat -c '%Y %y' "$path" 2>&1); rc=$?
    state=ok; code=''; message=''; epoch=null; raw=''
    if [ "$rc" -eq 0 ]; then raw=$output; epoch=${output%% *}
    else
        case "$output" in
            *'No such file or directory'*) ;;
            *) state=failed; code=command_failed; message=$output
               [ "$rc" -ne 127 ] || code=command_missing
               case "$output" in *'Permission denied'*) code=permission_denied;; esac;;
        esac
    fi
    case "$item" in binary) cupsd_mtime=$raw;; keepalive) cron_mtime=$raw;; esac
    checks="$checks$(printf 'CHECK|%s_mtime|%s|%s|' "$item" "$state" "$code"; printf '%s' "$message" | base64 | tr -d '\n'; printf '|'; printf '%s' "$epoch" | base64 | tr -d '\n')\n"
done
printf 'STAT|%s|%s\n' "$cupsd_mtime" "$cron_mtime"
printf '%b' "$checks"
