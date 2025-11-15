#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 1 ] && die "$0 <vm_ID>"

vm_ID=$1
domain_name="inst-$vm_ID"

# Metrics file configuration
METRICS_DIR="/var/lib/node_exporter"
CPU_METRICS_FILE="$METRICS_DIR/vm_cpu_adjustment_status.prom"
BANDWIDTH_METRICS_FILE="$METRICS_DIR/vm_bandwidth_adjustment_status.prom"
BANDWIDTH_CONFIG_FILE="$METRICS_DIR/vm_interface_bandwidth_config.prom"

# Function: Clean up specific domain records from metrics file
cleanup_metrics_file() {
    local metrics_file=$1
    local domain=$2
    local metrics_type=$3
    
    if [[ ! -f "$metrics_file" ]]; then
        echo "No $metrics_type metrics file found: $metrics_file"
        return 0
    fi
    
    echo "Cleaning $metrics_type metrics for domain: $domain"
    
    # Check if metrics exist for this domain
    if ! grep -q "domain=\"$domain\"" "$metrics_file"; then
        echo "No $metrics_type metrics found for domain: $domain"
        return 0
    fi
    
    # Create temporary file, remove all metrics lines for this domain
    local temp_file="$metrics_file.tmp"
    grep -v "domain=\"$domain\"" "$metrics_file" > "$temp_file"
    
    # Check if any metrics lines remain after cleanup (excluding comments)
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

# Clean up CPU adjustment metrics
cleanup_metrics_file "$CPU_METRICS_FILE" "$domain_name" "CPU adjustment"

# Clean up bandwidth adjustment metrics
cleanup_metrics_file "$BANDWIDTH_METRICS_FILE" "$domain_name" "bandwidth adjustment"

# Clean up bandwidth configuration metrics
/opt/cloudland/scripts/kvm/update_vm_interface_bandwidth.sh delete_domain "$domain_name" 2>&1 | logger -t cleanup_vm_custom_metrics || true

echo "Custom metrics cleanup completed for domain: $domain_name"
