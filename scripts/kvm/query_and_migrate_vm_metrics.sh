#!/bin/bash

# VM Metrics Migration Script
# Purpose: Query VM custom metrics from Prometheus and write to target node
# Author: CloudLand Resource Management System
# Version: 1.1
# Dependencies: curl, jq

set -e

# Check dependencies
if ! command -v jq >/dev/null 2>&1; then
    echo "Error: jq is not installed. Please install jq to use this script." >&2
    exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
    echo "Error: curl is not installed. Please install curl to use this script." >&2
    exit 1
fi

# Default values
DOMAIN=""
PROMETHEUS_HOST=""
PROMETHEUS_PORT=""
OUTPUT_FORMAT="json"

# Usage help
usage() {
    cat << EOF
Usage: $0 --domain <vm_domain> --prometheus-host <host> --prometheus-port <port> [options]

Parameters:
  --domain           VM domain name (required)
  --prometheus-host  Prometheus server IP/hostname (required)
  --prometheus-port  Prometheus server port (required)
  --output-format   Output format (optional, default: json)

Examples:
  $0 --domain inst-6 --prometheus-host 192.168.1.100 --prometheus-port 9090
  $0 --domain inst-6 --prometheus-host localhost --prometheus-port 9090

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --domain)
            DOMAIN="$2"
            shift 2
            ;;
        --prometheus-host)
            PROMETHEUS_HOST="$2"
            shift 2
            ;;
        --prometheus-port)
            PROMETHEUS_PORT="$2"
            shift 2
            ;;
        --output-format)
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
if [[ -z "$DOMAIN" || -z "$PROMETHEUS_HOST" || -z "$PROMETHEUS_PORT" ]]; then
    echo "Error: Missing required parameters" >&2
    usage
    exit 1
fi

# Validate port number
if ! [[ "$PROMETHEUS_PORT" =~ ^[0-9]+$ ]] || [ "$PROMETHEUS_PORT" -le 0 ] || [ "$PROMETHEUS_PORT" -gt 65535 ]; then
    echo "Error: Invalid port number: $PROMETHEUS_PORT" >&2
    exit 1
fi

# Construct Prometheus URL
PROMETHEUS_URL="http://${PROMETHEUS_HOST}:${PROMETHEUS_PORT}"
QUERY_API="${PROMETHEUS_URL}/api/v1/query"

echo "Querying VM metrics for domain: $DOMAIN from Prometheus: $PROMETHEUS_URL"

# Function to extract latest metric value and rule_id from metric labels
query_latest_metric_with_rule_id() {
    local metric_name="$1"
    local query="${metric_name}{domain=\"${DOMAIN}\"}"
    local url="${QUERY_API}?query=${query}"
    
    echo "Querying latest metric with rule_id: $metric_name" >&2
    
    # Use curl to query Prometheus
    local response
    response=$(curl -s --connect-timeout 10 --max-time 30 "$url" 2>/dev/null)
    
    if [ $? -ne 0 ]; then
        echo "Warning: Failed to query metric $metric_name from Prometheus" >&2
        echo "|"  # Return empty value and rule_id
        return 1
    fi
    
    # Parse JSON response to extract the latest metric value and rule_id using jq
    local result
    result=$(echo "$response" | jq -r '
        if .status == "success" and (.data.result | length) > 0 then
            .data.result
            | map(select(.value[1] != null))
            | sort_by(.value[0] | tonumber)
            | last
            | "\(.value[1] // "")|\(.metric.rule_id // "")|\(.value[0] | floor)"
        else
            "||"
        end
    ' 2>/dev/null || echo "||")
    
    echo "$result"
}

# Query CPU adjustment status
echo "=== Querying Latest CPU Adjustment Status ==="
cpu_result=$(query_latest_metric_with_rule_id "vm_cpu_adjustment_status")
cpu_status=$(echo "$cpu_result" | cut -d'|' -f1)
cpu_rule_id=$(echo "$cpu_result" | cut -d'|' -f2)
cpu_timestamp=$(echo "$cpu_result" | cut -d'|' -f3)

if [[ -z "$cpu_status" ]]; then
    cpu_status="0"  # Default to not limited
    echo "No CPU adjustment status found, using default: $cpu_status"
else
    echo "Latest CPU adjustment status: $cpu_status"
    if [[ -n "$cpu_timestamp" ]]; then
        cpu_time_readable=$(date -d "@$cpu_timestamp" 2>/dev/null || echo "Unknown")
        echo "CPU metric timestamp: $cpu_timestamp ($cpu_time_readable)"
    fi
fi

if [[ -n "$cpu_rule_id" ]]; then
    echo "CPU rule ID: $cpu_rule_id"
fi

# Query Bandwidth adjustment status  
echo "=== Querying Latest Bandwidth Adjustment Status ==="
bandwidth_result=$(query_latest_metric_with_rule_id "vm_bandwidth_adjustment_status")
bandwidth_status=$(echo "$bandwidth_result" | cut -d'|' -f1)
bandwidth_rule_id=$(echo "$bandwidth_result" | cut -d'|' -f2)
bandwidth_timestamp=$(echo "$bandwidth_result" | cut -d'|' -f3)

if [[ -z "$bandwidth_status" ]]; then
    bandwidth_status="0"  # Default to not limited
    echo "No bandwidth adjustment status found, using default: $bandwidth_status"
else
    echo "Latest bandwidth adjustment status: $bandwidth_status"
    if [[ -n "$bandwidth_timestamp" ]]; then
        bandwidth_time_readable=$(date -d "@$bandwidth_timestamp" 2>/dev/null || echo "Unknown")
        echo "Bandwidth metric timestamp: $bandwidth_timestamp ($bandwidth_time_readable)"
    fi
fi

if [[ -n "$bandwidth_rule_id" ]]; then
    echo "Bandwidth rule ID: $bandwidth_rule_id"
fi

# Write metrics to target node
echo "=== Writing Metrics to Target Node ==="

# Write CPU adjustment status (use default rule_id if none found)
if [[ -z "$cpu_rule_id" ]]; then
    cpu_rule_id="default"
    echo "Using default CPU rule_id: $cpu_rule_id"
fi

if [[ "$cpu_status" != "" ]]; then
    echo "Writing CPU adjustment metric..."
    if /opt/cloudland/scripts/kvm/update_vm_cpu_adjustment_status.sh --domain "$DOMAIN" --rule-id "$cpu_rule_id" --status "$cpu_status"; then
        echo "Successfully wrote CPU adjustment status: $cpu_status"
    else
        echo "Warning: Failed to write CPU adjustment status" >&2
    fi
else
    echo "Skipping CPU adjustment metric (no status data)"
fi

# Write Bandwidth adjustment status if we have data
if [[ -n "$bandwidth_rule_id" && "$bandwidth_status" != "" ]]; then
    echo "Writing bandwidth adjustment metric..."
    if /opt/cloudland/scripts/kvm/update_vm_bandwidth_adjustment_status.sh --domain "$DOMAIN" --rule-id "$bandwidth_rule_id" --status "$bandwidth_status"; then
        echo "Successfully wrote bandwidth adjustment status: $bandwidth_status"
    else
        echo "Warning: Failed to write bandwidth adjustment status" >&2
    fi
else
    echo "Skipping bandwidth adjustment metric (no data or rule_id)"
fi

# Output summary
echo "=== Migration Summary ==="
echo "Domain: $DOMAIN"
echo "Prometheus Server: $PROMETHEUS_HOST:$PROMETHEUS_PORT"
echo "CPU Status: $cpu_status $(if [[ -n "$cpu_rule_id" ]]; then echo "(Rule: $cpu_rule_id)"; fi) $(if [[ -n "$cpu_timestamp" ]]; then echo "(Timestamp: $cpu_timestamp)"; fi)"
echo "Bandwidth Status: $bandwidth_status $(if [[ -n "$bandwidth_rule_id" ]]; then echo "(Rule: $bandwidth_rule_id)"; fi) $(if [[ -n "$bandwidth_timestamp" ]]; then echo "(Timestamp: $bandwidth_timestamp)"; fi)"
echo "Metrics migration completed successfully"

exit 0 