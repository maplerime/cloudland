#!/bin/bash

# Cleanup Old Rule ID Metrics Script
# Purpose: Clean up old format rule_id metrics and migrate to new typed format
# Author: CloudLand Resource Management System
# Version: 1.0

set -e

# Configuration
METRICS_DIR="/var/lib/node_exporter"
CPU_METRICS_FILE="$METRICS_DIR/vm_cpu_adjustment_status.prom"
BW_METRICS_FILE="$METRICS_DIR/vm_bandwidth_adjustment_status.prom"
BACKUP_DIR="$METRICS_DIR/backup"
TEMP_DIR="$METRICS_DIR/temp"

# Logging function
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >&2
}

# Usage help
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Clean up old format rule_id metrics and migrate to new typed format.

Options:
  --dry-run       Show what would be changed without making actual changes
  --backup        Create backup of original files before cleanup
  --force         Force cleanup without confirmation prompts
  --help          Show this help message

Examples:
  $0 --dry-run                    # Preview changes
  $0 --backup --force            # Backup and cleanup automatically
  $0                             # Interactive cleanup

Old format: domain-groupUUID
New format: cpu-domain-groupUUID or adjust-bw-domain-groupUUID
EOF
}

# Parse command line arguments
DRY_RUN=false
BACKUP=false
FORCE=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --backup)
            BACKUP=true
            shift
            ;;
        --force)
            FORCE=true
            shift
            ;;
        --help)
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

# Create necessary directories
if [[ "$BACKUP" == "true" && "$DRY_RUN" == "false" ]]; then
    mkdir -p "$BACKUP_DIR"
fi
mkdir -p "$TEMP_DIR"

# Function to backup file
backup_file() {
    local file="$1"
    if [[ -f "$file" && "$BACKUP" == "true" && "$DRY_RUN" == "false" ]]; then
        local backup_file="$BACKUP_DIR/$(basename "$file").$(date '+%Y%m%d_%H%M%S')"
        cp "$file" "$backup_file"
        log "Backed up $file to $backup_file"
    fi
}

# Function to clean CPU metrics
clean_cpu_metrics() {
    local file="$1"
    local temp_file="$TEMP_DIR/$(basename "$file").tmp"
    
    if [[ ! -f "$file" ]]; then
        log "CPU metrics file not found: $file"
        return 0
    fi
    
    log "Processing CPU metrics file: $file"
    
    local old_count=0
    local new_count=0
    local migrated_count=0
    
    > "$temp_file"  # Clear temp file
    
    while IFS= read -r line; do
        if [[ -z "$line" || "$line" =~ ^# ]]; then
            # Keep comments and empty lines
            echo "$line" >> "$temp_file"
            continue
        fi
        
        # Parse metric line: metric_name{labels} value [timestamp]
        if [[ "$line" =~ vm_cpu_adjustment_status\{([^}]+)\}[[:space:]]+([0-9.]+)([[:space:]]+[0-9]+)? ]]; then
            local labels="${BASH_REMATCH[1]}"
            local value="${BASH_REMATCH[2]}"
            local timestamp="${BASH_REMATCH[3]}"
            
            # Extract domain and rule_id from labels
            local domain=""
            local rule_id=""
            
            # Parse labels
            while [[ "$labels" =~ ([^,=]+)=\"([^\"]*)\",?(.*)$ ]]; do
                local key="${BASH_REMATCH[1]}"
                local val="${BASH_REMATCH[2]}"
                labels="${BASH_REMATCH[3]}"
                
                case "$key" in
                    domain)
                        domain="$val"
                        ;;
                    rule_id)
                        rule_id="$val"
                        ;;
                esac
            done
            
            if [[ -n "$domain" && -n "$rule_id" ]]; then
                if [[ "$rule_id" =~ ^cpu-.* ]]; then
                    # Already new format
                    echo "$line" >> "$temp_file"
                    ((new_count++))
                elif [[ "$rule_id" =~ ^[^-]+-[a-f0-9-]+$ ]]; then
                    # Old format: domain-uuid, convert to cpu-domain-uuid
                    local new_rule_id="cpu-$rule_id"
                    local new_line="vm_cpu_adjustment_status{domain=\"$domain\",rule_id=\"$new_rule_id\"} $value$timestamp"
                    echo "$new_line" >> "$temp_file"
                    ((old_count++))
                    ((migrated_count++))
                    
                    if [[ "$DRY_RUN" == "true" ]]; then
                        log "Would migrate: $rule_id -> $new_rule_id"
                    else
                        log "Migrated: $rule_id -> $new_rule_id"
                    fi
                else
                    # Keep unknown formats as-is
                    echo "$line" >> "$temp_file"
                    log "Warning: Unknown rule_id format: $rule_id"
                fi
            else
                # Keep lines without proper labels
                echo "$line" >> "$temp_file"
            fi
        else
            # Keep non-metric lines
            echo "$line" >> "$temp_file"
        fi
    done < "$file"
    
    log "CPU metrics summary: $old_count old format, $new_count already new format, $migrated_count migrated"
    
    if [[ "$DRY_RUN" == "false" && "$migrated_count" -gt 0 ]]; then
        backup_file "$file"
        mv "$temp_file" "$file"
        log "Updated CPU metrics file: $file"
    else
        rm -f "$temp_file"
    fi
}

# Function to clean bandwidth metrics
clean_bw_metrics() {
    local file="$1"
    local temp_file="$TEMP_DIR/$(basename "$file").tmp"
    
    if [[ ! -f "$file" ]]; then
        log "Bandwidth metrics file not found: $file"
        return 0
    fi
    
    log "Processing bandwidth metrics file: $file"
    
    local old_count=0
    local new_count=0
    local migrated_count=0
    
    > "$temp_file"  # Clear temp file
    
    while IFS= read -r line; do
        if [[ -z "$line" || "$line" =~ ^# ]]; then
            # Keep comments and empty lines
            echo "$line" >> "$temp_file"
            continue
        fi
        
        # Parse metric line
        if [[ "$line" =~ vm_bandwidth_adjustment_status\{([^}]+)\}[[:space:]]+([0-9.]+)([[:space:]]+[0-9]+)? ]]; then
            local labels="${BASH_REMATCH[1]}"
            local value="${BASH_REMATCH[2]}"
            local timestamp="${BASH_REMATCH[3]}"
            
            # Extract domain, rule_id, and type from labels
            local domain=""
            local rule_id=""
            local type=""
            
            # Parse labels
            local temp_labels="$labels"
            while [[ "$temp_labels" =~ ([^,=]+)=\"([^\"]*)\",?(.*)$ ]]; do
                local key="${BASH_REMATCH[1]}"
                local val="${BASH_REMATCH[2]}"
                temp_labels="${BASH_REMATCH[3]}"
                
                case "$key" in
                    domain)
                        domain="$val"
                        ;;
                    rule_id)
                        rule_id="$val"
                        ;;
                    type)
                        type="$val"
                        ;;
                esac
            done
            
            if [[ -n "$domain" && -n "$rule_id" && -n "$type" ]]; then
                if [[ "$rule_id" =~ ^bw-.* ]]; then
                    # Already new format
                    echo "$line" >> "$temp_file"
                    ((new_count++))
                elif [[ "$rule_id" =~ ^[^-]+-[a-f0-9-]+$ ]]; then
                    # Old format: domain-uuid, convert to adjust-bw-domain-uuid
                    local new_rule_id="adjust-bw-$rule_id"
                    local new_line="vm_bandwidth_adjustment_status{domain=\"$domain\",rule_id=\"$new_rule_id\",type=\"$type\"} $value$timestamp"
                    echo "$new_line" >> "$temp_file"
                    ((old_count++))
                    ((migrated_count++))
                    
                    if [[ "$DRY_RUN" == "true" ]]; then
                        log "Would migrate: $rule_id -> $new_rule_id (type=$type)"
                    else
                        log "Migrated: $rule_id -> $new_rule_id (type=$type)"
                    fi
                else
                    # Keep unknown formats as-is
                    echo "$line" >> "$temp_file"
                    log "Warning: Unknown rule_id format: $rule_id"
                fi
            else
                # Keep lines without proper labels
                echo "$line" >> "$temp_file"
            fi
        else
            # Keep non-metric lines
            echo "$line" >> "$temp_file"
        fi
    done < "$file"
    
    log "Bandwidth metrics summary: $old_count old format, $new_count already new format, $migrated_count migrated"
    
    if [[ "$DRY_RUN" == "false" && "$migrated_count" -gt 0 ]]; then
        backup_file "$file"
        mv "$temp_file" "$file"
        log "Updated bandwidth metrics file: $file"
    else
        rm -f "$temp_file"
    fi
}

# Main execution
main() {
    log "Starting rule_id metrics cleanup"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log "DRY RUN MODE - No changes will be made"
    fi
    
    # Check if metrics directory exists
    if [[ ! -d "$METRICS_DIR" ]]; then
        log "Error: Metrics directory not found: $METRICS_DIR"
        exit 1
    fi
    
    # Confirmation prompt
    if [[ "$FORCE" == "false" && "$DRY_RUN" == "false" ]]; then
        echo
        echo "This script will migrate rule_id metrics from old format to new typed format:"
        echo "  Old: domain-groupUUID"
        echo "  New: cpu-domain-groupUUID or adjust-bw-domain-groupUUID"
        echo
        echo "Files to be processed:"
        [[ -f "$CPU_METRICS_FILE" ]] && echo "  - $CPU_METRICS_FILE"
        [[ -f "$BW_METRICS_FILE" ]] && echo "  - $BW_METRICS_FILE"
        echo
        read -p "Do you want to continue? [y/N] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log "Operation cancelled by user"
            exit 0
        fi
    fi
    
    # Process metrics files
    clean_cpu_metrics "$CPU_METRICS_FILE"
    clean_bw_metrics "$BW_METRICS_FILE"
    
    # Cleanup temp directory
    rm -rf "$TEMP_DIR"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log "Dry run completed. Use --force to apply changes."
    else
        log "Rule_id metrics cleanup completed successfully"
        log "You may need to restart node_exporter to reload the metrics"
    fi
}

# Run main function
main "$@"
