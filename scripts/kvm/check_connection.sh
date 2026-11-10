#!/bin/bash

cd "$(dirname "$0")"
source ../cloudrc

if [ $# -lt 1 ] || [ $# -gt 2 ] || { [ $# -eq 2 ] && [ "$2" != delete ]; }; then
    echo "$0 <ip> [delete]"
    exit 1
fi

ip=$1
file=$run_dir/check_connection.ip_list

if [ "${2:-}" = delete ]; then
    [ -f "$file" ] || exit 0
    (
        flock -x 9 || exit 1
        remaining=$(grep -Fxv -- "$ip" "$file")
        [ $? -gt 1 ] && exit 1
        if [ -n "$remaining" ]; then
            printf '%s\n' "$remaining" > "$file"
        else
            : > "$file"
        fi
    ) 9<>"$file"
    exit $?
fi

sets=$(ipset list -name) || exit 1
for set in $sets; do
    [[ $set == sgas-* ]] || continue
    entries=$(ipset save "$set") || exit 1
    # Only add records contain members; ignore set definitions and metadata.
    if echo "$entries" | awk -v set="$set" '$1 == "add" && $2 == set {split($3, entry, ","); print entry[1]}' | grep -Fxq -- "$ip"; then
        mkdir -p "$run_dir" || exit 1
        (
            flock -x 9 || exit 1
            grep -Fxq -- "$ip" "$file" || echo "$ip" >&9
        ) 9>>"$file"
        exit $?
    fi
done

exit 0
