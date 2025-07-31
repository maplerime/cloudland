#!/bin/bash

source /opt/cloudland/scripts/cloudrc

domain=$1
cpu_count=$2

if [ -z "$domain" ] || [ -z "$cpu_count" ]; then
    echo "用法: $0 <domain_name> <cpu_count>"
    exit 1
fi

# 获取当前配置的CPU数
current_cpu=$(virsh vcpucount "$domain" --current)
current_count=$?

if [ $current_count -ne 0 ]; then
    echo "获取虚拟机 $domain 当前CPU配置失败"
    exit 1
fi

# 检查CPU热插拔支持
max_cpu=$(virsh vcpucount "$domain" --maximum)
max_count=$?

if [ $max_count -ne 0 ]; then
    echo "获取虚拟机 $domain 最大CPU配置失败"
    exit 1
fi

if [ "$cpu_count" -gt "$max_cpu" ]; then
    echo "请求的CPU数量($cpu_count)超过最大配置($max_cpu)"
    exit 1
fi

# 使用virsh setvcpus命令动态调整CPU数量
virsh setvcpus "$domain" "$cpu_count" --live --config

if [ $? -ne 0 ]; then
    echo "调整CPU数量失败"
    exit 1
fi

echo "成功将$domain的CPU数量从$current_cpu调整为$cpu_count"
exit 0 