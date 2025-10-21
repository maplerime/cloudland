#!/bin/bash

# VM CPU Adjustment Status Management Script
# Purpose: Update VM CPU adjustment status metrics for Prometheus monitoring
# Author: CloudLand Resource Management System
# Version: 1.0

set -e

# Configuration
METRICS_DIR="/var/lib/node_exporter"
METRICS_FILE="$METRICS_DIR/vm_cpu_adjustment_status.prom"
TEMP_FILE="$METRICS_FILE.tmp"

# Default values
DOMAIN=""
RULE_ID=""
STATUS=""

# Usage help
usage() {
    cat << EOF
Usage: $0 --domain <vm_domain> --rule-id <rule_id> --status <0|1>

Parameters:
  --domain    VM domain name (required)
  --rule-id   Rule ID for tracking (required)  
  --status    CPU adjustment status (required)
              0 = normal (not limited)
              1 = limited

Examples:
  $0 --domain inst-6 --rule-id inst-6-7c64dbfd-d676-4232-ae61-52f9cc75f890 --status 0
  $0 --domain inst-6 --rule-id inst-6-7c64dbfd-d676-4232-ae61-52f9cc75f890 --status 1

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --domain)
            DOMAIN="$2"
            shift 2
            ;;
        --rule-id)
            RULE_ID="$2"
            shift 2
            ;;
        --status)
            STATUS="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: Unknown parameter: $1"
            usage
            exit 1
            ;;
    esac
done

# Validate required parameters
if [[ -z "$DOMAIN" || -z "$RULE_ID" || -z "$STATUS" ]]; then
    echo "Error: Missing required parameters"
    usage
    exit 1
fi

# Validate status value
if [[ "$STATUS" != "0" && "$STATUS" != "1" ]]; then
    echo "Error: Status must be 0 (normal) or 1 (limited)"
    usage
    exit 1
fi

# Ensure metrics directory exists, create if not
if [[ ! -d "$METRICS_DIR" ]]; then
    echo "Error: Metrics directory $METRICS_DIR does not exist. Please ensure it is created by Prometheus."
    exit 1
fi

# Build metric line
#METRIC_LINE="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$RULE_ID\"} $STATUS"
PROMETHEUS_RULE_ID="cpu-$RULE_ID"
METRIC_LINE="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"} $STATUS"

# Check if this is a recovery operation (status = 0)
if [[ "$STATUS" == "0" ]]; then
    # For recovery, check if metrics file exists
    if [[ ! -f "$METRICS_FILE" ]]; then
        echo "Warning: No existing metrics file found for recovery operation"
        echo "No action needed - VM is already in normal state"
        exit 0
    fi
    
    # Check if the specific metric exists
    PATTERN="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"}"
    if ! grep -q "^$PATTERN" "$METRICS_FILE"; then
        echo "Warning: No existing metric found for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
        echo "No action needed - VM is already in normal state"
        exit 0
    fi
fi

# If metrics file doesn't exist, create new file
if [[ ! -f "$METRICS_FILE" ]]; then
    echo "Creating new metrics file: $METRICS_FILE"
    echo "# VM CPU adjustment status metrics" > "$TEMP_FILE"
    echo "# 0 = normal, 1 = limited" >> "$TEMP_FILE"
    echo "$METRIC_LINE" >> "$TEMP_FILE"
    mv "$TEMP_FILE" "$METRICS_FILE"
    echo "CPU adjustment status updated successfully (new file created)"
    exit 0
fi

# Check if metric with same domain and rule_id already exists
PATTERN="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"}"

if grep -q "^$PATTERN" "$METRICS_FILE"; then
    # Update existing metric
    echo "Updating existing metric for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
    
    # For recovery (status=0), remove the metric line entirely
    if [[ "$STATUS" == "0" ]]; then
        # Remove the specific metric line and keep others
        grep -v "^$PATTERN" "$METRICS_FILE" > "$TEMP_FILE"
        
        # Check if file is now empty (only comments remain)
        if ! grep -q "^vm_cpu_adjustment_status" "$TEMP_FILE"; then
            echo "All CPU adjustment metrics cleared - removing metrics file"
            rm -f "$TEMP_FILE" "$METRICS_FILE"
        else
            mv "$TEMP_FILE" "$METRICS_FILE"
        fi
        echo "CPU adjustment status recovered successfully (metric removed)"
    else
        # Update existing metric with new status
        while IFS= read -r line; do
            if [[ "$line" =~ ^$PATTERN ]]; then
                echo "$METRIC_LINE"
            else
                echo "$line"
            fi
        done < "$METRICS_FILE" > "$TEMP_FILE"
        
        mv "$TEMP_FILE" "$METRICS_FILE"
        echo "CPU adjustment status updated successfully (existing metric updated)"
    fi
else
    # Add new metric (only for status=1, limiting case)
    if [[ "$STATUS" == "1" ]]; then
        echo "Adding new metric for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
        echo "$METRIC_LINE" >> "$METRICS_FILE"
        echo "CPU adjustment status updated successfully (new metric added)"
    else
        echo "No existing metric to recover for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
        echo "No action needed - VM is already in normal state"
    fi
fi

# Verify file is readable (if it still exists)
if [[ -f "$METRICS_FILE" && ! -r "$METRICS_FILE" ]]; then
    echo "Error: Failed to create/update metrics file or file is not readable"
    exit 1
fi

echo "Operation completed successfully"
