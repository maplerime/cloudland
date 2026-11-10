#!/bin/bash

cd "$(dirname "$0")"
source ../cloudrc

file=$run_dir/check_connection.ip_list
[ -s "$file" ] || exit 0

# Common public DNS, site DNS, and any additional trusted destination IPs.
ignore_ips="8.8.8.8 8.8.4.4 1.1.1.1 1.0.0.1 9.9.9.9 149.112.112.112
223.5.5.5 223.6.6.6 119.29.29.29 114.114.114.114 114.114.115.115
2001:4860:4860::8888 2001:4860:4860::8844 2606:4700:4700::1111 2606:4700:4700::1001
$dns_server $dns6_server $observe_connection_ignore_ips"
# Override in cloudrc.local; an empty value disables service exclusions.
ignore_services=${observe_connection_ignore_services-"tcp:53 udp:53 tcp:853 udp:853 udp:123"}

connections=$(conntrack -L 2>/dev/null) || exit 1
ips=$(echo "$connections" | awk -v ignore_ips="$ignore_ips" -v ignore_services="$ignore_services" '
    BEGIN {
        n = split(ignore_ips, values, /[[:space:],]+/)
        for (i = 1; i <= n; i++) if (values[i] != "") trusted[values[i]] = 1
        n = split(ignore_services, values, /[[:space:],]+/)
        for (i = 1; i <= n; i++) if (values[i] != "") services[values[i]] = 1
    }
    NR == FNR { if ($1 != "") watched[$1] = 1; next }
    {
        src = ""; dst = ""; dport = ""
        for (i = 1; i <= NF; i++) {
            if (src == "" && $i ~ /^src=/) src = substr($i, 5)
            if (dst == "" && $i ~ /^dst=/) dst = substr($i, 5)
            if (dport == "" && $i ~ /^dport=/) dport = substr($i, 7)
        }
        if (src in watched && dst != "" && !(dst in trusted) && !(($1 ":" dport) in services)) print dst
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
