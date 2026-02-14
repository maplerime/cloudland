#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID>"

vm_ID=$1
volumes=$(cat $run_dir/$vm_ID/volumes.json)
nvolume=$(jq length <<< $volumes)
uss_id=$(get_uss_gateway)
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
	if [ "${vhost_path/$business_network:/}" == "$vhost_path" ]; then
            wds_curl PUT "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}" | jq -r .ret_code
        else
            wds_curl DELETE "api/v2/failure_domain/black_list" "{\"path\": \"$vhost_path\"}"
	fi
        let j=$j+1
    done
done
