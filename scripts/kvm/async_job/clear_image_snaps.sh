#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 2 ] && die "$0 <ID> <image_prefix>"

ID=$1
image_prefix=$2

log_debug $ID "Starting async image snapshot cleanup for prefix: $image_prefix"

# Step 1: Query all snapshots matching the image prefix
response=$(wds_curl GET "api/v2/block/snaps?name=$image_prefix")
snap_count=$(echo "$response" | jq -r '.count // 0')
snaps_json=$(echo "$response" | jq -c '.snaps // []')

if [ "$snap_count" -eq 0 ]; then
    log_debug $ID "No snapshots found for image: $image_prefix, exiting"
    exit 0
fi

snap_names=$(echo "$snaps_json" | jq -r '[.[].name] | join(", ")')
log_debug $ID "Found $snap_count snapshots for prefix $image_prefix: $snap_names"

# Step 2: Parse snapshots — extract group_prefix and tail_number
# group_prefix = everything before the last '-' in the name
# tail_number  = the last segment after the last '-'
parsed=$(echo "$snaps_json" | jq -c '[.[] | {
    name, id, clone_vol_num,
    group_prefix: (.name | split("-") | .[0:-1] | join("-")),
    tail_number: (.name | split("-") | .[-1] | tonumber)
}]')

# Step 3: Classify snapshots via jq group_by + sort_by, then process in one loop
# Per group, the snapshot with the max tail_number is "skip" (newest), others with clone_vol_num==0 are "delete"
echo "$parsed" | jq -r '
    sort_by(.group_prefix)
    | group_by(.group_prefix)[]
    | sort_by(.tail_number)
    | (last.tail_number) as $max
    | .[]
    | (if .tail_number == $max or .clone_vol_num != 0 then "skip" else "delete" end)
      + " \(.id) \(.name) \(.clone_vol_num)"
' | while IFS=' ' read -r action snap_id snap_name clone_vol_num; do
    [ -z "$action" ] && continue
    if [ "$action" = "delete" ]; then
        log_debug $ID "Deleting snapshot: $snap_name (id: $snap_id)"
        del_result=$(wds_curl DELETE "api/v2/sync/block/snaps/$snap_id?force=false")
        ret_code=$(echo "$del_result" | jq -r '.ret_code // "unknown"')
        if [ "$ret_code" = "0" ]; then
            log_debug $ID "Successfully deleted snapshot: $snap_name"
        else
            log_debug $ID "Failed to delete snapshot: $snap_name, response: $del_result"
        fi
    else
        log_debug $ID "Skipped: $snap_name clone_vol_num=$clone_vol_num (newest or in use)"
    fi
done

log_debug $ID "Async image snapshot cleanup completed for prefix: $image_prefix"
