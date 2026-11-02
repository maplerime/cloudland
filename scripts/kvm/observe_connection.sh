#!/bin/bash

cd "$(dirname "$0")"
source ../cloudrc

file=$run_dir/check_connection.ip_list
[ -s "$file" ] || exit 0

connections=$(conntrack -L 2>/dev/null) || exit 1
ips=$(echo "$connections" | awk '
    NR == FNR { if ($1 != "") watched[$1] = 1; next }
    {
        src = ""; dst = ""
        for (i = 1; i <= NF; i++) {
            if (src == "" && $i ~ /^src=/) src = substr($i, 5)
            if (dst == "" && $i ~ /^dst=/) dst = substr($i, 5)
        }
        if (src in watched && dst != "") print dst
    }
' "$file" - | sort -u)

# Use the existing quoted RPC argument format; batch large destination lists.
count=0
batch=""
for ip in $ips; do
    batch="$batch '$ip'"
    count=$((count + 1))
    if [ "$count" -eq 100 ]; then
        echo "|:-COMMAND-:| $(basename "$0") '$SCI_CLIENT_ID'$batch"
        count=0
        batch=""
    fi
done
[ -n "$batch" ] && echo "|:-COMMAND-:| $(basename "$0") '$SCI_CLIENT_ID'$batch"
exit 0
