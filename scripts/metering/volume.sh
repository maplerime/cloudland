#!/bin/bash

# 加载环境变量和函数
CLOUDRC="/opt/cloudland/scripts/cloudrc"
if [ -f "$CLOUDRC" ]; then
    source "$CLOUDRC"
fi

# 遍历 /var/run/wds/ 下所有以 instance- 开头的 socket 文件
for vhost_path in /var/run/wds/instance-*; do
    [ -S "$vhost_path" ] || continue  # 只处理 socket 文件

    vhost_name=$(basename "$vhost_path")
    echo "Processing vhost: $vhost_name"

    # 1. 通过 vhost 名称获取 vhost_id
    vhost_id=$(wds_curl GET "api/v2/sync/block/vhost?name=$vhost_name" | jq -r '.vhosts[0].id')

    if [ "$vhost_id" != "null" ] && [ -n "$vhost_id" ]; then
        echo "  vhost_id: $vhost_id"

        # 2. 通过 vhost_id 获取卷信息
        volume_info=$(wds_curl GET "api/v2/sync/block/vhost/$vhost_id/get_vhost_map_volumes" | jq -r '.map_volumes[0]')

        if [ "$volume_info" != "null" ] && [ -n "$volume_info" ]; then
            volume_name=$(echo "$volume_info" | jq -r '.volume_name')
            volume_id=$(echo "$volume_info" | jq -r '.id')
            volume_size=$(echo "$volume_info" | jq -r '.volume_size')

            echo "    volume_name: $volume_name"
            echo "    volume_id: $volume_id"
            echo "    volume_size: $volume_size"
        else
            echo "    No volume mapped to this vhost"
        fi
    else
        echo "  Failed to get vhost_id for $vhost_name"
    fi
    echo "---"
done
