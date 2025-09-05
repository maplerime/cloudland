#!/bin/bash

# VM CPU Limit Status Detection Script
# Purpose: Real-time detection of VM CPU limitation status using virsh commands
# Author: CloudLand Resource Management System
# Version: 1.0

set -e

# Default values
DOMAIN=""
OUTPUT_FORMAT="human"  # human, json, status-only

# Usage help
usage() {
    cat << EOF
Usage: $0 --domain <vm_domain> [--format <human|json|status-only>]

Parameters:
  --domain    VM domain name (required)
  --format    Output format (optional, default: human)
              human      = Human readable output
              json       = JSON format output
              status-only = Only output 0 (not limited) or 1 (limited)

Examples:
  $0 --domain inst-6
  $0 --domain inst-6 --format json
  $0 --domain inst-6 --format status-only

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --domain)
            DOMAIN="$2"
            shift 2
            ;;
        --format)
            OUTPUT_FORMAT="$2"
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
if [[ -z "$DOMAIN" ]]; then
    echo "Error: Missing required parameter --domain" >&2
    usage
    exit 1
fi

# Validate format parameter
if [[ "$OUTPUT_FORMAT" != "human" && "$OUTPUT_FORMAT" != "json" && "$OUTPUT_FORMAT" != "status-only" ]]; then
    echo "Error: Invalid format. Must be 'human', 'json', or 'status-only'" >&2
    usage
    exit 1
fi

# Check if domain exists and is accessible
if ! virsh dominfo "$DOMAIN" >/dev/null 2>&1; then
    echo "Error: Domain '$DOMAIN' not found or not accessible" >&2
    exit 1
fi

# Get CPU information
vcpu_count=$(virsh dominfo "$DOMAIN" | grep "CPU(s)" | awk '{print $2}')
vcpu_period=$(virsh schedinfo "$DOMAIN" | grep "vcpu_period" | awk '{print $3}')
vcpu_quota=$(virsh schedinfo "$DOMAIN" | grep "vcpu_quota" | awk '{print $3}')

# Validate extracted values
if [[ -z "$vcpu_count" || -z "$vcpu_period" || -z "$vcpu_quota" ]]; then
    echo "Error: Failed to extract CPU information from domain '$DOMAIN'" >&2
    exit 1
fi

# Convert to integers for calculation
if ! [[ "$vcpu_count" =~ ^[0-9]+$ ]] || ! [[ "$vcpu_period" =~ ^[0-9]+$ ]] || ! [[ "$vcpu_quota" =~ ^-?[0-9]+$ ]]; then
    echo "Error: Invalid CPU information extracted (non-numeric values)" >&2
    exit 1
fi

# Calculate theoretical maximum quota (100% per core)
max_quota=$((vcpu_period * vcpu_count))

# Determine limitation status
is_limited=0
current_percent=0

# Handle special cases
if [[ $vcpu_quota -eq -1 ]]; then
    # -1 means unlimited
    is_limited=0
    current_percent=100
    status_text="unlimited"
elif [[ $vcpu_quota -le 0 ]]; then
    # Invalid or zero quota
    echo "Error: Invalid vcpu_quota value: $vcpu_quota" >&2
    exit 1
elif [[ $vcpu_quota -ge $max_quota ]]; then
    # Full speed or higher
    is_limited=0
    current_percent=$((vcpu_quota * 100 / max_quota))
    status_text="not limited"
else
    # Limited
    is_limited=1
    current_percent=$((vcpu_quota * 100 / max_quota))
    status_text="limited"
fi

# Output based on format
case "$OUTPUT_FORMAT" in
    "status-only")
        echo "$is_limited"
        ;;
    "json")
        cat << EOF
{
  "domain": "$DOMAIN",
  "is_limited": $is_limited,
  "status": "$status_text",
  "vcpu_count": $vcpu_count,
  "vcpu_period": $vcpu_period,
  "vcpu_quota": $vcpu_quota,
  "max_quota": $max_quota,
  "current_percent": $current_percent,
  "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
}
EOF
        ;;
    "human"|*)
        echo "Domain: $DOMAIN"
        echo "CPU Cores: $vcpu_count"
        echo "Period: $vcpu_period"
        echo "Current Quota: $vcpu_quota"
        echo "Max Quota: $max_quota"
        echo "Current CPU Allocation: ${current_percent}%"
        echo "Status: $status_text"
        echo "Is Limited: $is_limited"
        ;;
esac

exit 0 