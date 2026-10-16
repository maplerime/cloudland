#!/bin/bash
# Query WDS uss_gateway CPU core-binding info via the wds_curl helper from cloudrc
# (auth comes from cloudrc.local, sourced automatically by cloudrc - never touched here).
# Filters by server_name, same as get_uss_gateway() - defaults to this host's own
# hostname, so no argument is needed when run locally on the compute node.
# Output: one JSON object per matching gateway; core (hex CPU mask) is expanded
# into a comma-separated core_cpus list.
#
# Usage: check_uss_core_bind.sh [server_hostname]

source /opt/cloudland/scripts/cloudrc

uss_hname=$1
[ -z "$uss_hname" ] && uss_hname=$(hostname -s)

wds_curl GET "api/v2/wds/uss" \
    | jq -c --arg hname "$uss_hname" '.uss_gateways[] | select(.server_name == $hname) | {name, server_name, server_id, core, status}' \
    | while IFS= read -r gw; do
        mask=$(( $(jq -r '.core' <<<"$gw") ))
        cpus=()
        for ((i = 0; i < 64; i++)); do
            (( (mask >> i) & 1 )) && cpus+=("$i")
        done
        IFS=,
        jq --arg cpus "${cpus[*]}" '. + {core_cpus: $cpus}' <<<"$gw"
        unset IFS
    done
