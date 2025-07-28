#!/bin/bash

# Cleanup Rule Metrics Script
# Purpose: Clean up adjustment status metrics for a specific rule ID across all compute nodes
# Author: CloudLand Resource Management System
# Version: 1.0

set -e

# Configuration
METRICS_DIR="/var/lib/node_exporter"
CPU_METRICS_FILE="$METRICS_DIR/vm_cpu_adjustment_status.prom"
BANDWIDTH_METRICS_FILE="$METRICS_DIR/vm_bandwidth_adjustment_status.prom"

# Default values
RULE_ID=""
RULE_TYPE=""

# Usage help
usage() {
    cat << EOF
Usage: $0 --rule-id <rule_id> --type <cpu|bandwidth>

Parameters:
  --rule-id       Rule ID to clean up (required)
                  e.g., fa7f3a53-8919-46ad-a404-e9d47c93a580
  --type          Rule type: cpu or bandwidth (required)

Examples:
  $0 --rule-id fa7f3a53-8919-46ad-a404-e9d47c93a580 --type cpu
  $0 --rule-id fa7f3a53-8919-46ad-a404-e9d47c93a580 --type bandwidth

Description:
  This script removes all adjustment status metrics associated with 
  the specified rule ID from the node_exporter metrics files.

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --rule-id)
            RULE_ID="$2"
            shift 2
            ;;
        --type)
            RULE_TYPE="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: Unknown parameter: $1" >&2
            usage
            exit 1
            ;;
    esac
done

# Validate required parameters
if [[ -z "$RULE_ID" || -z "$RULE_TYPE" ]]; then
    echo "Error: Missing required parameters" >&2
    usage
    exit 1
fi

# Validate type value
if [[ "$RULE_TYPE" != "cpu" && "$RULE_TYPE" != "bandwidth" ]]; then
    echo "Error: Type must be 'cpu' or 'bandwidth'" >&2
    usage
    exit 1
fi

echo "Starting cleanup of $RULE_TYPE adjustment metrics for rule ID: $RULE_ID"

# Function to clean up metrics file
cleanup_metrics_file() {
    local metrics_file=$1
    local prometheus_rule_id=$2
    local metrics_type=$3
    
    if [[ ! -f "$metrics_file" ]]; then
        echo "No $metrics_type metrics file found: $metrics_file"
        return 0
    fi
    
    # Check if any metrics exist for this rule ID (using grep -E for regex support)
    if ! grep -E "rule_id=\"$prometheus_rule_id\"" "$metrics_file" >/dev/null 2>&1; then
        echo "No $metrics_type metrics found for rule ID: $RULE_ID (prometheus rule pattern: $prometheus_rule_id)"
        return 0
    fi
    
    echo "Found $metrics_type metrics for rule ID: $RULE_ID"
    echo "Prometheus rule ID pattern: $prometheus_rule_id"
    
    # Show what will be removed
    echo "Metrics to be removed:"
    grep -E "rule_id=\"$prometheus_rule_id\"" "$metrics_file" | while IFS= read -r line; do
        echo "  $line"
    done
    
    # Remove all metrics lines containing the rule ID (using grep -E for regex support)
    local temp_file="$metrics_file.tmp"
    grep -v -E "rule_id=\"$prometheus_rule_id\"" "$metrics_file" > "$temp_file"
    
    # Check if file is now empty (only comments remain)
    if ! grep -q "^vm_" "$temp_file"; then
        echo "All $metrics_type metrics cleared - removing metrics file"
        rm -f "$temp_file" "$metrics_file"
        echo "Removed empty $metrics_type metrics file"
    else
        mv "$temp_file" "$metrics_file"
        echo "Cleaned $metrics_type metrics for rule ID: $RULE_ID (file preserved with other metrics)"
    fi
}

# Clean up based on rule type
if [[ "$RULE_TYPE" == "cpu" ]]; then
    # CPU metrics use format: cpu-$RULE_ID
    PROMETHEUS_RULE_ID="cpu-$RULE_ID"
    cleanup_metrics_file "$CPU_METRICS_FILE" "$PROMETHEUS_RULE_ID" "CPU adjustment"
elif [[ "$RULE_TYPE" == "bandwidth" ]]; then
    # Bandwidth metrics use format: adjust-bw-domain-$RULE_ID  
    PROMETHEUS_RULE_ID="adjust-bw-.*-$RULE_ID"
    cleanup_metrics_file "$BANDWIDTH_METRICS_FILE" "$PROMETHEUS_RULE_ID" "bandwidth adjustment"
fi

echo "$RULE_TYPE adjustment metrics cleanup completed successfully for rule ID: $RULE_ID"
exit 0
