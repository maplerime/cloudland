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

# Build metric lines
#METRIC_LINE="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$RULE_ID\"} $STATUS"
PROMETHEUS_RULE_ID="adjust-cpu-$RULE_ID"
STATUS_METRIC_LINE="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"} $STATUS"

# For status=1 (limited), also add timestamp metric
TIMESTAMP_METRIC_LINE=""
if [[ "$STATUS" == "1" ]]; then
    # Get current timestamp in milliseconds
    TIMESTAMP=$(date +%s%3N)
    TIMESTAMP_METRIC_LINE="vm_cpu_limit_start_timestamp{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"} $TIMESTAMP"
fi

# Check if this is a recovery operation (status = 0)
if [[ "$STATUS" == "0" ]]; then
    # For recovery, check if metrics file exists
    if [[ ! -f "$METRICS_FILE" ]]; then
        echo "Warning: No existing metrics file found for recovery operation"
        echo "No action needed - VM is already in normal state"
        exit 0
    fi
    
    # Check if the specific metric exists
    STATUS_PATTERN="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"}"
    TIMESTAMP_PATTERN="vm_cpu_limit_start_timestamp{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"}"
    if ! grep -q "^$STATUS_PATTERN" "$METRICS_FILE" && ! grep -q "^$TIMESTAMP_PATTERN" "$METRICS_FILE"; then
        echo "Warning: No existing metrics found for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
        echo "No action needed - VM is already in normal state"
        exit 0
    fi
fi

# If metrics file doesn't exist, create new file
if [[ ! -f "$METRICS_FILE" ]]; then
    echo "Creating new metrics file: $METRICS_FILE"
    echo "# VM CPU adjustment status metrics" > "$TEMP_FILE"
    echo "# 0 = normal, 1 = limited" >> "$TEMP_FILE"
    echo "$STATUS_METRIC_LINE" >> "$TEMP_FILE"
    if [[ -n "$TIMESTAMP_METRIC_LINE" ]]; then
        echo "$TIMESTAMP_METRIC_LINE" >> "$TEMP_FILE"
    fi
    mv "$TEMP_FILE" "$METRICS_FILE"
    echo "CPU adjustment status updated successfully (new file created)"
    exit 0
fi

# Check if metric with same domain and rule_id already exists
STATUS_PATTERN="vm_cpu_adjustment_status{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"}"
TIMESTAMP_PATTERN="vm_cpu_limit_start_timestamp{domain=\"$DOMAIN\", rule_id=\"$PROMETHEUS_RULE_ID\"}"

if grep -q "^$STATUS_PATTERN" "$METRICS_FILE" || grep -q "^$TIMESTAMP_PATTERN" "$METRICS_FILE"; then
    # Update existing metrics
    echo "Updating existing metrics for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
    
    # For recovery (status=0), remove both metric lines entirely
    if [[ "$STATUS" == "0" ]]; then
        # Remove both status and timestamp metrics
        grep -v "^$STATUS_PATTERN" "$METRICS_FILE" | grep -v "^$TIMESTAMP_PATTERN" > "$TEMP_FILE"
        
        # Check if file is now empty (only comments remain)
        if ! grep -q "^vm_cpu_adjustment_status\|^vm_cpu_limit_start_timestamp" "$TEMP_FILE"; then
            echo "All CPU adjustment metrics cleared - removing metrics file"
            rm -f "$TEMP_FILE" "$METRICS_FILE"
        else
            mv "$TEMP_FILE" "$METRICS_FILE"
        fi
        echo "CPU adjustment status recovered successfully (both metrics removed)"
    else
        # Update existing metrics with new values
        # First, remove old metrics
        grep -v "^$STATUS_PATTERN" "$METRICS_FILE" | grep -v "^$TIMESTAMP_PATTERN" > "$TEMP_FILE"
        # Then, add updated metrics
        echo "$STATUS_METRIC_LINE" >> "$TEMP_FILE"
        if [[ -n "$TIMESTAMP_METRIC_LINE" ]]; then
            echo "$TIMESTAMP_METRIC_LINE" >> "$TEMP_FILE"
        fi
        
        mv "$TEMP_FILE" "$METRICS_FILE"
        echo "CPU adjustment status updated successfully (existing metrics updated)"
    fi
else
    # Add new metrics (only for status=1, limiting case)
    if [[ "$STATUS" == "1" ]]; then
        echo "Adding new metrics for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
        echo "$STATUS_METRIC_LINE" >> "$METRICS_FILE"
        if [[ -n "$TIMESTAMP_METRIC_LINE" ]]; then
            echo "$TIMESTAMP_METRIC_LINE" >> "$METRICS_FILE"
        fi
        echo "CPU adjustment status updated successfully (new metrics added)"
    else
        echo "No existing metrics to recover for domain=$DOMAIN, rule_id=$PROMETHEUS_RULE_ID"
        echo "No action needed - VM is already in normal state"
    fi
fi

# Verify file is readable (if it still exists)
if [[ -f "$METRICS_FILE" && ! -r "$METRICS_FILE" ]]; then
    echo "Error: Failed to create/update metrics file or file is not readable"
    exit 1
fi

echo "Operation completed successfully"
