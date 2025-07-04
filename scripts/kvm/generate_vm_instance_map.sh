#!/bin/bash
set -euo pipefail

# Configuration directories
XML_DIR="/opt/cloudland/cache/xml"
PROM_DIR="/var/lib/node_exporter"
TMP_FILE="${PROM_DIR}/vm_instance_map.prom.tmp"
FINAL_FILE="${PROM_DIR}/vm_instance_map.prom"

# Command line arguments
ACTION=${1:-"full"}  # Default action is full sync
DOMAIN=${2:-""}      # Domain name for add/remove actions

# Ensure output directory exists with proper permissions
mkdir -p "$PROM_DIR"
chmod 755 "$PROM_DIR"  # Ensure directory has sufficient read permissions

# Get hostname for hypervisor label
hypervisor=$(hostname)

# Function to extract instance_id from XML file
extract_instance_id() {
    local xml_file=$1
    local instance_id=""
    
    # 使用grep直接提取cloudland:instance_id的值
    instance_id=$(grep -o "<cloudland:instance_id>.*</cloudland:instance_id>" "$xml_file" | sed -e 's/<cloudland:instance_id>\(.*\)<\/cloudland:instance_id>/\1/')
    
    # 如果读取失败，返回空字符串，不要使用uuid
    echo "$instance_id"
}

# Function to add a VM to metrics
add_vm_to_metrics() {
    local domain=$1
    local vm_dir="$XML_DIR/$domain"
    local output_file=$2
    local found=false
    
    # Process main VM file
    main_xml="$vm_dir/$domain.xml"
    if [ -f "$main_xml" ]; then
        instance_id=$(extract_instance_id "$main_xml")
        if [[ -n "$domain" && -n "$instance_id" ]]; then
            echo "vm_instance_map{domain=\"$domain\",instance_id=\"$instance_id\",hypervisor=\"$hypervisor\",mode=\"normal\"} 1" >> "$output_file"
            found=true
        fi
    fi
    
    # Process rescue VM file if exists
    rescue_xml="$vm_dir/$domain-rescue.xml"
    if [ -f "$rescue_xml" ]; then
        rescue_instance_id=$(extract_instance_id "$rescue_xml")
        # If instance_id is empty but we found it in the main XML, use that
        if [ -z "$rescue_instance_id" ] && [ -n "${instance_id:-}" ]; then
            rescue_instance_id="$instance_id"
        fi
        
        if [[ -n "$domain" && -n "$rescue_instance_id" ]]; then
            rescue_domain="${domain}-rescue"
            echo "vm_instance_map{domain=\"$rescue_domain\",instance_id=\"$rescue_instance_id\",hypervisor=\"$hypervisor\",mode=\"rescue\"} 1" >> "$output_file"
            found=true
        fi
    fi
    
    echo "$found"
}

# Function to remove a VM from metrics
remove_vm_from_metrics() {
    local domain=$1
    local rescue_domain="${domain}-rescue"
    
    # Create a temporary file for the filtered content
    local filtered_file="${PROM_DIR}/vm_instance_map.filtered.tmp"
    
    # Filter out lines containing the domain
    if [ -f "$FINAL_FILE" ]; then
        grep -v "domain=\"$domain\"" "$FINAL_FILE" | grep -v "domain=\"$rescue_domain\"" > "$filtered_file" || true
        mv "$filtered_file" "$FINAL_FILE"
        # Set permissions for Prometheus to read
        chmod 644 "$FINAL_FILE"
        if getent passwd prometheus > /dev/null; then
            chown prometheus:prometheus "$FINAL_FILE"
        fi
        echo "Removed domain $domain from metrics file"
    else
        echo "Metrics file does not exist, nothing to remove"
    fi
}

# Main logic based on action
case "$ACTION" in
    "full")
        # Clear temporary file
        > "$TMP_FILE"
        
        # Add metric help information and type
        echo "# HELP vm_instance_map Mapping between VM domain and instance_id" >> "$TMP_FILE"
        echo "# TYPE vm_instance_map gauge" >> "$TMP_FILE"
        
        # Process all VM directories
        if [ -d "$XML_DIR" ]; then
            for vm_dir in "$XML_DIR"/*; do
                if [ -d "$vm_dir" ]; then
                    domain=$(basename "$vm_dir")
                    add_vm_to_metrics "$domain" "$TMP_FILE" > /dev/null
                fi
            done
        fi
        
        # Atomic file replacement
        mv "$TMP_FILE" "$FINAL_FILE"
        
        # Set permissions for Prometheus to read
        chmod 644 "$FINAL_FILE"
        if getent passwd prometheus > /dev/null; then
            chown prometheus:prometheus "$FINAL_FILE"
        fi
        
        echo "Full VM instance mapping metrics generated at $FINAL_FILE"
        ;;
        
    "add")
        if [ -z "$DOMAIN" ]; then
            echo "Error: Domain name is required for add action"
            exit 1
        fi
        
        # Create temporary file if final file doesn't exist
        if [ ! -f "$FINAL_FILE" ]; then
            echo "# HELP vm_instance_map Mapping between VM domain and instance_id" > "$FINAL_FILE"
            echo "# TYPE vm_instance_map gauge" >> "$FINAL_FILE"
            # Set permissions for Prometheus to read
            chmod 644 "$FINAL_FILE"
            if getent passwd prometheus > /dev/null; then
                chown prometheus:prometheus "$FINAL_FILE"
            fi
        fi
        
        # Remove existing entries for this domain first
        remove_vm_from_metrics "$DOMAIN" > /dev/null
        
        # Add the VM to metrics
        found=$(add_vm_to_metrics "$DOMAIN" "$FINAL_FILE")
        
        if [ "$found" = "true" ]; then
            echo "Added domain $DOMAIN to metrics file"
        else
            echo "No valid XML found for domain $DOMAIN"
        fi
        ;;
        
    "remove")
        if [ -z "$DOMAIN" ]; then
            echo "Error: Domain name is required for remove action"
            exit 1
        fi
        
        remove_vm_from_metrics "$DOMAIN"
        ;;
        
    *)
        echo "Error: Invalid action. Use 'full', 'add', or 'remove'"
        exit 1
        ;;
esac

exit 0 
