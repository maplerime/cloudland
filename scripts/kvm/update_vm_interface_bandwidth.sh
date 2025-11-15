#!/bin/bash

# VM Interface Bandwidth Configuration Management Script
# Purpose: Manage bandwidth configuration metrics for Prometheus monitoring
# This script maintains the configured bandwidth limits for VM interfaces

set -e

# Configuration
METRICS_DIR="/var/lib/node_exporter"
METRICS_FILE="$METRICS_DIR/vm_interface_bandwidth_config.prom"
TEMP_FILE="$METRICS_FILE.tmp"

# Operation types
OPERATION=""
DOMAIN=""
TARGET_DEVICE=""
INBOUND=""
OUTBOUND=""

usage() {
    cat << EOF
Usage: $0 <operation> [parameters]

Operations:
  add <domain> <target_device> <inbound_mbps> <outbound_mbps>
      Add or update bandwidth configuration for a VM interface
      
  delete <domain> <target_device>
      Delete bandwidth configuration for a specific VM interface
      
  delete_domain <domain>
      Delete all bandwidth configurations for a VM

Parameters:
  domain          VM domain name (e.g., inst-6)
  target_device   Network device name (e.g., tap6c299b)
  inbound_mbps    Inbound bandwidth limit in Mbps
  outbound_mbps   Outbound bandwidth limit in Mbps

Examples:
  # Add/update bandwidth configuration
  $0 add inst-6 tap6c299b 1000 1000
  
  # Delete specific interface configuration
  $0 delete inst-6 tap6c299b
  
  # Delete all configurations for a VM
  $0 delete_domain inst-6

Metrics Format:
  vm_interface_bandwidth_config_mbps{domain="<domain>",target_device="<device>",direction="in"} <value>
  vm_interface_bandwidth_config_mbps{domain="<domain>",target_device="<device>",direction="out"} <value>

EOF
}

# Validate parameters
validate_params() {
    case "$OPERATION" in
        add)
            if [[ -z "$DOMAIN" || -z "$TARGET_DEVICE" || -z "$INBOUND" || -z "$OUTBOUND" ]]; then
                echo "Error: Missing required parameters for 'add' operation"
                usage
                exit 1
            fi
            if ! [[ "$INBOUND" =~ ^[0-9]+$ ]] || ! [[ "$OUTBOUND" =~ ^[0-9]+$ ]]; then
                echo "Error: Bandwidth values must be positive integers"
                exit 1
            fi
            ;;
        delete)
            if [[ -z "$DOMAIN" || -z "$TARGET_DEVICE" ]]; then
                echo "Error: Missing required parameters for 'delete' operation"
                usage
                exit 1
            fi
            ;;
        delete_domain)
            if [[ -z "$DOMAIN" ]]; then
                echo "Error: Missing domain parameter for 'delete_domain' operation"
                usage
                exit 1
            fi
            ;;
        *)
            echo "Error: Invalid operation: $OPERATION"
            usage
            exit 1
            ;;
    esac
}

# Initialize metrics file if it doesn't exist
init_metrics_file() {
    if [[ ! -d "$METRICS_DIR" ]]; then
        mkdir -p "$METRICS_DIR"
    fi
    
    if [[ ! -f "$METRICS_FILE" ]]; then
        cat > "$METRICS_FILE" << 'EOF'
# HELP vm_interface_bandwidth_config_mbps Configured bandwidth limit for VM interface in Mbps
# TYPE vm_interface_bandwidth_config_mbps gauge
EOF
        echo "Initialized metrics file: $METRICS_FILE"
    fi
}

# Add or update bandwidth configuration
add_bandwidth_config() {
    local domain="$1"
    local device="$2"
    local inbound="$3"
    local outbound="$4"
    
    init_metrics_file
    
    # Read existing metrics, excluding header and entries for this domain+device
    if [[ -f "$METRICS_FILE" ]]; then
        grep -E '^#' "$METRICS_FILE" > "$TEMP_FILE" || true
        grep -v -E "^(#|vm_interface_bandwidth_config_mbps\{domain=\"${domain}\",target_device=\"${device}\")" "$METRICS_FILE" >> "$TEMP_FILE" 2>/dev/null || true
    else
        cat > "$TEMP_FILE" << 'EOF'
# HELP vm_interface_bandwidth_config_mbps Configured bandwidth limit for VM interface in Mbps
# TYPE vm_interface_bandwidth_config_mbps gauge
EOF
    fi
    
    # Add new entries
    echo "vm_interface_bandwidth_config_mbps{domain=\"${domain}\",target_device=\"${device}\",direction=\"in\"} ${inbound}" >> "$TEMP_FILE"
    echo "vm_interface_bandwidth_config_mbps{domain=\"${domain}\",target_device=\"${device}\",direction=\"out\"} ${outbound}" >> "$TEMP_FILE"
    
    # Atomic update
    mv "$TEMP_FILE" "$METRICS_FILE"
    
    echo "Updated bandwidth config: domain=${domain}, device=${device}, in=${inbound}Mbps, out=${outbound}Mbps"
}

# Delete bandwidth configuration for a specific interface
delete_bandwidth_config() {
    local domain="$1"
    local device="$2"
    
    if [[ ! -f "$METRICS_FILE" ]]; then
        echo "Metrics file does not exist, nothing to delete"
        return 0
    fi
    
    # Read existing metrics, excluding entries for this domain+device
    grep -E '^#' "$METRICS_FILE" > "$TEMP_FILE" || true
    grep -v -E "^(#|vm_interface_bandwidth_config_mbps\{domain=\"${domain}\",target_device=\"${device}\")" "$METRICS_FILE" >> "$TEMP_FILE" 2>/dev/null || true
    
    # Atomic update
    mv "$TEMP_FILE" "$METRICS_FILE"
    
    echo "Deleted bandwidth config: domain=${domain}, device=${device}"
}

# Delete all bandwidth configurations for a domain
delete_domain_config() {
    local domain="$1"
    
    if [[ ! -f "$METRICS_FILE" ]]; then
        echo "Metrics file does not exist, nothing to delete"
        return 0
    fi
    
    # Read existing metrics, excluding all entries for this domain
    grep -E '^#' "$METRICS_FILE" > "$TEMP_FILE" || true
    grep -v -E "^(#|vm_interface_bandwidth_config_mbps\{domain=\"${domain}\")" "$METRICS_FILE" >> "$TEMP_FILE" 2>/dev/null || true
    
    # Atomic update
    mv "$TEMP_FILE" "$METRICS_FILE"
    
    echo "Deleted all bandwidth configs for domain: ${domain}"
}

# Main logic
main() {
    if [[ $# -lt 1 ]]; then
        echo "Error: No operation specified"
        usage
        exit 1
    fi
    
    OPERATION="$1"
    shift
    
    case "$OPERATION" in
        add)
            if [[ $# -ne 4 ]]; then
                echo "Error: 'add' requires 4 parameters"
                usage
                exit 1
            fi
            DOMAIN="$1"
            TARGET_DEVICE="$2"
            INBOUND="$3"
            OUTBOUND="$4"
            validate_params
            add_bandwidth_config "$DOMAIN" "$TARGET_DEVICE" "$INBOUND" "$OUTBOUND"
            ;;
        delete)
            if [[ $# -ne 2 ]]; then
                echo "Error: 'delete' requires 2 parameters"
                usage
                exit 1
            fi
            DOMAIN="$1"
            TARGET_DEVICE="$2"
            validate_params
            delete_bandwidth_config "$DOMAIN" "$TARGET_DEVICE"
            ;;
        delete_domain)
            if [[ $# -ne 1 ]]; then
                echo "Error: 'delete_domain' requires 1 parameter"
                usage
                exit 1
            fi
            DOMAIN="$1"
            validate_params
            delete_domain_config "$DOMAIN"
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: Unknown operation: $OPERATION"
            usage
            exit 1
            ;;
    esac
}

# Run main function
main "$@"

