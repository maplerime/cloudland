#!/bin/bash

cd "$(dirname "$0")"
source ../cloudrc

[ $# -ne 1 ] && echo "$0 <ip>" && exit 1

ip=$1
file=$run_dir/check_connection.ip_list

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
