#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID>"

vm_ID=$1
domain_name="inst-$vm_ID"

# 指标文件配置
METRICS_DIR="/var/lib/node_exporter"
CPU_METRICS_FILE="$METRICS_DIR/vm_cpu_adjustment_status.prom"
BANDWIDTH_METRICS_FILE="$METRICS_DIR/vm_bandwidth_adjustment_status.prom"

# 函数：清理指标文件中的特定domain记录
cleanup_metrics_file() {
    local metrics_file=$1
    local domain=$2
    local metrics_type=$3
    
    if [[ ! -f "$metrics_file" ]]; then
        echo "No $metrics_type metrics file found: $metrics_file"
        return 0
    fi
    
    echo "Cleaning $metrics_type metrics for domain: $domain"
    
    # 检查是否存在该domain的指标
    if ! grep -q "domain=\"$domain\"" "$metrics_file"; then
        echo "No $metrics_type metrics found for domain: $domain"
        return 0
    fi
    
    # 创建临时文件，移除该domain的所有指标行
    local temp_file="$metrics_file.tmp"
    grep -v "domain=\"$domain\"" "$metrics_file" > "$temp_file"
    
    # 检查清理后是否还有指标行（除了注释）
    if ! grep -q "^vm_" "$temp_file"; then
        echo "All $metrics_type metrics cleared - removing metrics file"
        rm -f "$temp_file" "$metrics_file"
        echo "Removed empty $metrics_type metrics file"
    else
        mv "$temp_file" "$metrics_file"
        echo "Cleaned $metrics_type metrics for domain: $domain (file preserved with other metrics)"
    fi
    
    return 0
}

echo "Starting cleanup of custom metrics for VM ID: $vm_ID (domain: $domain_name)"

# 清理CPU调整指标
cleanup_metrics_file "$CPU_METRICS_FILE" "$domain_name" "CPU adjustment"

# 清理带宽调整指标  
cleanup_metrics_file "$BANDWIDTH_METRICS_FILE" "$domain_name" "bandwidth adjustment"

echo "Custom metrics cleanup completed for domain: $domain_name" 