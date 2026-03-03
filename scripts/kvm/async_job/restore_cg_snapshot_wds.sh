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

log_debug $cg_ID "restore_cg_snapshot_wds: Starting, snapshot_ID=$snapshot_ID, cg_ID=$cg_ID, wds_cg_id=$wds_cg_id, wds_snap_id=$wds_snap_id"

if [ -z "$wds_address" ]; then
    log_debug $cg_ID "restore_cg_snapshot_wds: Error - wds_address is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'wds_address is not set'"
    exit -1
fi

if [ -z "$wds_cg_id" ]; then
    log_debug $cg_ID "restore_cg_snapshot_wds: Error - wds_cg_id is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'wds_cg_id is not set'"
    exit -1
fi

if [ -z "$wds_snap_id" ]; then
    log_debug $cg_ID "restore_cg_snapshot_wds: Error - wds_snap_id is not set"
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'wds_snap_id is not set'"
    exit -1
fi

get_wds_token

# Function to cleanup vhosts for attached volumes
# 清理已挂载卷的 vhost（恢复前执行）
cleanup_vhosts() {
    log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - Starting cleanup for attached volumes"

    # Parse volumes_json and process each volume with instance_id > 0
    echo "$volumes_json" | jq -c '.[]' | while read vol; do
        vol_id=$(echo $vol | jq -r '.vol_id')
        wds_id=$(echo $vol | jq -r '.wds_id')
        instance_id=$(echo $vol | jq -r '.instance_id')

        # Skip volumes not attached to any instance
        if [ "$instance_id" == "0" ] || [ "$instance_id" == "null" ]; then
            continue
        fi

        log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - Processing volume vol_id=$vol_id, wds_id=$wds_id, instance_id=$instance_id"

        # 1. Get vhost ID via WDS API using volume's wds_id
        # 通过卷的 wds_id 获取 vhost ID
        vhost_ret=$(wds_curl GET "api/v2/block/volumes/$wds_id/vhost")
        log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - vhost API response: $vhost_ret"
        vhost_count=$(echo $vhost_ret | jq -r '.count // 0')
        vhost_id=$(echo $vhost_ret | jq -r '.vhosts[0].id // empty')
        vhost_name=$(echo $vhost_ret | jq -r '.vhosts[0].name // empty')

        if [ -z "$vhost_id" ] || [ "$vhost_count" == "0" ]; then
            log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - WARN: Could not find vhost for vol_id=$vol_id, wds_id=$wds_id"
            # Save info for rebuild with default vhost name
            default_vhost_name="instance-${instance_id}-vol-${vol_id}"
            echo "${vol_id} ${default_vhost_name} ${instance_id} ${wds_id} " >> $vhost_info_file
            continue
        fi

        # 2. Get bound USS ID via WDS API
        # 获取绑定的 USS ID
        uss_ret=$(wds_curl GET "api/v2/sync/block/vhost/$vhost_id/vhost_binded_uss")
        log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - uss API response: $uss_ret"
        uss_id=$(echo $uss_ret | jq -r '.uss[0].id // empty')

        # Save vhost info for rebuild later (including uss_id)
        # 保存 vhost 信息用于后续重建（包括 uss_id）
        echo "${vol_id} ${vhost_name} ${instance_id} ${wds_id} ${uss_id}" >> $vhost_info_file
        log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - Saved vhost info: vol_id=$vol_id, vhost_name=$vhost_name, uss_id=$uss_id"

        # 3. Delete vhost using cloudrc function (handles unbind automatically)
        # 使用 cloudrc 函数删除 vhost（自动处理解绑）
        log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - Deleting vhost vhost_id=$vhost_id, vhost_name=$vhost_name, uss_id=$uss_id"
        if [ -n "$uss_id" ]; then
            delete_vhost $vol_id $vhost_id $uss_id
        else
            delete_vhost $vol_id $vhost_id
        fi
        log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - Successfully deleted vhost vhost_id=$vhost_id"
    done
    log_debug $cg_ID "restore_cg_snapshot_wds: cleanup_vhosts - Completed"
}

# Function to rebuild vhosts for attached volumes
# 重建已挂载卷的 vhost（恢复后执行）
rebuild_vhosts() {
    log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Starting rebuild for attached volumes"

    if [ ! -f "$vhost_info_file" ]; then
        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - No vhost info file, skipping rebuild"
        return 0
    fi

    while read line; do
        vol_id=$(echo $line | awk '{print $1}')
        vhost_name=$(echo $line | awk '{print $2}')
        instance_id=$(echo $line | awk '{print $3}')
        wds_id=$(echo $line | awk '{print $4}')
        uss_id=$(echo $line | awk '{print $5}')

        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Rebuilding vhost vhost_name=$vhost_name for vol_id=$vol_id, uss_id=$uss_id"

        # 1. Create new vhost
        # 创建新的 vhost
        create_ret=$(wds_curl POST "api/v2/sync/block/vhost" "{\"name\": \"$vhost_name\"}")
        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Create vhost response: $create_ret"
        new_vhost_id=$(echo $create_ret | jq -r '.id // empty')
        if [ -z "$new_vhost_id" ]; then
            log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - ERROR: Failed to create vhost vhost_name=$vhost_name"
            continue
        fi
        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Created new vhost new_vhost_id=$new_vhost_id"

        # 2. Check if we have the saved USS ID
        # 检查是否有保存的 USS ID
        if [ -z "$uss_id" ]; then
            log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - WARN: No saved USS ID for vol_id=$vol_id, skipping bind"
            continue
        fi

        # 3. Bind vhost to USS using saved uss_id
        # 使用保存的 uss_id 绑定 vhost 到 USS
        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Binding vhost new_vhost_id=$new_vhost_id to uss_id=$uss_id, lun_id=$wds_id"
        bind_ret=$(wds_curl PUT "api/v2/sync/block/vhost/bind_uss" \
            "{\"vhost_id\": \"$new_vhost_id\", \"uss_gw_id\": \"$uss_id\", \"lun_id\": \"$wds_id\", \"is_snapshot\": false}")
        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Bind response: $bind_ret"
        ret_code=$(echo $bind_ret | jq -r '.ret_code // empty')
        if [ -z "$ret_code" ] || [ "$ret_code" != "0" ]; then
            log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - ERROR: Failed to bind vhost, ret_code=$ret_code, message=$(echo $bind_ret | jq -r '.message // empty')"
            continue
        fi

        log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Successfully rebuilt vhost vhost_name=$vhost_name, new_vhost_id=$new_vhost_id"
    done < $vhost_info_file

    log_debug $cg_ID "restore_cg_snapshot_wds: rebuild_vhosts - Completed"
    return 0
}

# Main execution flow
# 主执行流程

# 1. Cleanup vhosts for attached volumes (before restore)
cleanup_vhosts

# 2. Restore consistency group from snapshot via WDS API
log_debug $cg_ID "restore_cg_snapshot_wds: Restoring CG from snapshot, wds_cg_id=$wds_cg_id, wds_snap_id=$wds_snap_id"
restore_ret=$(wds_curl PUT "api/v2/sync/block/consistency_groups/$wds_cg_id/recovery" "{\"cg_snap_id\": \"$wds_snap_id\"}")
log_debug $cg_ID "restore_cg_snapshot_wds: WDS API response: $restore_ret"

ret_code=$(echo $restore_ret | jq -r '.ret_code // empty')
message=$(echo $restore_ret | jq -r '.message // empty')
log_debug $cg_ID "restore_cg_snapshot_wds: Parsed ret_code=$ret_code, message=$message"

if [ -z "$ret_code" ] || [ "$ret_code" != "0" ]; then
    log_debug $cg_ID "restore_cg_snapshot_wds: Failed to restore CG, ret_code=$ret_code, message=$message"
    # Try to rebuild vhosts even on failure
    rebuild_vhosts
    rm -f $vhost_info_file
    echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' 'error' 'failed to restore CG snapshot: $message (ret_code: $ret_code)'"
    exit -1
fi

log_debug $cg_ID "restore_cg_snapshot_wds: CG restored successfully from snapshot"

# 3. Rebuild vhosts for attached volumes (after restore)
rebuild_vhosts

# Cleanup temp file
rm -f $vhost_info_file

state='available'

log_debug $cg_ID "restore_cg_snapshot_wds: Completed successfully, state=$state"
# 4. Return result via COMMAND protocol
echo "|:-COMMAND-:| $(basename $0) '$snapshot_ID' '$cg_ID' '$state' 'success'"
