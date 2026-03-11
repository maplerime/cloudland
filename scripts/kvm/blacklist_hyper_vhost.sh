#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID> <hyper>"

ID=$1
vm_ID=inst-$ID
hyper=$2
volumes=$(cat)
nvolume=$(jq length <<< $volumes)
log_debug "Processing $nvolume volumes for VM $vm_ID on hyper $hyper"
uss_id=$(get_uss_gateway $hyper)
uss_int_id=/$(wds_curl GET "api/v2/wds/uss/$uss_id" | jq -r .uss_id)/
log_debug "USS internal ID: $uss_int_id"
i=0
while [ $i -lt $nvolume ]; do
    read volume_id < <(jq -r ".[$i].uuid" <<<$volumes)
    log_debug "Processing volume: $volume_id"
    vhost_paths=$(wds_curl GET "api/v2/sync/block/volumes/$volume_id/bind_status" | jq -r .path)
    npaths=$(jq length <<< $vhost_paths)
    log_debug "Found $npaths vhost paths for volume $volume_id"
    j=0
    while [ $j -lt $npaths ]; do
	vhost_path=$(jq -r .[$j] <<<$vhost_paths)
	if [ "${vhost_path/$uss_int_id/}" != "$vhost_path" ]; then
            log_debug "putting $vhost_path into blacklist"
            ret_code=$(wds_curl PUT "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}" | jq -r .ret_code)
            if [ "$ret_code" != "0" ]; then
                log_debug "failed to put $vhost_path into blacklist"
                exit 1
            fi
        else
            log_debug "removing $vhost_path from blacklist"
            wds_curl DELETE "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}"
	fi
        let j=$j+1
    done
    let i=$i+1
done
log_debug "Successfully processed blacklist operations for VM $vm_ID"
exit 0
