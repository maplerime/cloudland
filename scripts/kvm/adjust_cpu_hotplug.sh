#!/bin/bash

source /opt/cloudland/scripts/cloudrc

domain=$1
cpu_count=$2

if [ -z "$domain" ] || [ -z "$cpu_count" ]; then
    echo "Usage: $0 <domain_name> <cpu_count>"
    exit 1
fi

# 获取当前配置的CPU数
current_cpu=$(virsh vcpucount "$domain" --current)
current_count=$?

if [ $current_count -ne 0 ]; then
    echo "Failed to get current CPU configuration for VM $domain"
    exit 1
fi

# 检查CPU热插拔支持
max_cpu=$(virsh vcpucount "$domain" --maximum)
max_count=$?

if [ $max_count -ne 0 ]; then
    echo "Failed to get maximum CPU configuration for VM $domain"
    exit 1
fi

if [ "$cpu_count" -gt "$max_cpu" ]; then
    echo "Requested CPU count ($cpu_count) exceeds maximum configuration ($max_cpu)"
    exit 1
fi

# 使用virsh setvcpus命令动态调整CPU数量
virsh setvcpus "$domain" "$cpu_count" --live --config

if [ $? -ne 0 ]; then
    echo "Failed to adjust CPU count"
    exit 1
fi

echo "Successfully adjusted $domain CPU count from $current_cpu to $cpu_count"
exit 0 