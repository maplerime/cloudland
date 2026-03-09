#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID> <hyper>"

ID=$1
vm_ID=inst-$ID
hyper=$2
volumes=$(cat)
nvolume=$(jq length <<< $volumes)
uss_id=$(get_uss_gateway $hyper)
business_network=$(wds_curl GET "api/v2/wds/uss/$uss_id" | jq -r .business_network)
business_network=${business_network%/*}
i=0
while [ $i -lt $nvolume ]; do
    read volume_id < <(jq -r ".[$i].uuid" <<<$volumes)
    vhost_paths=$(wds_curl GET "api/v2/sync/block/volumes/$volume_id/bind_status" | jq -r .path)
    npaths=$(jq length <<< $vhost_paths)
    j=0
    while [ $j -lt $npaths ]; do
	vhost_path=$(jq -r .[$j] <<<$vhost_paths)
	if [ "${vhost_path/$business_network:/}" != "$vhost_path" ]; then
            log_debug "putting $vhost_path into blacklist"
            ret_code=$(wds_curl PUT "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}" | jq -r .ret_code)
            if [ "$ret_code" != "0" ]; then
                log_debug "failed to put $vhost_path into blacklist"
                exit 1
            fi
        else
            wds_curl DELETE "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}"
	fi
        let j=$j+1
    done
    let i=$i+1
done
exit 0
