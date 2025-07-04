#!/bin/bash
set -e


CLOUDRC="/opt/cloudland/scripts/cloudrc"
if [ -f "$CLOUDRC" ]; then
    source "$CLOUDRC"
fi

OUTPUT="/var/lib/node_exporter/volume_size_metrics.prom"
> "$OUTPUT"

for vhost_path in /var/run/wds/instance-*; do
    [ -S "$vhost_path" ] || continue

    vhost_name=$(basename "$vhost_path")
    # 只支持 instance-数字- 这种格式
    if [[ "$vhost_name" =~ ^instance-([0-9]+)- ]]; then
        domain_id="inst-${BASH_REMATCH[1]}"
    else
        continue
    fi

    # 1. 通过 vhost 名称获取 vhost_id
    vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')

    if [ "$vhost_id" != "null" ] && [ -n "$vhost_id" ]; then
        # 2. 通过 vhost_id 获取卷信息
        volume_info=$(wds_curl GET "api/v2/sync/block/vhost/$vhost_id/get_vhost_map_volumes" | jq -r '.map_volumes[0]')

        if [ "$volume_info" != "null" ] && [ -n "$volume_info" ]; then
            volume_name=$(echo "$volume_info" | jq -r '.volume_name')
            volume_id=$(echo "$volume_info" | jq -r '.id')
            volume_size=$(echo "$volume_info" | jq -r '.volume_size')

            # 输出为 Prometheus 格式
            echo "wds_volume_size_bytes{domain=\"$domain_id\",volume_id=\"$volume_id\",volume_name=\"$volume_name\"} $volume_size" >> "$OUTPUT"
        fi
    fi
done

chown prometheus:prometheus "$OUTPUT"
