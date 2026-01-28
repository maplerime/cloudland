#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

# Parameters: snapshot_ID, cg_ID, wds_cg_id, wds_snap_id, volumes_json
# volumes_json format: [{"vol_id":1,"wds_id":"xxx","instance_id":123},...]
[ $# -lt 5 ] && echo "$0 <snapshot_ID> <cg_ID> <wds_cg_id> <wds_snap_id> <volumes_json>" && exit -1

snapshot_ID=$1
cg_ID=$2
wds_cg_id=$3
wds_snap_id=$4
volumes_json=$5

state='error'
vhost_info_file=/tmp/cg_restore_vhosts_${snapshot_ID}_$$

if [ -z "$wds_address" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_cg_id" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'wds_cg_id is not set'"
    exit -1
fi

if [ -z "$wds_snap_id" ]; then
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'wds_snap_id is not set'"
    exit -1
fi

get_wds_token

# Function to cleanup vhosts for attached volumes
# 清理已挂载卷的 vhost（恢复前执行）
cleanup_vhosts() {
    echo "Cleaning up vhosts for attached volumes"

    # Parse volumes_json and process each volume with instance_id > 0
    echo "$volumes_json" | jq -c '.[]' | while read vol; do
        vol_id=$(echo $vol | jq -r '.vol_id')
        wds_id=$(echo $vol | jq -r '.wds_id')
        instance_id=$(echo $vol | jq -r '.instance_id')

        # Skip volumes not attached to any instance
        if [ "$instance_id" == "0" ] || [ "$instance_id" == "null" ]; then
            continue
        fi

        echo "Processing volume $vol_id (wds: $wds_id, instance: $instance_id)"

        # 1. Construct vhost name pattern: instance-{inst_id}-volume-{vol_id}*
        vhost_name_pattern="instance-${instance_id}-volume-${vol_id}"

        # 2. Find vhost by name pattern via WDS API
        vhost_ret=$(wds_curl GET "api/v2/sync/block/vhost?name=${vhost_name_pattern}")
        vhost_id=$(echo $vhost_ret | jq -r '.vhosts[0].id')
        vhost_name=$(echo $vhost_ret | jq -r '.vhosts[0].name')

        if [ -z "$vhost_id" ] || [ "$vhost_id" == "null" ]; then
            echo "WARN: Could not find vhost for vol $vol_id, inst $instance_id"
            # Save default name for rebuild
            echo "${vol_id} ${vhost_name_pattern} ${instance_id} ${wds_id}" >> $vhost_info_file
            continue
        fi

        # Save vhost info for rebuild later
        echo "${vol_id} ${vhost_name} ${instance_id} ${wds_id}" >> $vhost_info_file

        # 3. Delete vhost (this will also unbind USS automatically)
        echo "Deleting vhost $vhost_id ($vhost_name)"
        delete_ret=$(wds_curl DELETE "api/v2/sync/block/vhost/$vhost_id")
        ret_code=$(echo $delete_ret | jq -r '.ret_code')
        if [ "$ret_code" != "0" ]; then
            echo "WARN: Failed to delete vhost $vhost_id: $(echo $delete_ret | jq -r .message)"
        fi
    done
}

# Function to rebuild vhosts for attached volumes
# 重建已挂载卷的 vhost（恢复后执行）
rebuild_vhosts() {
    echo "Rebuilding vhosts for attached volumes"

    if [ ! -f "$vhost_info_file" ]; then
        echo "No vhost info file, skipping rebuild"
        return 0
    fi

    while read line; do
        vol_id=$(echo $line | awk '{print $1}')
        vhost_name=$(echo $line | awk '{print $2}')
        instance_id=$(echo $line | awk '{print $3}')
        wds_id=$(echo $line | awk '{print $4}')

        echo "Rebuilding vhost $vhost_name for vol $vol_id"

        # 1. Create new vhost
        create_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
        new_vhost_id=$(echo $create_ret | jq -r '.id')
        if [ -z "$new_vhost_id" ] || [ "$new_vhost_id" == "null" ]; then
            echo "ERROR: Failed to create vhost $vhost_name"
            continue
        fi

        # 2. Get USS gateway ID for the hyper (from instance)
        # Note: This requires looking up the instance's hyper and its USS
        # Using a helper function from cloudrc
        if [ -n "$(type -t get_uss_gateway_by_instance)" ]; then
            uss_id=$(get_uss_gateway_by_instance $instance_id)
        else
            # Fallback: try to get USS from instance's hyper
            uss_id=""
        fi

        if [ -z "$uss_id" ] || [ "$uss_id" == "null" ]; then
            echo "WARN: Failed to get USS ID for instance $instance_id, skipping bind"
            continue
        fi

        # 3. Bind vhost to USS
        bind_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" \
            "{\"vhost_id\": \"$new_vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$wds_id\", \"is_snapshot\": false}")
        ret_code=$(echo $bind_ret | jq -r '.ret_code')
        if [ "$ret_code" != "0" ]; then
            echo "ERROR: Failed to bind vhost $new_vhost_id to USS: $(echo $bind_ret | jq -r .message)"
            continue
        fi

        echo "Successfully rebuilt vhost $vhost_name (ID: $new_vhost_id)"
    done < $vhost_info_file

    return 0
}

# Main execution flow
# 主执行流程

# 1. Cleanup vhosts for attached volumes (before restore)
cleanup_vhosts

# 2. Restore consistency group from snapshot via WDS API
echo "Restoring CG $cg_ID from snapshot $wds_snap_id"
restore_ret=$(wds_curl PUT "api/v2/sync/block/consistency_groups/$wds_cg_id/recovery" "{\"cg_snap_id\": \"$wds_snap_id\"}")

ret_code=$(echo $restore_ret | jq -r .ret_code)
message=$(echo $restore_ret | jq -r .message)

if [ "$ret_code" != "0" ]; then
    echo "Failed to restore CG $cg_ID from snapshot $wds_snap_id: $message"
    # Try to rebuild vhosts even on failure
    rebuild_vhosts
    rm -f $vhost_info_file
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' 'error' 'failed to restore CG snapshot: $message'"
    exit -1
fi

echo "CG $cg_ID restored from snapshot $wds_snap_id"

# 3. Rebuild vhosts for attached volumes (after restore)
rebuild_vhosts

# Cleanup temp file
rm -f $vhost_info_file

state='available'

# 4. Return result via COMMAND protocol
echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'success'"
