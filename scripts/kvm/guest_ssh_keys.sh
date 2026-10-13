#!/bin/sh
# Runs INSIDE the guest via qemu-ga guest-exec (fallback for qemu-ga < 5.2).
# Usage: guest_ssh_keys.sh <add|remove|reset> <user>   (public keys on stdin, one per line)
set -e
home=$(getent passwd "$2" | cut -d: -f6)
[ -n "$home" ] || { echo "user $2 not found" >&2; exit 1; }
grp=$(id -gn "$2")
f=$home/.ssh/authorized_keys
mkdir -p "$home/.ssh"
touch "$f"
chmod 700 "$home/.ssh"
chmod 600 "$f"
keys=$(cat)
case "$1" in
add)
    # qemu-ga and hand edits may leave no trailing newline; appending would join two keys
    [ -s "$f" ] && [ -n "$(tail -c 1 "$f")" ] && echo >> "$f"
    printf '%s\n' "$keys" | while IFS= read -r k; do
        [ -z "$k" ] || grep -qxF "$k" "$f" || printf '%s\n' "$k" >> "$f"
    done
    ;;
remove)
    # empty pattern lines would make grep -v match everything, drop them
    t=$(mktemp)
    printf '%s\n' "$keys" | sed '/^$/d' > "$t"
    grep -vxF -f "$t" "$f" > "$t.new" || true
    cat "$t.new" > "$f"
    rm -f "$t" "$t.new"
    ;;
reset)
    printf '%s\n' "$keys" | sed '/^$/d' > "$f"
    ;;
*)
    echo "invalid action $1" >&2
    exit 1
    ;;
esac
chown -R "$2:$grp" "$home/.ssh"
if command -v restorecon >/dev/null 2>&1; then restorecon -R "$home/.ssh" || true; fi
